package timeutil

import "testing"

func TestParseDuration(t *testing.T) {
	ok := map[string]int{
		"1h30": 5400, "1h30m": 5400, "1h": 3600, "2h05": 7500, "90m": 5400, "90min": 5400,
		"1:30": 5400, "0:07": 420, "1.5": 5400, "1,5": 5400, "1.5h": 5400, "2": 7200,
		"0.25": 900, " 45m ": 2700, "1H30": 5400,
	}
	for in, want := range ok {
		if got, err := ParseDuration(in); err != nil || got != want {
			t.Errorf("ParseDuration(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "0", "0m", "abc", "1h60", "1:75", "45", "1.5.5", "-1", "h30"} {
		if got, err := ParseDuration(in); err == nil {
			t.Errorf("ParseDuration(%q) = %d, want error", in, got)
		}
	}
}

func TestRoundUp(t *testing.T) {
	for _, c := range []struct{ in, step, want int }{
		{0, 15, 0}, {1, 15, 900}, {900, 15, 900}, {901, 15, 1800}, {67 * 60, 15, 75 * 60}, {7 * 60, 5, 10 * 60},
	} {
		if got := RoundUp(c.in, c.step); got != c.want {
			t.Errorf("RoundUp(%d, %d) = %d, want %d", c.in, c.step, got, c.want)
		}
	}
}
