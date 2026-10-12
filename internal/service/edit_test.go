package service

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

func TestAddPresence(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()

	p, err := s.AddPresence(ctx, day, "8", "12:30")
	if err != nil {
		t.Fatal(err)
	}
	if p.From != "08:00" || p.To != "12:30" || !p.IsHomeOffice {
		t.Errorf("added %+v", p)
	}
	if _, err := s.AddPresence(ctx, day, "12", "13"); err == nil || !strings.Contains(err.Error(), "overlaps") {
		t.Errorf("overlap: %v", err)
	}
	if _, err := s.AddPresence(ctx, day, "14", "13"); err == nil {
		t.Error("end before start accepted")
	}
	open, err := s.AddPresence(ctx, day, "13:30", "")
	if err != nil {
		t.Fatal(err)
	}
	if open.To != "" {
		t.Errorf("empty end should open the presence: %+v", open)
	}
	if n := len(fake.Presences("2026-10-06")); n != 2 {
		t.Errorf("%d presences, want 2", n)
	}
}

func TestAddPresenceQueued(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	fake.SetDown(http.StatusServiceUnavailable)
	_, err := s.AddPresence(ctx, day, "8", "12")
	var q *QueuedError
	if !errors.As(err, &q) || q.Item.Kind != store.KindPresence || q.Item.From != "08:00" || q.Item.To != "12:00" {
		t.Fatalf("err = %v", err)
	}
	fake.SetDown(0)
	if _, err := s.SyncQueue(ctx, false); err != nil {
		t.Fatal(err)
	}
	if ps := fake.Presences("2026-10-06"); len(ps) != 1 || ps[0].To != "12:00" {
		t.Errorf("after sync: %+v", ps)
	}
}

func TestMergePresences(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	first := fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "13:00"})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "14:00"})

	m, err := s.MergePresences(ctx, day, first)
	if err != nil {
		t.Fatal(err)
	}
	if m.From != "08:00" || m.To != "" {
		t.Errorf("merged %+v", m)
	}
	ps := fake.Presences("2026-10-06")
	if len(ps) != 1 || ps[0].From != "08:00" || ps[0].To != "" {
		t.Errorf("after merge: %+v", ps)
	}
	if _, err := s.MergePresences(ctx, day, ps[0].ID); err == nil || !strings.Contains(err.Error(), "last presence") {
		t.Errorf("merge last: %v", err)
	}
}

func TestSetDayLocation(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	if err := s.SetDayLocation(ctx, day, true); err == nil {
		t.Error("location without presence accepted")
	}
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "13:00"})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "14:00", To: "17:00"})
	if err := s.SetDayLocation(ctx, day, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range fake.Presences("2026-10-06") {
		if !p.IsHomeOffice {
			t.Errorf("presence %s not home", p.From)
		}
	}
}

func TestDaysAndHours(t *testing.T) {
	s, fake := newTestService(t)
	ctx := context.Background()
	mon := Monday(day)
	if mon.Format("2006-01-02") != "2026-10-05" {
		t.Fatalf("Monday = %s", mon)
	}
	fake.AddPresence(api.Presence{Date: "2026-10-05", From: "08:00", To: "16:00"})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "09:00", To: "12:00"})
	fake.AddActivity(api.Activity{Date: "2026-10-05", Seconds: 3600, Project: api.Ref{ID: 1}, Task: api.Ref{ID: 10}})
	fake.AddActivity(api.Activity{Date: "2026-10-05", Seconds: 1800, Project: api.Ref{ID: 1}, Task: api.Ref{ID: 11}})
	fake.AddActivity(api.Activity{Date: "2026-10-06", Seconds: 7200, Project: api.Ref{ID: 2}, Task: api.Ref{ID: 20}})

	days, err := s.Days(ctx, mon, mon.AddDate(0, 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 7 {
		t.Fatalf("%d days", len(days))
	}
	if days[0].PresentSeconds != 8*3600 || days[0].LoggedSeconds != 5400 || len(days[0].Activities) != 2 {
		t.Errorf("monday %+v", days[0])
	}
	if days[1].Gap() != 3600 {
		t.Errorf("tuesday gap %d", days[1].Gap())
	}
	if days[6].Date != "2026-10-11" || len(days[6].Presences) != 0 {
		t.Errorf("sunday %+v", days[6])
	}

	acts, err := s.ActivitiesBetween(ctx, mon, mon.AddDate(0, 0, 6))
	if err != nil {
		t.Fatal(err)
	}
	h := SumHours(acts, time.Now())
	if h.Total != 3600+1800+7200 || h.ByProject[1] != 5400 || h.ByTask[1][11] != 1800 || h.ByTask[2][20] != 7200 {
		t.Errorf("hours %+v", h)
	}
}
