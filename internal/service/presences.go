package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// dayPresences returns the presences of a date, sorted by start time.
func (s *Service) dayPresences(ctx context.Context, date time.Time) ([]api.Presence, error) {
	ds := timeutil.Date(date)
	ps, err := s.API.Presences(ctx, s.UserID, ds, ds)
	if err != nil {
		return nil, err
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].From < ps[j].From })
	return ps, nil
}

func openPresence(ps []api.Presence) *api.Presence {
	for i := range ps {
		if ps[i].To == "" {
			return &ps[i]
		}
	}
	return nil
}

func span(p api.Presence) string {
	to := p.To
	if to == "" {
		to = "…"
	}
	return p.From + "–" + to
}

// Start opens a presence on date at from ("HH:MM"). home nil means: keep the day's location if
// the day already has presences, otherwise use the configured default for that weekday.
// In MOCO, home office is a per-day setting; passing home changes it for the whole day.
// If MOCO is not reachable, the start is queued and a *QueuedError is returned.
func (s *Service) Start(ctx context.Context, date time.Time, from string, home *bool) (api.Presence, error) {
	from, err := timeutil.NormalizeClock(from)
	if err != nil {
		return api.Presence{}, err
	}
	p, err := s.start(ctx, date, from, home)
	if api.IsUnreachable(err) {
		return p, s.enqueue(store.QueueItem{Kind: store.KindStart, Date: timeutil.Date(date), From: from, Home: home}, err)
	}
	return p, err
}

func (s *Service) start(ctx context.Context, date time.Time, from string, home *bool) (api.Presence, error) {
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return api.Presence{}, err
	}
	if o := openPresence(ps); o != nil {
		return api.Presence{}, fmt.Errorf("already started at %s on %s (open presence) — use `moco stop` first", o.From, o.Date)
	}
	for _, p := range ps {
		if from >= p.From && from < p.To {
			return api.Presence{}, fmt.Errorf("%s falls into the existing presence %s", from, span(p))
		}
	}
	if home == nil && len(ps) == 0 {
		h := s.Cfg.HomeOfficeOn(date)
		home = &h
	}
	return s.API.CreatePresence(ctx, api.PresenceInput{Date: timeutil.Date(date), From: from, IsHomeOffice: home})
}

// Stop closes the open presence of date at to ("HH:MM").
// If MOCO is not reachable, the stop is queued and a *QueuedError is returned.
func (s *Service) Stop(ctx context.Context, date time.Time, to string) (api.Presence, error) {
	to, err := timeutil.NormalizeClock(to)
	if err != nil {
		return api.Presence{}, err
	}
	p, err := s.stop(ctx, date, to)
	if api.IsUnreachable(err) {
		return p, s.enqueue(store.QueueItem{Kind: store.KindStop, Date: timeutil.Date(date), To: to}, err)
	}
	return p, err
}

func (s *Service) stop(ctx context.Context, date time.Time, to string) (api.Presence, error) {
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return api.Presence{}, err
	}
	o := openPresence(ps)
	if o == nil {
		return api.Presence{}, fmt.Errorf("no open presence on %s — nothing to stop", timeutil.Date(date))
	}
	if to <= o.From {
		return api.Presence{}, fmt.Errorf("end %s is not after the start %s", to, o.From)
	}
	return s.API.UpdatePresence(ctx, o.ID, api.PresenceInput{To: to})
}

// BreakResult describes how a presence was split.
type BreakResult struct {
	Before api.Presence  // the presence ending at the break start
	After  *api.Presence // the presence starting at the break end (nil if the break ends the presence)

	cover *api.Presence // the presence being split, once known
}

