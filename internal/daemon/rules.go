// Package daemon runs the reminders: it decides when to ask what (rules.go, pure and tested with
// a fake clock), shows the questions via MocoNotifier and carries out the answers through the
// same service code as the CLI.
package daemon

import (
	"sort"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// Event is one reminder of the day.
type Event string

const (
	Start     Event = "start"
	Morning   Event = "morning_log"
	Break     Event = "break"
	Afternoon Event = "afternoon_log"
	End       Event = "end"
)

// Events lists the reminders in the order of the day.
var Events = []Event{Start, Morning, Break, Afternoon, End}

// at returns the scheduled time of a "HH:MM" on date.
func at(date time.Time, hhmm string) time.Time {
	t, err := timeutil.ParseClock(date, hhmm)
	if err != nil {
		return date // config.Validate rejects invalid times; never reached
	}
	return t
}

func scheduled(cfg config.Config, ev Event) string {
	s := cfg.Schedule
	return map[Event]string{Start: s.Start, Morning: s.MorningLog, Break: s.BreakCheck, Afternoon: s.AfternoonLog, End: s.End}[ev]
}

func duration(s string) time.Duration {
	d, _ := time.ParseDuration(s)
	return d
}

// NewDay creates the reminder state for date. Days off and non-workdays get no reminders.
func NewDay(cfg config.Config, date time.Time, paused bool) *store.DaemonDay {
	d := &store.DaemonDay{Date: timeutil.Date(date), Events: map[string]*store.EventState{}}
	off := paused || !cfg.IsWorkday(date)
	for _, ev := range Events {
		d.Events[string(ev)] = &store.EventState{Next: at(date, scheduled(cfg, ev)), Done: off}
	}
	return d
}

// Relevant reports whether ev still needs the user. known is false when MOCO's state is unknown
// (day == nil, MOCO unreachable): then only the start question is asked — its answer can be
// queued — and everything else waits.
func Relevant(ev Event, cfg config.Config, day *service.Day) (relevant, known bool) {
	if day == nil {
		return ev == Start, ev == Start
	}
	switch ev {
	case Start:
		return len(day.Presences) == 0, true
	case Morning, Afternoon:
		return len(day.Presences) > 0 && day.Gap() >= cfg.RoundingMinutes*60, true
	case Break:
		// Not split yet: one open presence that began before the break.
		return len(day.Presences) == 1 && day.OpenPresence != nil && day.OpenPresence.From < cfg.Schedule.BreakFrom, true
	case End:
		return day.OpenPresence != nil, true
	}
	return false, true
}

// Pick chooses the reminder to show now: of all due reminders, the latest that is still
// relevant. Due reminders that are no longer relevant are closed; older relevant ones are
// dropped as missed, so after a sleep only one notification appears, never a burst.
func Pick(d *store.DaemonDay, cfg config.Config, now time.Time, day *service.Day) (Event, bool) {
	var due []Event
	for _, ev := range Events {
		if st := d.Events[string(ev)]; !st.Done && !st.Next.After(now) {
			due = append(due, ev)
		}
	}
	sort.SliceStable(due, func(i, j int) bool { return d.Events[string(due[i])].Next.After(d.Events[string(due[j])].Next) })
	var pick Event
	for _, ev := range due {
		relevant, known := Relevant(ev, cfg, day)
		st := d.Events[string(ev)]
		switch {
		case !known:
			// wait until MOCO can be asked
		case !relevant:
			st.Done, st.Skipped = true, true
		case pick == "":
			pick = ev
		default:
			st.Done = true // missed
		}
	}
	return pick, pick != ""
}

// SkipReason explains why ev is not needed, for the log.
func SkipReason(ev Event, day *service.Day) string {
	if day == nil {
		return ""
	}
	var spans []string
	for _, p := range day.Presences {
		to := p.To
		if to == "" {
			to = "…"
		}
		spans = append(spans, p.From+"–"+to)
	}
	switch ev {
	case Start:
		return "presence already recorded (" + strings.Join(spans, ", ") + ")"
	case Morning, Afternoon:
		if len(day.Presences) == 0 {
			return "no presence today"
		}
		return "nothing (or less than one rounding step) left to log"
	case Break:
		return "no single open presence to split (" + strings.Join(spans, ", ") + ")"
	case End:
		return "no open presence"
	}
	return ""
}

// Revive brings back a start question that was skipped because the day already had a presence,
// once that presence is gone (e.g. a test entry deleted) — until the end of the working day.
func Revive(d *store.DaemonDay, cfg config.Config, now time.Time, day *service.Day) bool {
	st := d.Events[string(Start)]
	if st == nil || !st.Skipped || day == nil || !now.Before(at(now, cfg.Schedule.End)) {
		return false
	}
	if relevant, _ := Relevant(Start, cfg, day); !relevant {
		return false
	}
	st.Done, st.Skipped, st.Next = false, false, now
	return true
}

// Shown updates the state after ev was shown: when (and whether) it is asked again.
func Shown(d *store.DaemonDay, cfg config.Config, ev Event, now time.Time) {
	st := d.Events[string(ev)]
	st.Fired++
	st.Shown = true
	date := now
	switch ev {
	case Start:
		if reask := at(date, cfg.Schedule.StartReask); st.Fired == 1 && reask.After(now) {
			st.Next = reask // no reaction counts as "not yet"
		} else {
			st.Done = true
		}
	case End:
		st.Next = now.Add(duration(cfg.Schedule.EndReaskEvery))
		st.Done = st.Next.After(at(date, cfg.Schedule.EndReaskUntil))
	default:
		st.Done = true
	}
}

// Answered closes ev: the user answered (yes, done, day off …).
func Answered(d *store.DaemonDay, ev Event) {
	st := d.Events[string(ev)]
	st.Done, st.Shown = true, false
}

// Snooze asks ev again after the configured snooze time.
func Snooze(d *store.DaemonDay, cfg config.Config, ev Event, now time.Time) {
	st := d.Events[string(ev)]
	st.Done, st.Shown = false, false
	st.Next = now.Add(duration(cfg.Schedule.Snooze))
}

// NotYet handles "Not yet" (start) and "No break yet" (break): ask once more, then stop.
func NotYet(d *store.DaemonDay, cfg config.Config, ev Event, now time.Time) {
	st := d.Events[string(ev)]
	st.Shown = false
	if st.Fired >= 2 {
		st.Done = true
		return
	}
	next := now.Add(duration(cfg.Schedule.Snooze))
	if reask := at(now, cfg.Schedule.StartReask); ev == Start && reask.After(now) {
		next = reask
	}
	st.Done, st.Next = false, next
}

// StillWorking handles "Still working" on the end question: ask again later, until the limit.
func StillWorking(d *store.DaemonDay, cfg config.Config, now time.Time) {
	st := d.Events[string(End)]
	st.Shown = false
	st.Next = now.Add(duration(cfg.Schedule.EndReaskEvery))
	st.Done = st.Next.After(at(now, cfg.Schedule.EndReaskUntil))
}

// DayOff closes every reminder of the day.
func DayOff(d *store.DaemonDay) {
	for _, st := range d.Events {
		st.Done, st.Shown, st.Skipped = true, false, false // a day off is never revived
	}
}
