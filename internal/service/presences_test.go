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

func TestCheckPresence(t *testing.T) {
	day := []api.Presence{
		{ID: 1, From: "08:00", To: "12:30"},
		{ID: 2, From: "13:15"}, // open
	}
	closed := []api.Presence{{ID: 1, From: "08:00", To: "12:30"}}
	tests := []struct {
		name     string
		ps       []api.Presence
		id       int64
		from, to string
		want     string // normalized "from-to", or a part of the error
	}{
		{"free afternoon", closed, 0, "13.15", "1700", "13:15-17:00"},
		{"open after the last", closed, 0, "13:15", "", "13:15-"},
		{"touching is fine", closed, 0, "12:30", "13:00", "12:30-13:00"},
		{"before the first", closed, 0, "6", "8", "06:00-08:00"},
		{"overlap", closed, 0, "12:00", "14:00", "12:00–14:00 overlaps the presence 08:00–12:30"},
		{"open overlaps a later one", closed, 0, "07:00", "", "07:00–… overlaps the presence 08:00–12:30"},
		{"after an open one", day, 0, "18:00", "19:00", "open presence 13:15–… — MOCO counts it until midnight"},
		{"end before start", closed, 0, "14:00", "13:00", "end 13:00 is not after the start 14:00"},
		{"no start", closed, 0, " ", "13:00", "a start time is required"},
		{"bad time", closed, 0, "25:00", "", "invalid time"},
		{"closing the open one", day, 2, "13:15", "17:00", "13:15-17:00"},
		{"moving the open one into the first", day, 2, "12:00", "17:00", "overlaps the presence 08:00–12:30"},
		{"editing itself", day, 1, "07:30", "12:45", "07:30-12:45"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			from, to, err := CheckPresence(tt.ps, tt.id, tt.from, tt.to)
			got := from + "-" + to
			if err != nil {
				got = err.Error()
			}
			if !strings.Contains(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSetPresence(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	first := fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "12:30"})
	open := fake.AddPresence(api.Presence{Date: "2026-10-06", From: "13:15"})

	if _, err := s.SetPresence(ctx, day, open, "12:00", ""); err == nil || !strings.Contains(err.Error(), "overlaps the presence 08:00–12:30") {
		t.Errorf("overlap: %v", err)
	}
	if _, err := s.SetPresence(ctx, day, open, "13:15", ""); err == nil || err.Error() != "nothing changed" {
		t.Errorf("unchanged: %v", err)
	}
	p, err := s.SetPresence(ctx, day, open, "13:00", "17:30")
	if err != nil {
		t.Fatal(err)
	}
	if p.From != "13:00" || p.To != "17:30" {
		t.Errorf("updated %+v", p)
	}
	if _, err := s.SetPresence(ctx, day, first, "08:00", ""); err == nil || !strings.Contains(err.Error(), "can't reopen") {
		t.Errorf("reopen: %v", err)
	}
	if got, want := fake.Dump("2026-10-06"), "08:00-12:30 13:00-17:30"; got != want {
		t.Errorf("day = %q, want %q", got, want)
	}
}
