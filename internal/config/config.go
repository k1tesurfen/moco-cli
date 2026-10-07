// Package config loads and saves ~/.config/moco/config.toml.
package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the non-secret user configuration. The API token lives in the Keychain.
type Config struct {
	Subdomain       string           `toml:"subdomain"`
	RoundingMinutes int              `toml:"rounding_minutes"`
	Rounding        string           `toml:"rounding"` // "up" (only mode for now)
	Schedule        Schedule         `toml:"schedule"`
	Location        Location         `toml:"location"`
	Aliases         map[string]Alias `toml:"aliases"`
}

// Schedule holds the reminder times ("HH:MM") and durations ("30m").
type Schedule struct {
	Workdays      []string `toml:"workdays"`
	Start         string   `toml:"start"`
	StartReask    string   `toml:"start_reask"`
	MorningLog    string   `toml:"morning_log"`
	BreakFrom     string   `toml:"break_from"`
	BreakTo       string   `toml:"break_to"`
	BreakCheck    string   `toml:"break_check"`
	AfternoonLog  string   `toml:"afternoon_log"`
	End           string   `toml:"end"`
	EndReaskEvery string   `toml:"end_reask_every"`
	EndReaskUntil string   `toml:"end_reask_until"`
	Snooze        string   `toml:"snooze"`
}

// Location is the default work location ("home" or "office"), optionally per weekday.
type Location struct {
	Default string `toml:"default"`
	Mon     string `toml:"mon,omitempty"`
	Tue     string `toml:"tue,omitempty"`
	Wed     string `toml:"wed,omitempty"`
	Thu     string `toml:"thu,omitempty"`
	Fri     string `toml:"fri,omitempty"`
	Sat     string `toml:"sat,omitempty"`
	Sun     string `toml:"sun,omitempty"`
}

// Alias maps a short name to a project and task (by name).
type Alias struct {
	Project string `toml:"project"`
	Task    string `toml:"task"`
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		RoundingMinutes: 15,
		Rounding:        "up",
		Schedule: Schedule{
			Workdays:      []string{"mon", "tue", "wed", "thu", "fri"},
			Start:         "08:00",
			StartReask:    "09:00",
			MorningLog:    "12:30",
			BreakFrom:     "13:00",
			BreakTo:       "14:00",
			BreakCheck:    "14:00",
			AfternoonLog:  "16:30",
			End:           "17:00",
			EndReaskEvery: "30m",
			EndReaskUntil: "20:00",
			Snooze:        "30m",
		},
		Location: Location{Default: "office"},
		Aliases:  map[string]Alias{},
	}
}

// Dir returns the config directory ($XDG_CONFIG_HOME/moco or ~/.config/moco).
func Dir() string {
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "moco")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "moco")
}

// Path returns the config file path.
func Path() string { return filepath.Join(Dir(), "config.toml") }

// Load reads the config file on top of the defaults. A missing file is not an error.
func Load() (Config, error) { return LoadFile(Path()) }

