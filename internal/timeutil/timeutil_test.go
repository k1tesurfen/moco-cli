package timeutil

import (
	"testing"
	"time"
)

func TestFormatSeconds(t *testing.T) {
	for sec, want := range map[int]string{
		0: "0h00", 59: "0h00", 900: "0h15", 7200: "2h00", 16200: "4h30", -9000: "-2h30",
	} {
		if got := FormatSeconds(sec); got != want {
			t.Errorf("FormatSeconds(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestParseClock(t *testing.T) {
	loc, _ := time.LoadLocation("Europe/Berlin")
	day := time.Date(2026, 10, 7, 15, 0, 0, 0, loc)
	got, err := ParseClock(day, "08:30")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2026, 10, 7, 8, 30, 0, 0, loc); !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
	if _, err := ParseClock(day, "8.30"); err == nil {
		t.Error("expected error for 8.30")
	}
}

func TestNormalizeClock(t *testing.T) {
	ok := map[string]string{
		"8": "08:00", "08": "08:00", "830": "08:30", "0830": "08:30", "8:30": "08:30",
		"8.30": "08:30", "17:07": "17:07", " 9:05 ": "09:05", "23:59": "23:59", "0": "00:00",
	}
	for in, want := range ok {
		if got, err := NormalizeClock(in); err != nil || got != want {
			t.Errorf("NormalizeClock(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "24", "8:60", "8:3", "abc", "12345", "-1", "8:30:00", "1h"} {
		if got, err := NormalizeClock(in); err == nil {
			t.Errorf("NormalizeClock(%q) = %q, want error", in, got)
		}
	}
}

func TestParseDate(t *testing.T) {
	now := time.Date(2026, 10, 7, 15, 0, 0, 0, time.UTC) // Wednesday
	for in, want := range map[string]string{
		"": "2026-10-07", "today": "2026-10-07", "yesterday": "2026-10-06", "y": "2026-10-06",
		"-2": "2026-10-05", "wed": "2026-10-07", "mon": "2026-10-05", "Thursday": "2026-10-01",
		"2026-09-30": "2026-09-30",
	} {
		got, err := ParseDate(in, now)
		if err != nil || Date(got) != want {
			t.Errorf("ParseDate(%q) = %s, %v; want %s", in, Date(got), err, want)
		}
	}
	if _, err := ParseDate("tomorrowish", now); err == nil {
		t.Error("expected error")
	}
}
