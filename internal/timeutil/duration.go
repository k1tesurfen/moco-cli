package timeutil

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	reHM     = regexp.MustCompile(`^(\d{1,2})h(\d{1,2})?m?$`) // 1h, 1h30, 1h30m
	reM      = regexp.MustCompile(`^(\d{1,4})m(in)?$`)        // 90m, 90min
	reClock  = regexp.MustCompile(`^(\d{1,2}):(\d{2})$`)      // 1:30
	reDecHrs = regexp.MustCompile(`^(\d{1,2})([.,]\d+)?h?$`)  // 1, 1.5, 1,5, 1.5h
)

// ParseDuration parses "1h30", "1h", "90m", "1:30", "1.5", "1,5" into seconds.
// A bare number means hours; more than 24 hours is rejected as a likely typo.
func ParseDuration(s string) (int, error) {
	s = strings.ToLower(strings.ReplaceAll(strings.TrimSpace(s), " ", ""))
	var minutes float64
	switch {
	case reHM.MatchString(s):
		m := reHM.FindStringSubmatch(s)
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2]) // "" → 0
		if mm > 59 {
			return 0, fmt.Errorf("invalid duration %q: minutes must be below 60", s)
		}
		minutes = float64(h*60 + mm)
	case reM.MatchString(s):
		m, _ := strconv.Atoi(reM.FindStringSubmatch(s)[1])
		minutes = float64(m)
	case reClock.MatchString(s):
		m := reClock.FindStringSubmatch(s)
		h, _ := strconv.Atoi(m[1])
		mm, _ := strconv.Atoi(m[2])
		if mm > 59 {
			return 0, fmt.Errorf("invalid duration %q: minutes must be below 60", s)
		}
		minutes = float64(h*60 + mm)
	case reDecHrs.MatchString(s):
		h, _ := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSuffix(s, "h"), ",", "."), 64)
		minutes = h * 60
	default:
		return 0, fmt.Errorf("invalid duration %q, use e.g. 1h30, 90m, 1:30 or 1.5", s)
	}
	if minutes <= 0 {
		return 0, fmt.Errorf("duration %q must be positive", s)
	}
	if minutes > 24*60 {
		return 0, fmt.Errorf("duration %q is more than 24 hours — did you mean %sm?", s, s)
	}
	return int(minutes*60 + 0.5), nil
}

// RoundUp rounds seconds up to the next multiple of step minutes.
func RoundUp(sec, stepMinutes int) int {
	step := stepMinutes * 60
	if step <= 0 || sec <= 0 {
		return sec
	}
	return (sec + step - 1) / step * step
}