// LoadFile reads the given config file on top of the defaults.
func LoadFile(path string) (Config, error) {
	cfg := Default()
	_, err := toml.DecodeFile(path, &cfg)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read %s: %w", path, err)
	}
	if cfg.Aliases == nil {
		cfg.Aliases = map[string]Alias{}
	}
	return cfg, cfg.Validate()
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// Validate checks times, durations and enum values.
func (c Config) Validate() error {
	var errs []string
	s := c.Schedule
	for name, v := range map[string]string{
		"start": s.Start, "start_reask": s.StartReask, "morning_log": s.MorningLog,
		"break_from": s.BreakFrom, "break_to": s.BreakTo, "break_check": s.BreakCheck,
		"afternoon_log": s.AfternoonLog, "end": s.End, "end_reask_until": s.EndReaskUntil,
	} {
		if !hhmm.MatchString(v) {
			errs = append(errs, fmt.Sprintf("schedule.%s = %q is not HH:MM", name, v))
		}
	}
	for name, v := range map[string]string{"end_reask_every": s.EndReaskEvery, "snooze": s.Snooze} {
		if d, err := time.ParseDuration(v); err != nil || d <= 0 {
			errs = append(errs, fmt.Sprintf("schedule.%s = %q is not a duration like 30m", name, v))
		}
	}
	for _, d := range s.Workdays {
		if _, ok := weekdays[d]; !ok {
			errs = append(errs, fmt.Sprintf("schedule.workdays: unknown day %q", d))
		}
	}
	l := c.Location
	for name, v := range map[string]string{"default": l.Default, "mon": l.Mon, "tue": l.Tue, "wed": l.Wed, "thu": l.Thu, "fri": l.Fri, "sat": l.Sat, "sun": l.Sun} {
		if v != "" && v != "home" && v != "office" {
			errs = append(errs, fmt.Sprintf("location.%s = %q must be home or office", name, v))
		}
	}
	if c.RoundingMinutes < 1 || c.RoundingMinutes > 60 {
		errs = append(errs, fmt.Sprintf("rounding_minutes = %d must be 1–60", c.RoundingMinutes))
	}
	if c.Rounding != "up" {
		errs = append(errs, fmt.Sprintf("rounding = %q: only \"up\" is supported", c.Rounding))
	}
	if len(errs) > 0 {
		sort.Strings(errs)
		return fmt.Errorf("invalid config %s:\n  %s", Path(), strings.Join(errs, "\n  "))
	}
	return nil
}

var weekdays = map[string]time.Weekday{
	"sun": time.Sunday, "mon": time.Monday, "tue": time.Tuesday, "wed": time.Wednesday,
	"thu": time.Thursday, "fri": time.Friday, "sat": time.Saturday,
}

// IsWorkday reports whether the date falls on a configured workday.
func (c Config) IsWorkday(t time.Time) bool {
	for _, d := range c.Schedule.Workdays {
		if weekdays[d] == t.Weekday() {
			return true
		}
	}
	return false
}

// HomeOfficeOn reports whether the default location for that day is home.
func (c Config) HomeOfficeOn(t time.Time) bool {
	l := c.Location
	loc := map[time.Weekday]string{
		time.Monday: l.Mon, time.Tuesday: l.Tue, time.Wednesday: l.Wed, time.Thursday: l.Thu,
		time.Friday: l.Fri, time.Saturday: l.Sat, time.Sunday: l.Sun,
	}[t.Weekday()]
	if loc == "" {
		loc = l.Default
	}
	return loc == "home"
}

// SetSubdomain writes the subdomain into the config file, creating it from the commented
// template if it does not exist yet. Other content of an existing file is kept as is.
func SetSubdomain(sub string) error {
	path := Path()
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return err
		}
		return os.WriteFile(path, []byte(fmt.Sprintf(template, sub)), 0o644)
	}
	if err != nil {
		return err
	}
	line := fmt.Sprintf("subdomain = %q", sub)
	re := regexp.MustCompile(`(?m)^subdomain\s*=.*$`)
	if re.Match(data) {
		data = re.ReplaceAll(data, []byte(line))
	} else {
		data = append([]byte(line+"\n"), data...)
	}
	return os.WriteFile(path, data, 0o644)
}

const template = `# moco-cli configuration. The API token is stored in the macOS Keychain, not here.
subdomain = %q

# Durations are always rounded up to this many minutes.
rounding_minutes = 15
rounding = "up"

[schedule]
workdays = ["mon", "tue", "wed", "thu", "fri"]
start = "08:00"            # "Did you start working?"
start_reask = "09:00"
morning_log = "12:30"      # "Morning: … missing"
break_from = "13:00"
break_to = "14:00"
break_check = "14:00"      # "Did you take your break?"
afternoon_log = "16:30"
end = "17:00"              # "Finished for today?"
end_reask_every = "30m"
end_reask_until = "20:00"
snooze = "30m"

[location]                 # default for the start prompt: "home" or "office"
default = "office"
# mon = "home"
# fri = "home"

[aliases]
# review = { project = "ACME Website", task = "Project management" }
`
