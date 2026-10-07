package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// QueuedError means MOCO was not reachable and the write was put into the local queue instead.
// It is not saved in MOCO yet; callers must say so unmistakably.
type QueuedError struct {
	Item    store.QueueItem
	Pending int // items waiting, including this one
	Cause   error
}

func (e *QueuedError) Error() string {
	return fmt.Sprintf("MOCO not reachable — queued #%d (%s), not saved in MOCO yet: %v", e.Item.ID, e.Item.Summary(), e.Cause)
}

func (s *Service) enqueue(item store.QueueItem, cause error) error {
	q := &QueuedError{Cause: cause}
	err := s.Store.Update(func(st *store.State) error {
		q.Item = st.Enqueue(item, s.Now())
		q.Pending, _ = st.QueueCounts()
		return nil
	})
	if err != nil {
		return fmt.Errorf("%w — and it could not be queued either: %v", cause, err)
	}
	return q
}

// Queue returns the queued items, oldest first.
func (s *Service) Queue() ([]store.QueueItem, error) {
	st, err := s.Store.Load()
	return st.Queue, err
}

// SyncResult reports what a queue sync did.
type SyncResult struct {
	Sent     []store.QueueItem
	Rejected []store.QueueItem // rejected by MOCO in this run; kept as failed
	Pending  int               // still waiting afterwards
	Failed   int               // failed items in the queue afterwards
	Busy     bool              // another process is syncing right now
	// Stopped is why the sync stopped before the end (MOCO unreachable, token rejected), or nil.
	Stopped error
}

// maxServerErrors is how often a write may answer 5xx before the item counts as rejected.
// MOCO answers some invalid input with 500, which must not be retried forever.
const maxServerErrors = 3

// SyncQueue sends the queued writes in order. Failed items are only retried if retryFailed is set.
// Sending stops at the first sign that MOCO is unreachable, so the order is kept.
func (s *Service) SyncQueue(ctx context.Context, retryFailed bool) (SyncResult, error) {
	var res SyncResult
	unlock, ok, err := s.Store.TrySyncLock()
	if err != nil {
		return res, err
	}
	if !ok {
		res.Busy = true
		return res, s.countQueue(&res)
	}
	defer unlock()

	st, err := s.Store.Load()
	if err != nil {
		return res, err
	}
	claimed := map[int64]bool{} // activities created or matched in this run
	for _, item := range st.Queue {
		if item.Failed && !retryFailed {
			continue
		}
		rest, rerr := s.replay(ctx, item, claimed)
		var stop bool
		err := s.Store.Update(func(st *store.State) error {
			q := st.QueueItem(item.ID)
			if q == nil {
				return nil // dropped meanwhile
			}
			if rerr == nil {
				res.Sent = append(res.Sent, *q)
				st.Dequeue(item.ID) // q is invalid from here on
				return nil
			}
			if rest != nil {
				q.Kind, q.From, q.To = rest.Kind, rest.From, rest.To
			}
			q.Attempts++
			q.LastError = rerr.Error()
			var ae *api.APIError
			switch {
			case errors.As(rerr, &ae) && ae.Status >= 500 && ae.Method != http.MethodGet:
				// MOCO is up (the reads before worked) but the write itself fails.
				q.Failed = q.Attempts >= maxServerErrors
				stop = !q.Failed
			case api.IsUnreachable(rerr), api.IsStatus(rerr, http.StatusUnauthorized),
				api.IsStatus(rerr, http.StatusTooManyRequests), ctx.Err() != nil:
				stop = true
			default:
				q.Failed = true
			}
			if q.Failed {
				res.Rejected = append(res.Rejected, *q)
			}
			return nil
		})
		if err != nil {
			return res, err
		}
		if stop {
			res.Stopped = rerr
			break
		}
	}
	return res, s.countQueue(&res)
}

func (s *Service) countQueue(res *SyncResult) error {
	st, err := s.Store.Load()
	res.Pending, res.Failed = st.QueueCounts()
	return err
}

