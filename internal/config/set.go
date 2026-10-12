package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// Keys are the settings `moco config get/set` knows, in display order. The subdomain is set by
// `moco login`, aliases by `moco alias`.
var Keys = []string{
	"rounding_minutes",
	"schedule.workdays", "schedule.start", "schedule.start_reask", "schedule.morning_log",
	"schedule.break_from", "schedule.break_to", "schedule.break_check", "schedule.afternoon_log",
	"schedule.end", "schedule.end_reask_every", "schedule.end_reask_until", "schedule.snooze",
	"location.default", "location.mon", "location.tue", "location.wed", "location.thu",
	"location.fri", "location.sat", "location.sun",
}

var clockKeys = map[string]bool{
	"schedule.start": true, "schedule.start_reask": true, "schedule.morning_log": true,
	"schedule.break_from": true, "schedule.break_to": true, "schedule.break_check": true,
	"schedule.afternoon_log": true, "schedule.end": true, "schedule.end_reask_until": true,
}

// Values returns every key of Keys with its effective value (defaults included).
func (c Config) Values() map[string]string {
	s, l := c.Schedule, c.Location
	return map[string]string{
		"rounding_minutes":         strconv.Itoa(c.RoundingMinutes),
		"schedule.workdays":        strings.Join(s.Workdays, ","),
		"schedule.start":           s.Start,
		"schedule.start_reask":     s.StartReask,
		"schedule.morning_log":     s.MorningLog,
		"schedule.break_from":      s.BreakFrom,
		"schedule.break_to":        s.BreakTo,
		"schedule.break_check":     s.BreakCheck,
		"schedule.afternoon_log":   s.AfternoonLog,
		"schedule.end":             s.End,
		"schedule.end_reask_every": s.EndReaskEvery,
		"schedule.end_reask_until": s.EndReaskUntil,
		"schedule.snooze":          s.Snooze,
		"location.default":         l.Default,
		"location.mon":             l.Mon,
		"location.tue":             l.Tue,
		"location.wed":             l.Wed,
		"location.thu":             l.Thu,
		"location.fri":             l.Fri,
		"location.sat":             l.Sat,
		"location.sun":             l.Sun,
	}
}

// Set writes one key into the config file, keeping comments and the rest of the file. The result
// is validated before it replaces the file. An empty value for a weekday location removes the
// line (the default applies again).
func Set(key, value string) error {
	known := false
	for _, k := range Keys {
		known = known || k == key
	}
	if !known {
		return fmt.Errorf("unknown key %q — see `moco config` for the list", key)
	}
	value = strings.TrimSpace(value)
	var literal string
	switch {
	case key == "rounding_minutes":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("rounding_minutes must be a number of minutes")
		}
		literal = strconv.Itoa(n)
	case key == "schedule.workdays":
		var days []string
		for _, d := range strings.FieldsFunc(strings.ToLower(value), func(r rune) bool { return r == ',' || r == ' ' }) {
			days = append(days, strconv.Quote(d))
		}
		literal = "[" + strings.Join(days, ", ") + "]"
	case clockKeys[key]:
		t, err := timeutil.NormalizeClock(value)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		literal = strconv.Quote(t)
	case strings.HasPrefix(key, "location.") && key != "location.default" && value == "":
		literal = "" // remove
	default:
		literal = strconv.Quote(value)
	}

	section, name := "", key
	if i := strings.IndexByte(key, '.'); i >= 0 {
		section, name = key[:i], key[i+1:]
	}
	path := Path()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("no config file yet — run `moco login` first")
	}
	if err != nil {
		return err
	}
	line := ""
	if literal != "" {
		line = name + " = " + literal
	}
	return writeConfig(path, setLine(strings.Split(strings.TrimRight(string(data), "\n"), "\n"), section, name, line))
}

// setLine replaces (or removes, if line is empty) name in section ("" = top level), or adds it
// at the end of the section, creating the section if needed. A commented-out "# name = …" line
// in the section is replaced too.
func setLine(lines []string, section, name, line string) []string {
	keyRe := regexp.MustCompile(`^\s*(#\s*)?` + regexp.QuoteMeta(name) + `\s*=`)
	start, end := -1, len(lines)
	if section == "" {
		start = -1 // top level: from the first line to the first header
		for i, l := range lines {
			if strings.HasPrefix(strings.TrimSpace(l), "[") {
				end = i
				break
			}
		}
	} else {
		found := false
		for i, l := range lines {
			t := strings.TrimSpace(l)
			if !found && (t == "["+section+"]" || strings.HasPrefix(t, "["+section+"]")) {
				start, found = i, true
				continue
			}
			if found && strings.HasPrefix(t, "[") {
				end = i
				break
			}
		}
		if !found {
			if line == "" {
				return lines
			}
			return append(lines, "", "["+section+"]", line)
		}
	}

	active := regexp.MustCompile(`^\s*` + regexp.QuoteMeta(name) + `\s*=`)
	target := -1
	for i := start + 1; i < end; i++ {
		if active.MatchString(lines[i]) {
			target = i
			break
		}
	}
	if target < 0 && line != "" {
		for i := start + 1; i < end; i++ {
			if keyRe.MatchString(lines[i]) { // a commented-out example
				target = i
				break
			}
		}
	}
	out := append([]string{}, lines...)
	switch {
	case target >= 0 && line == "":
		return append(out[:target], out[target+1:]...)
	case target >= 0:
		out[target] = line + trailingComment(lines[target])
		return out
	case line == "":
		return out
	}
	pos := end // after the last non-blank line of the section
	for pos > start+1 && strings.TrimSpace(out[pos-1]) == "" {
		pos--
	}
	return append(out[:pos], append([]string{line}, out[pos:]...)...)
}

// trailingComment returns the "   # …" part of an uncommented key line, so it survives an update.
func trailingComment(l string) string {
	if strings.HasPrefix(strings.TrimSpace(l), "#") {
		return ""
	}
	inString := false
	for i, r := range l {
		switch {
		case r == '"':
			inString = !inString
		case r == '#' && !inString:
			j := i
			for j > 0 && (l[j-1] == ' ' || l[j-1] == '\t') {
				j--
			}
			return l[j:]
		}
	}
	return ""
}
