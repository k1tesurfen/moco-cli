package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/fakemoco"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

var day = time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC) // Tuesday

func newTestService(t *testing.T) (*Service, *fakemoco.Server) {
	t.Helper()
	fake := fakemoco.New(t)
	cfg := config.Default()
	cfg.Location.Tue = "home"
	return &Service{
		Cfg:    cfg,
		API:    fake.Client(),
		Store:  store.NewAt(t.TempDir()),
		UserID: fakemoco.UserID,
		Now:    func() time.Time { return day.Add(20 * time.Hour) },
	}, fake
}

func ptr[T any](v T) *T { return &v }

func TestStartBreakStop(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()

	p, err := s.Start(ctx, day, "8", nil)
	if err != nil {
		t.Fatal(err)
	}
	if !p.IsHomeOffice {
		t.Error("Tuesday default should be home")
	}
	if _, err := s.Start(ctx, day, "09:00", nil); err == nil || !strings.Contains(err.Error(), "already started at 08:00") {
		t.Errorf("second start: %v", err)
	}

	res, err := s.Break(ctx, day, "13:00", "14:00")
	if err != nil {
		t.Fatal(err)
	}
	if res.Before.To != "13:00" || res.After == nil || res.After.From != "14:00" || res.After.To != "" {
		t.Errorf("break result: %+v", res)
	}
	if _, err := s.Break(ctx, day, "13:00", "14:00"); err == nil || !strings.Contains(err.Error(), "already recorded") {
		t.Errorf("second break: %v", err)
	}

	if _, err := s.Stop(ctx, day, "17:07"); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Dump("2026-10-06"), "08:00-13:00 14:00-17:07 home"; got != want {
		t.Errorf("day = %q, want %q", got, want)
	}
	if _, err := s.Stop(ctx, day, "18:00"); err == nil || !strings.Contains(err.Error(), "no open presence") {
		t.Errorf("second stop: %v", err)
	}
}

func TestBreakSplitsClosedPresence(t *testing.T) {
	s, fake := newTestService(t)
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "17:00"})
	res, err := s.Break(context.Background(), day, "12:30", "13:15")
	if err != nil {
		t.Fatal(err)
	}
	if res.After == nil || res.After.To != "17:00" {
		t.Errorf("after = %+v", res.After)
	}
	if got, want := fake.Dump("2026-10-06"), "08:00-12:30 13:15-17:00"; got != want {
		t.Errorf("day = %q, want %q", got, want)
	}
}

func TestBreakAtEndOfPresenceOnlyShortens(t *testing.T) {
	s, fake := newTestService(t)
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "13:30"})
	res, err := s.Break(context.Background(), day, "13:00", "14:00")
	if err != nil {
		t.Fatal(err)
	}
	if res.After != nil {
		t.Errorf("unexpected after part %+v", res.After)
	}
	if got, want := fake.Dump("2026-10-06"), "08:00-13:00"; got != want {
		t.Errorf("day = %q, want %q", got, want)
	}
}

func TestBreakWithoutPresence(t *testing.T) {
	s, _ := newTestService(t)
	if _, err := s.Break(context.Background(), day, "13:00", "14:00"); err == nil || !strings.Contains(err.Error(), "start the day first") {
		t.Errorf("err = %v", err)
	}
}

func TestStartInsideExistingPresence(t *testing.T) {
	s, fake := newTestService(t)
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "13:00"})
	if _, err := s.Start(context.Background(), day, "10:00", nil); err == nil || !strings.Contains(err.Error(), "falls into") {
		t.Errorf("err = %v", err)
	}
	// After the existing block is fine and keeps the day's location (office here).
	p, err := s.Start(context.Background(), day, "14:00", nil)
	if err != nil {
		t.Fatal(err)
	}
	if p.IsHomeOffice {
		t.Error("second presence must keep the day's location, not the weekday default")
	}
}

func TestStopBeforeStart(t *testing.T) {
	s, fake := newTestService(t)
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00"})
	if _, err := s.Stop(context.Background(), day, "07:30"); err == nil {
		t.Error("expected error")
	}
}

func TestInvalidTimesNeverReachMOCO(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	_, err1 := s.Start(ctx, day, "25:00", nil)
	_, err2 := s.Stop(ctx, day, "x")
	_, err3 := s.Break(ctx, day, "13", "12")
	_, err4 := s.EditPresence(ctx, 1, "8:3", "", nil)
	for i, err := range []error{err1, err2, err3, err4} {
		if err == nil {
			t.Errorf("case %d: expected error", i+1)
		}
	}
	if len(fake.Requests) != 0 {
		t.Errorf("requests sent: %v", fake.Requests)
	}
}

func TestEditPresenceLocationAppliesToDay(t *testing.T) {
	s, fake := newTestService(t)
	id := fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "13:00"})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "14:00", To: "17:00"})
	if _, err := s.EditPresence(context.Background(), id, "7:45", "", ptr(true)); err != nil {
		t.Fatal(err)
	}
	if got, want := fake.Dump("2026-10-06"), "07:45-13:00 14:00-17:00 home"; got != want {
		t.Errorf("day = %q, want %q", got, want)
	}
}
