package service

import (
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

func TestBuildDayToday(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 10, 7, 15, 30, 0, 0, loc)
	timerStart := now.Add(-20 * time.Minute)
	d, err := buildDay("2026-10-07",
		[]api.Presence{
			{ID: 2, From: "14:00"},
			{ID: 1, From: "08:00", To: "13:00"},
		},
		[]api.Activity{
			{ID: 10, Seconds: 4 * 3600},
			{ID: 11, Seconds: 600, TimerStartedAt: &timerStart},
		}, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.Presences[0].ID != 1 {
		t.Error("presences not sorted by from")
	}
	if want := 5*3600 + 90*60; d.PresentSeconds != want {
		t.Errorf("present = %d, want %d", d.PresentSeconds, want)
	}
	if want := 4*3600 + 600 + 20*60; d.LoggedSeconds != want {
		t.Errorf("logged = %d, want %d", d.LoggedSeconds, want)
	}
	if d.OpenPresence == nil || d.OpenPresence.ID != 2 {
		t.Errorf("open presence = %+v", d.OpenPresence)
	}
	if d.RunningTimer == nil || d.RunningTimer.ID != 11 {
		t.Errorf("running timer = %+v", d.RunningTimer)
	}
	if d.Gap() != d.PresentSeconds-d.LoggedSeconds {
		t.Error("gap")
	}
}

func TestBuildDayPastOpenPresenceNotCounted(t *testing.T) {
	now := time.Date(2026, 10, 7, 9, 0, 0, 0, time.UTC)
	d, err := buildDay("2026-10-06", []api.Presence{{ID: 1, From: "08:00"}}, nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if d.PresentSeconds != 0 || d.OpenPresence == nil {
		t.Errorf("present = %d, open = %v", d.PresentSeconds, d.OpenPresence)
	}
}