// Break splits the presence covering from–to ("HH:MM") into two: one ending at from and one
// starting at to. An open presence stays open after the break.
// If MOCO is not reachable, the break (or its second half) is queued and a *QueuedError is returned.
func (s *Service) Break(ctx context.Context, date time.Time, from, to string) (BreakResult, error) {
	var res BreakResult
	from, err := timeutil.NormalizeClock(from)
	if err != nil {
		return res, err
	}
	if to, err = timeutil.NormalizeClock(to); err != nil {
		return res, err
	}
	if to <= from {
		return res, fmt.Errorf("break end %s is not after its start %s", to, from)
	}
	res, err = s.breakAt(ctx, date, from, to)
	var half *halfBreakError
	switch {
	case errors.As(err, &half) && api.IsUnreachable(half.Err):
		// The presence is already shortened; only the part after the break is missing.
		return res, s.enqueue(store.QueueItem{Kind: store.KindPresence, Date: timeutil.Date(date), From: half.From, To: half.To}, half.Err)
	case half == nil && api.IsUnreachable(err):
		item := store.QueueItem{Kind: store.KindBreak, Date: timeutil.Date(date), From: from, To: to}
		if res.cover != nil {
			item.CoverID, item.CoverTo = res.cover.ID, res.cover.To
		}
		return res, s.enqueue(item, err)
	}
	return res, err
}

// halfBreakError means a break shortened the presence but could not create the part after it.
type halfBreakError struct {
	Before   api.Presence // the original presence
	From, To string       // the missing part
	Err      error
}

func (e *halfBreakError) Error() string {
	return fmt.Sprintf("shortened %s to end at %s, but could not create the part after the break (%s–%s): %v",
		span(e.Before), e.From, e.From, orEllipsis(e.To), e.Err)
}

func (e *halfBreakError) Unwrap() error { return e.Err }

func (s *Service) breakAt(ctx context.Context, date time.Time, from, to string) (BreakResult, error) {
	var res BreakResult
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return res, err
	}
	var cover *api.Presence
	for i := range ps {
		p := &ps[i]
		if p.To == from {
			for _, q := range ps {
				if q.From == to {
					return res, fmt.Errorf("the break %s–%s is already recorded (%s, %s)", from, to, span(*p), span(q))
				}
			}
		}
		if p.From < from && (p.To == "" || p.To > from) {
			cover = p
		}
	}
	if cover == nil {
		return res, fmt.Errorf("no presence on %s covers %s — start the day first", timeutil.Date(date), from)
	}
	res.cover = cover
	if cover.To != "" && cover.To <= to {
		// The break reaches past the end of this presence: just shorten it.
		res.Before, err = s.API.UpdatePresence(ctx, cover.ID, api.PresenceInput{To: from})
		return res, err
	}
	for _, p := range ps {
		if p.ID != cover.ID && p.From < to && p.From >= from {
			return res, fmt.Errorf("the break %s–%s overlaps the presence %s", from, to, span(p))
		}
	}

	// Shorten first, then create the second part; the other order would overlap.
	res.Before, err = s.API.UpdatePresence(ctx, cover.ID, api.PresenceInput{To: from})
	if err != nil {
		return res, err
	}
	after, err := s.API.CreatePresence(ctx, api.PresenceInput{Date: cover.Date, From: to, To: cover.To})
	if err != nil {
		return res, &halfBreakError{Before: *cover, From: to, To: cover.To, Err: err}
	}
	res.After = &after
	return res, nil
}

func orEllipsis(s string) string {
	if s == "" {
		return "…"
	}
	return s
}

// PresencesBetween lists presences in [from, to].
func (s *Service) PresencesBetween(ctx context.Context, from, to time.Time) ([]api.Presence, error) {
	ps, err := s.API.Presences(ctx, s.UserID, timeutil.Date(from), timeutil.Date(to))
	if err != nil {
		return nil, err
	}
	sort.Slice(ps, func(i, j int) bool { return ps[i].Date+ps[i].From < ps[j].Date+ps[j].From })
	return ps, nil
}

// EditPresence changes from/to (normalized) and/or the day's location.
func (s *Service) EditPresence(ctx context.Context, id int64, from, to string, home *bool) (api.Presence, error) {
	in := api.PresenceInput{IsHomeOffice: home}
	var err error
	if from != "" {
		if in.From, err = timeutil.NormalizeClock(from); err != nil {
			return api.Presence{}, err
		}
	}
	if to != "" {
		if in.To, err = timeutil.NormalizeClock(to); err != nil {
			return api.Presence{}, err
		}
	}
	if in == (api.PresenceInput{}) {
		return api.Presence{}, fmt.Errorf("nothing to change — pass --from, --to, --home or --office")
	}
	return s.API.UpdatePresence(ctx, id, in)
}

