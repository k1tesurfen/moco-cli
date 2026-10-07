package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/store"
)

// newTimerService returns a service whose clock (shared with the fake) is at *clock.
func newTimerService(t *testing.T) (*Service, func(time.Duration), func() string) {
	s, fake := newActivityService(t)
	clock := day.Add(9 * time.Hour)
	s.Now = func() time.Time { return clock }
	fake.Now = s.Now
	advance := func(d time.Duration) { clock = clock.Add(d) }
	dump := func() string {
		out := ""
		for _, a := range fake.Activities("2026-10-06") {
			out += a.Description + " "
			if a.TimerRunning() {
				out += "running"
			} else {
				out += (time.Duration(a.Seconds) * time.Second).String()
			}
			out += ";"
		}
		return out
	}
	return s, advance, dump
}

func TestTimerStartStopRoundsUpAndSetsDescription(t *testing.T) {
	s, advance, dump := newTimerService(t)
	ctx := context.Background()

	a, err := s.StartTimer(ctx, intern, intern.Tasks[0], "")
	if err != nil {
		t.Fatal(err)
	}
	if !a.TimerRunning() || a.Description != TimerPlaceholder || a.Date != "2026-10-06" {
		t.Errorf("started: %+v", a)
	}
	if d, _ := s.Day(ctx, s.Now()); d.OpenPresence == nil || d.OpenPresence.From != "09:00" {
		t.Errorf("MOCO opens a presence with the timer: %+v", d.Presences)
	}
	var running *TimerRunningError
	if _, err := s.StartTimer(ctx, intern, intern.Tasks[1], "x"); !errors.As(err, &running) {
		t.Errorf("second start: %v", err)
	}

	advance(67 * time.Minute)
	if r, _ := s.RunningTimer(ctx); r == nil || TimerSeconds(*r, s.Now()) != 67*60 {
		t.Errorf("running = %+v", r)
	}
	if _, err := s.StopTimer(ctx, ""); err == nil {
		t.Error("stopping a placeholder timer without description must fail")
	}
	res, err := s.StopTimer(ctx, "Fix login")
	if err != nil {
		t.Fatal(err)
	}
	if res.Tracked != 67*60 || res.Activity.Seconds != 75*60 || res.Activity.Description != "Fix login" {
		t.Errorf("stopped: %+v", res)
	}
	if got := dump(); got != "Fix login 1h15m0s;" {
		t.Errorf("activities = %q", got)
	}
	if _, err := s.StopTimer(ctx, "x"); !errors.Is(err, ErrNoTimer) {
		t.Errorf("stop without timer: %v", err)
	}
}

func TestTimerShortRunBooksOneStepAndKeepsDescription(t *testing.T) {
	s, advance, _ := newTimerService(t)
	ctx := context.Background()
	if _, err := s.StartTimer(ctx, intern, intern.Tasks[0], "Call"); err != nil {
		t.Fatal(err)
	}
	advance(10 * time.Second)
	res, err := s.StopTimer(ctx, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Activity.Seconds != 15*60 || res.Activity.Description != "Call" {
		t.Errorf("stopped: %+v", res.Activity)
	}
}

func TestTimerCancel(t *testing.T) {
	s, advance, dump := newTimerService(t)
	ctx := context.Background()
	if _, err := s.StartTimer(ctx, intern, intern.Tasks[0], ""); err != nil {
		t.Fatal(err)
	}
	advance(time.Minute)
	if _, err := s.CancelTimer(ctx); err != nil {
		t.Fatal(err)
	}
	if got := dump(); got != "" {
		t.Errorf("activities = %q", got)
	}
}

func TestTimerStopLostAnswer(t *testing.T) {
	s, fake := newActivityService(t)
	clock := day.Add(9 * time.Hour)
	s.Now = func() time.Time { return clock }
	fake.Now = s.Now
	ctx := context.Background()
	if _, err := s.StartTimer(ctx, intern, intern.Tasks[0], ""); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(20 * time.Minute)
	fake.DropNextReply() // stop_timer is processed, its answer lost
	if _, err := s.StopTimer(ctx, "Review"); err == nil || !strings.Contains(err.Error(), "moco timer status") {
		t.Fatalf("want a hint to check the timer, got %v", err)
	}
}

func TestTimerStopQueuesUpdateWhenSavingFails(t *testing.T) {
	s, fake := newActivityService(t)
	clock := day.Add(9 * time.Hour)
	s.Now = func() time.Time { return clock }
	fake.Now = s.Now
	ctx := context.Background()
	if _, err := s.StartTimer(ctx, intern, intern.Tasks[0], ""); err != nil {
		t.Fatal(err)
	}
	clock = clock.Add(20 * time.Minute)

	// stop_timer works, the PATCH with rounded time + description does not.
	fake.Fail = func(r *http.Request) int {
		if r.Method == http.MethodPatch && !strings.HasSuffix(r.URL.Path, "_timer") {
			return http.StatusServiceUnavailable
		}
		return 0
	}
	res, err := s.StopTimer(ctx, "Review")
	q := asQueued(t, err)
	if q.Item.Kind != store.KindEdit || q.Item.Seconds != 30*60 || q.Item.Description != "Review" || res.Tracked != 20*60 {
		t.Fatalf("queued %+v, res %+v", q.Item, res)
	}
	fake.Fail = nil
	if r := sync(t, s, false); len(r.Sent) != 1 {
		t.Fatalf("sync: %+v", r)
	}
	a := fake.Activities("2026-10-06")[0]
	if a.Seconds != 30*60 || a.Description != "Review" || a.TimerRunning() {
		t.Errorf("after sync: %+v", a)
	}
}

func TestTimerStartCleansUpWhenTimerFails(t *testing.T) {
	s, fake := newActivityService(t)
	clock := day.Add(9 * time.Hour)
	s.Now = func() time.Time { return clock }
	fake.Now = s.Now
	// An activity for another day doesn't auto-start, so start_timer is called and fails.
	fake.Now = func() time.Time { return day.AddDate(0, 0, 1) } // MOCO is already a day ahead
	fake.Fail = func(r *http.Request) int {
		if strings.HasSuffix(r.URL.Path, "/start_timer") {
			return http.StatusUnprocessableEntity
		}
		return 0
	}
	if _, err := s.StartTimer(context.Background(), intern, intern.Tasks[0], ""); err == nil {
		t.Fatal("want error")
	}
	if n := len(fake.Activities("2026-10-06")); n != 0 {
		t.Errorf("left %d activities behind", n)
	}
}
