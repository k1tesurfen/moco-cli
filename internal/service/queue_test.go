package service

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

func asQueued(t *testing.T, err error) *QueuedError {
	t.Helper()
	var q *QueuedError
	if !errors.As(err, &q) {
		t.Fatalf("want *QueuedError, got %v", err)
	}
	return q
}

func sync(t *testing.T, s *Service, retryFailed bool) SyncResult {
	t.Helper()
	res, err := s.SyncQueue(context.Background(), retryFailed)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestQueuePresencesWhileDownAndReplayInOrder(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	fake.SetDown(http.StatusServiceUnavailable)

	q := asQueued(t, second(s.Start(ctx, day, "8", ptr(false))))
	if q.Item.ID != 1 || q.Item.From != "08:00" || q.Pending != 1 {
		t.Errorf("queued start: %+v", q)
	}
	asQueued(t, second(s.Break(ctx, day, "13:00", "14:00")))
	asQueued(t, second(s.Stop(ctx, day, "17:07")))

	res := sync(t, s, false)
	if len(res.Sent) != 0 || res.Pending != 3 || res.Stopped == nil || !api.IsUnreachable(res.Stopped) {
		t.Errorf("sync while down: %+v", res)
	}

	fake.SetDown(0)
	res = sync(t, s, false)
	if len(res.Sent) != 3 || res.Pending != 0 || res.Stopped != nil {
		t.Fatalf("sync: %+v", res)
	}
	for i, it := range res.Sent {
		if it.ID != int64(i+1) {
			t.Errorf("sent[%d] = #%d", i, it.ID)
		}
	}
	if got := fake.Dump("2026-10-06"); got != "08:00-13:00 14:00-17:07" {
		t.Errorf("day = %q", got)
	}

	// A second sync has nothing to do.
	if res := sync(t, s, false); len(res.Sent) != 0 {
		t.Errorf("second sync sent %v", res.Sent)
	}
}

func second[T any](_ T, err error) error { return err }

func TestQueueLogAndNoDuplicateWhenReplyWasLost(t *testing.T) {
	s, fake := newActivityService(t)
	ctx := context.Background()

	fake.DropNextReply() // MOCO stores it, the client sees 502
	q := asQueued(t, second(s.LogActivity(ctx, day, intern, intern.Tasks[0], 67*60, "Fix login")))
	if q.Item.Seconds != 75*60 || q.Item.ProjectName != intern.Name || q.Item.TaskName != "Programmierung" {
		t.Errorf("queued: %+v", q.Item)
	}
	if n := len(fake.Activities("2026-10-06")); n != 1 {
		t.Fatalf("fake has %d activities, want 1", n)
	}
	if res := sync(t, s, false); len(res.Sent) != 1 || res.Pending != 0 {
		t.Errorf("sync: %+v", res)
	}
	if n := len(fake.Activities("2026-10-06")); n != 1 {
		t.Errorf("replay duplicated the activity: %d", n)
	}
	if st, _ := s.Store.Load(); len(st.Recent) == 0 {
		t.Error("queued log should still be remembered as recent")
	}
}

func TestQueueLogIgnoresOlderIdenticalActivity(t *testing.T) {
	s, fake := newActivityService(t)
	ctx := context.Background()
	// The same entry logged deliberately earlier that day must not swallow the queued one.
	fake.AddActivity(api.Activity{Date: "2026-10-06", Project: api.Ref{ID: 1}, Task: api.Ref{ID: 11},
		Seconds: 1800, Description: "Standup", CreatedAt: s.Now().Add(-3 * time.Hour)})

	fake.SetDown(http.StatusBadGateway)
	asQueued(t, second(s.LogActivity(ctx, day, intern, intern.Tasks[0], 1800, "Standup")))
	asQueued(t, second(s.LogActivity(ctx, day, intern, intern.Tasks[0], 1800, "Standup")))
	fake.SetDown(0)

	if res := sync(t, s, false); len(res.Sent) != 2 {
		t.Errorf("sync: %+v", res)
	}
	if n := len(fake.Activities("2026-10-06")); n != 3 {
		t.Errorf("activities = %d, want 3", n)
	}
}

func TestQueueRejectedItemIsKeptAsFailed(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	fake.SetDown(http.StatusServiceUnavailable)
	asQueued(t, second(s.Stop(ctx, day, "17:00"))) // nothing to stop once MOCO is back
	asQueued(t, second(s.Start(ctx, day, "08:00", nil)))
	fake.SetDown(0)

	res := sync(t, s, false)
	if len(res.Rejected) != 1 || res.Rejected[0].Kind != store.KindStop || len(res.Sent) != 1 || res.Failed != 1 || res.Pending != 0 {
		t.Fatalf("sync: %+v", res)
	}
	// Failed items are not retried automatically …
	if res := sync(t, s, false); len(res.Sent)+len(res.Rejected) != 0 {
		t.Errorf("auto sync retried a failed item: %+v", res)
	}
	// … only on request; now there is an open presence to stop.
	if res := sync(t, s, true); len(res.Sent) != 1 || res.Failed != 0 {
		t.Errorf("retry: %+v", res)
	}
	if got := fake.Dump("2026-10-06"); got != "08:00-17:00 home" {
		t.Errorf("day = %q", got)
	}
}

func TestQueueServerErrorOnWriteGivesUpAfterRetries(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	fake.SetDown(http.StatusServiceUnavailable)
	asQueued(t, second(s.Start(ctx, day, "08:00", nil)))
	fake.SetDown(0)
	fake.SetFailWrites(http.StatusInternalServerError)

	for i := 1; i < maxServerErrors; i++ {
		if res := sync(t, s, false); res.Pending != 1 || res.Stopped == nil {
			t.Fatalf("attempt %d: %+v", i, res)
		}
	}
	if res := sync(t, s, false); len(res.Rejected) != 1 || res.Failed != 1 {
		t.Errorf("last attempt: %+v", res)
	}
}

func TestQueueHalfDoneBreak(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00"})

	fake.DropNextReply() // the shortening PATCH works but its answer is lost …
	q := asQueued(t, second(s.Break(ctx, day, "13:00", "14:00")))
	if q.Item.Kind != store.KindBreak {
		t.Fatalf("queued %+v", q.Item)
	}
	if got := fake.Dump("2026-10-06"); got != "08:00-13:00" {
		t.Fatalf("day = %q", got)
	}
	// … the replay sees the shortened presence and only adds the rest.
	fake.SetFailWrites(http.StatusServiceUnavailable)
	res := sync(t, s, false)
	items, _ := s.Queue()
	if res.Pending != 1 || items[0].Kind != store.KindPresence || items[0].From != "14:00" {
		t.Fatalf("after half replay: %+v %+v", res, items)
	}
	fake.SetFailWrites(0)
	if res := sync(t, s, false); len(res.Sent) != 1 {
		t.Errorf("sync: %+v", res)
	}
	if got := fake.Dump("2026-10-06"); got != "08:00-13:00 14:00-…" {
		t.Errorf("day = %q", got)
	}
}

func TestDropQueued(t *testing.T) {
	s, fake := newTestService(t)
	fake.SetDown(http.StatusServiceUnavailable)
	asQueued(t, second(s.Start(context.Background(), day, "08:00", nil)))
	if _, err := s.Store.Drop(7); err == nil {
		t.Error("dropping an unknown id should fail")
	}
	if it, err := s.Store.Drop(1); err != nil || it.Kind != store.KindStart {
		t.Errorf("drop: %+v %v", it, err)
	}
	if items, _ := s.Queue(); len(items) != 0 {
		t.Errorf("queue = %+v", items)
	}
}

func TestSyncSkipsWhileAnotherProcessSyncs(t *testing.T) {
	s, _ := newTestService(t)
	unlock, ok, err := s.Store.TrySyncLock()
	if err != nil || !ok {
		t.Fatal(ok, err)
	}
	defer unlock()
	if res := sync(t, s, false); !res.Busy {
		t.Errorf("want busy: %+v", res)
	}
}
