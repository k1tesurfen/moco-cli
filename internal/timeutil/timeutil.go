// Package timeutil has the time math shared by CLI, TUI and daemon.
package timeutil

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// NormalizeClock turns user input like "8", "830", "8:30", "8.30" or "08:30" into "HH:MM".
// MOCO answers malformed times with a 500, so every time is normalized before sending.
func NormalizeClock(s string) (string, error) {
	s = strings.TrimSpace(strings.ReplaceAll(s, ".", ":"))
	var h, m int
	var err error
	switch {
	case strings.Contains(s, ":"):
		parts := strings.SplitN(s, ":", 2)
		if h, err = atoiStrict(parts[0], 1, 2); err == nil {
			m, err = atoiStrict(parts[1], 2, 2)
		}
	case len(s) <= 2:
		h, err = atoiStrict(s, 1, 2)
	case len(s) <= 4:
		if h, err = atoiStrict(s[:len(s)-2], 1, 2); err == nil {
			m, err = atoiStrict(s[len(s)-2:], 2, 2)
		}
	default:
		err = errors.New("too long")
	}
	if err != nil || h > 23 || m > 59 {
		return "", fmt.Errorf("invalid time %q, expected HH:MM", s)
	}
	return fmt.Sprintf("%02d:%02d", h, m), nil
}

func atoiStrict(s string, minLen, maxLen int) (int, error) {
	if len(s) < minLen || len(s) > maxLen {
		return 0, errors.New("bad length")
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.New("not a number")
		}
	}
	return strconv.Atoi(s)
}

// Clock formats t as "HH:MM".
func Clock(t time.Time) string { return t.Format("15:04") }

// ParseDate understands "today", "yesterday", "-N" (days ago), weekday names ("mon",
// "monday": the most recent such day, today included) and YYYY-MM-DD. Empty means today.
func ParseDate(s string, now time.Time) (time.Time, error) {
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	s = strings.ToLower(strings.TrimSpace(s))
	switch s {
	case "", "today", "t":
		return today, nil
	case "yesterday", "y":
		return today.AddDate(0, 0, -1), nil
	}
	if strings.HasPrefix(s, "-") {
		if n, err := strconv.Atoi(s[1:]); err == nil && n >= 0 {
			return today.AddDate(0, 0, -n), nil
		}
	}
	for wd := time.Sunday; wd <= time.Saturday; wd++ {
		name := strings.ToLower(wd.String())
		if s == name || s == name[:3] {
			diff := (int(today.Weekday()) - int(wd) + 7) % 7
			return today.AddDate(0, 0, -diff), nil
		}
	}
	d, err := time.ParseInLocation(DateLayout, s, now.Location())
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid date %q, expected YYYY-MM-DD, today, yesterday, -N or a weekday", s)
	}
	return d, nil
}