// AddPresence creates a presence from–to ("HH:MM") on date; an empty to opens it (like Start).
// If MOCO is not reachable, the presence is queued and a *QueuedError is returned.
func (s *Service) AddPresence(ctx context.Context, date time.Time, from, to string) (api.Presence, error) {
	if strings.TrimSpace(to) == "" {
		return s.Start(ctx, date, from, nil)
	}
	from, err := timeutil.NormalizeClock(from)
	if err != nil {
		return api.Presence{}, err
	}
	if to, err = timeutil.NormalizeClock(to); err != nil {
		return api.Presence{}, err
	}
	if to <= from {
		return api.Presence{}, fmt.Errorf("end %s is not after the start %s", to, from)
	}
	p, err := s.addPresence(ctx, date, from, to)
	if api.IsUnreachable(err) {
		return p, s.enqueue(store.QueueItem{Kind: store.KindPresence, Date: timeutil.Date(date), From: from, To: to}, err)
	}
	return p, err
}

func (s *Service) addPresence(ctx context.Context, date time.Time, from, to string) (api.Presence, error) {
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return api.Presence{}, err
	}
	in := api.Presence{From: from, To: to}
	for _, p := range ps {
		if overlap(in, p) {
			return api.Presence{}, fmt.Errorf("%s–%s overlaps the presence %s", from, to, span(p))
		}
	}
	var home *bool
	if len(ps) == 0 {
		h := s.Cfg.HomeOfficeOn(date)
		home = &h
	}
	return s.API.CreatePresence(ctx, api.PresenceInput{Date: timeutil.Date(date), From: from, To: to, IsHomeOffice: home})
}

// overlap treats an open presence as running until the end of the day, like MOCO.
func overlap(a, b api.Presence) bool {
	end := func(p api.Presence) string {
		if p.To == "" {
			return "24:00"
		}
		return p.To
	}
	return a.From < end(b) && b.From < end(a)
}

// DeletePresence deletes a presence.
func (s *Service) DeletePresence(ctx context.Context, id int64) error {
	return s.API.DeletePresence(ctx, id)
}

// MergePresences removes the break after the presence id: the presence is deleted and the next
// presence of the day is extended back to its start. (MOCO can't reopen a presence by PATCH, so
// the later one is kept: it may be open.) Returns the merged presence.
func (s *Service) MergePresences(ctx context.Context, date time.Time, id int64) (api.Presence, error) {
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return api.Presence{}, err
	}
	i := -1
	for j := range ps {
		if ps[j].ID == id {
			i = j
		}
	}
	switch {
	case i < 0:
		return api.Presence{}, fmt.Errorf("presence %d not found on %s", id, timeutil.Date(date))
	case i == len(ps)-1:
		return api.Presence{}, fmt.Errorf("%s is the last presence of the day — nothing to merge it with", span(ps[i]))
	}
	first, next := ps[i], ps[i+1]
	if err := s.API.DeletePresence(ctx, first.ID); err != nil {
		return api.Presence{}, err
	}
	merged, err := s.API.UpdatePresence(ctx, next.ID, api.PresenceInput{From: first.From})
	if err != nil {
		return api.Presence{}, fmt.Errorf("deleted %s, but could not extend %s to start at %s — add %s again: %w",
			span(first), span(next), first.From, span(first), err)
	}
	return merged, nil
}

// SetDayLocation sets home office or office for all presences of date (a per-day setting in MOCO).
func (s *Service) SetDayLocation(ctx context.Context, date time.Time, home bool) error {
	ps, err := s.dayPresences(ctx, date)
	if err != nil {
		return err
	}
	if len(ps) == 0 {
		return fmt.Errorf("no presence on %s — the location is stored with the presences", timeutil.Date(date))
	}
	_, err = s.API.UpdatePresence(ctx, ps[0].ID, api.PresenceInput{IsHomeOffice: &home})
	return err
}