// replay sends one queued item. Each kind first checks whether MOCO already has the result
// (the original request may have reached MOCO before the connection failed), so a replay never
// creates duplicates. If a break is only half done, rest is what remains to be sent.
func (s *Service) replay(ctx context.Context, q store.QueueItem, claimed map[int64]bool) (rest *store.QueueItem, err error) {
	if q.Kind == store.KindBreak {
		return s.replayBreak(ctx, q)
	}
	return nil, s.replayOne(ctx, q, claimed)
}

func (s *Service) replayBreak(ctx context.Context, q store.QueueItem) (*store.QueueItem, error) {
	date, err := time.ParseInLocation(timeutil.DateLayout, q.Date, s.Now().Location())
	if err != nil {
		return nil, err
	}
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return nil, err
	}
	for _, p := range ps {
		if p.To != q.From {
			continue
		}
		for _, n := range ps {
			if n.From == q.To {
				return nil, nil // already recorded
			}
		}
		if p.ID == q.CoverID {
			// The presence was shortened, but the part after the break is missing (or not needed
			// because the presence ended within the break).
			if q.CoverTo != "" && q.CoverTo <= q.To {
				return nil, nil
			}
			rest := store.QueueItem{Kind: store.KindPresence, Date: q.Date, From: q.To, To: q.CoverTo}
			if err := s.replayOne(ctx, rest, nil); err != nil {
				return &rest, err
			}
			return nil, nil
		}
	}
	_, err = s.breakAt(ctx, date, q.From, q.To)
	var half *halfBreakError
	if errors.As(err, &half) {
		return &store.QueueItem{Kind: store.KindPresence, From: half.From, To: half.To}, half.Err
	}
	return nil, err
}

func (s *Service) replayOne(ctx context.Context, q store.QueueItem, claimed map[int64]bool) error {
	date, err := time.ParseInLocation(timeutil.DateLayout, q.Date, s.Now().Location())
	if err != nil {
		return err
	}
	switch q.Kind {
	case store.KindLog:
		as, err := s.API.Activities(ctx, s.UserID, q.Date, q.Date)
		if err != nil {
			return err
		}
		// Clocks differ and the failed request may have been sent a little before queueing.
		since := q.QueuedAt.Add(-2 * time.Minute)
		for _, a := range as {
			if !claimed[a.ID] && a.Project.ID == q.ProjectID && a.Task.ID == q.TaskID &&
				a.Seconds == q.Seconds && a.Description == q.Description && !a.CreatedAt.Before(since) {
				claimed[a.ID] = true
				return nil
			}
		}
		sec := q.Seconds
		a, err := s.API.CreateActivity(ctx, api.ActivityInput{
			Date: q.Date, ProjectID: q.ProjectID, TaskID: q.TaskID, Seconds: &sec, Description: q.Description,
		})
		if err == nil {
			claimed[a.ID] = true
		}
		return err

	case store.KindEdit:
		in := api.ActivityInput{Description: q.Description}
		if q.Seconds > 0 {
			sec := q.Seconds
			in.Seconds = &sec
		}
		_, err := s.API.UpdateActivity(ctx, q.ActivityID, in)
		return err

	case store.KindStart:
		ps, err := s.dayPresences(ctx, date)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.From == q.From {
				return nil
			}
		}
		_, err = s.start(ctx, date, q.From, q.Home)
		return err

	case store.KindStop:
		ps, err := s.dayPresences(ctx, date)
		if err != nil {
			return err
		}
		if openPresence(ps) == nil {
			for _, p := range ps {
				if p.To == q.To {
					return nil
				}
			}
		}
		_, err = s.stop(ctx, date, q.To)
		return err

	case store.KindPresence:
		ps, err := s.dayPresences(ctx, date)
		if err != nil {
			return err
		}
		for _, p := range ps {
			if p.From == q.From {
				return nil
			}
		}
		_, err = s.API.CreatePresence(ctx, api.PresenceInput{Date: q.Date, From: q.From, To: q.To})
		return err
	}
	return fmt.Errorf("unknown queue item kind %q", q.Kind)
}
