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
