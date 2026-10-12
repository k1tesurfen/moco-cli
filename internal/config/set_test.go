package config

import (
	"strings"
	"testing"
)

func TestSetLine(t *testing.T) {
	file := strings.Split(strings.TrimRight(`subdomain = "x"
rounding_minutes = 15

[schedule]
start = "08:00"            # "Did you start working?"
end = "17:00"

[location]
default = "office"
# mon = "home"
# fri = "home"
`, "\n"), "\n")
	cases := []struct {
		section, name, line string
		want                []string // lines that must be present
		gone                []string // lines that must be absent
	}{
		{"", "rounding_minutes", "rounding_minutes = 30", []string{"rounding_minutes = 30"}, []string{"rounding_minutes = 15"}},
		{"schedule", "start", `start = "07:30"`, []string{`start = "07:30"            # "Did you start working?"`}, []string{`start = "08:00"`}},
		{"schedule", "snooze", `snooze = "15m"`, []string{`snooze = "15m"`}, nil},
		{"location", "mon", `mon = "office"`, []string{`mon = "office"`, `# fri = "home"`}, []string{`# mon = "home"`}},
		{"location", "fri", "", []string{`# fri = "home"`}, nil}, // removing a commented line keeps it
		{"aliases", "x", `x = { project = "p", task = "t" }`, []string{"[aliases]", `x = { project = "p", task = "t" }`}, nil},
	}
	for _, c := range cases {
		got := strings.Join(setLine(file, c.section, c.name, c.line), "\n")
		for _, w := range c.want {
			if !strings.Contains(got, w) {
				t.Errorf("%s.%s: missing %q in\n%s", c.section, c.name, w, got)
			}
		}
		for _, g := range c.gone {
			if strings.Contains(got, g) {
				t.Errorf("%s.%s: still has %q in\n%s", c.section, c.name, g, got)
			}
		}
	}
	// snooze goes into [schedule], before [location]
	got := setLine(file, "schedule", "snooze", `snooze = "15m"`)
	if strings.Join(got, "\n") == "" || indexOf(got, `snooze = "15m"`) > indexOf(got, "[location]") {
		t.Errorf("snooze not in [schedule]:\n%s", strings.Join(got, "\n"))
	}
	// removing an active weekday line
	got = setLine(setLine(file, "location", "mon", `mon = "home"`), "location", "mon", "")
	if indexOf(got, `mon = "home"`) >= 0 {
		t.Error("mon not removed")
	}
}

func indexOf(lines []string, s string) int {
	for i, l := range lines {
		if l == s {
			return i
		}
	}
	return -1
}
