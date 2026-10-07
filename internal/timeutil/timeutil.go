// Package timeutil has the time math shared by CLI, TUI and daemon.
package timeutil

import (
	"fmt"
	"time"
)

// DateLayout is MOCO's date format.
const DateLayout = "2006-01-02"

// FormatSeconds renders a duration as "2h05"; negative values get a leading "-".
func FormatSeconds(sec int) string {
	sign := ""
	if sec < 0 {
		sign = "-"
		sec = -sec
	}
	m := sec / 60
	return fmt.Sprintf("%s%dh%02d", sign, m/60, m%60)
}

// ParseClock parses "HH:MM" on the given day in that day's location.
func ParseClock(day time.Time, hhmm string) (time.Time, error) {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid time %q, expected HH:MM", hhmm)
	}
	y, mo, d := day.Date()
	return time.Date(y, mo, d, t.Hour(), t.Minute(), 0, 0, day.Location()), nil
}

// Date formats t as YYYY-MM-DD.
func Date(t time.Time) string { return t.Format(DateLayout) }
