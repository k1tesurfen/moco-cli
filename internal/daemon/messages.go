package daemon

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// Notification ids: "<date>.<event>" for real reminders (a re-ask replaces the earlier one),
// "test.<event>.<unix>" for `moco daemon test`, and "….result" for the outcome of an answer.

func noteID(date string, ev Event) string { return date + "." + string(ev) }

func testID(ev Event, now time.Time) string {
	return "test." + string(ev) + "." + strconv.FormatInt(now.Unix(), 10)
}

// parseID returns the date and event of a reminder id; test reports a dry-run id.
func parseID(id string) (date string, ev Event, test, ok bool) {
	parts := strings.Split(id, ".")
	switch {
	case len(parts) == 3 && parts[0] == "test":
		return "", Event(parts[1]), true, valid(Event(parts[1]))
	case len(parts) == 2:
		return parts[0], Event(parts[1]), false, valid(Event(parts[1]))
	}
	return "", "", false, false
}

func valid(ev Event) bool {
	for _, e := range Events {
		if e == ev {
			return true
		}
	}
	return false
}

// Action ids.
const (
	actStartDefault = "start_default"
	actStartNow     = "start_now"
	actStartAlt     = "start_alt"
	actStartTime    = "start_time"
	actNotYet       = "not_yet"
	actDayOff       = "day_off"
	actCopy         = "copy"
	actSnooze       = "snooze"
	actDone         = "done"
	actBreakDefault = "break_default"
	actBreakOther   = "break_other"
	actEndDefault   = "end_default"
	actEndNow       = "end_now"
	actEndTimer     = "end_timer"
	actStillWorking = "still_working"
)

func where(home bool) string {
	if home {
		return "home"
	}
	return "office"
}

// message builds the notification for ev. day may be nil when MOCO is unreachable.
// macOS shows all actions in one "Options" menu, so the most likely answer comes first.
func message(ev Event, cfg config.Config, date time.Time, day *service.Day, id string) notify.Notification {
	s := cfg.Schedule
	n := notify.Notification{ID: id, Category: string(ev), Sound: true}
	timerNote := ""
	if day != nil && day.RunningTimer != nil {
		timerNote = fmt.Sprintf(" Timer running on %s / %s.", day.RunningTimer.Project.Name, day.RunningTimer.Task.Name)
	}
	switch ev {
	case Start:
		home := cfg.HomeOfficeOn(date)
		n.Title = "Did you start working?"
		n.Body = fmt.Sprintf("Answer via Options · default today: %s", where(home))
		if day == nil {
			n.Body += " · MOCO not reachable, the answer will be queued"
		}
		n.Actions = []notify.Action{
			{ID: actStartDefault, Title: fmt.Sprintf("Yes, %s · %s", s.Start, where(home))},
			{ID: actStartNow, Title: fmt.Sprintf("Yes, now · %s", where(home))},
			{ID: actStartAlt, Title: fmt.Sprintf("Yes, %s · %s", s.Start, where(!home))},
			{ID: actStartTime, Title: "Other time…", Input: true, Placeholder: "HH:MM, optionally + home/office", Button: "Start"},
			{ID: actNotYet, Title: "Not yet"},
			{ID: actDayOff, Title: "Day off"},
		}
	case Morning, Afternoon:
		label := "Morning"
		if ev == Afternoon {
			label = "Today"
		}
		present, logged, gap := 0, 0, 0
		if day != nil {
			present, logged, gap = day.PresentSeconds, day.LoggedSeconds, day.Gap()
		}
		n.Title = fmt.Sprintf("%s: %s present, %s logged", label, timeutil.FormatSeconds(present), timeutil.FormatSeconds(logged))
		n.Body = fmt.Sprintf("%s not logged yet — copy `moco log` and run it in your terminal.%s", timeutil.FormatSeconds(gap), timerNote)
		n.Actions = []notify.Action{
			{ID: actCopy, Title: "Copy `moco log`"},
			{ID: actSnooze, Title: "Snooze " + s.Snooze},
			{ID: actDone, Title: "Done"},
		}
	case Break:
		n.Title = fmt.Sprintf("Did you take your break %s–%s?", s.BreakFrom, s.BreakTo)
		n.Body = "Splits today's working time at the break." + timerNote
		n.Actions = []notify.Action{
			{ID: actBreakDefault, Title: fmt.Sprintf("Yes, %s–%s", s.BreakFrom, s.BreakTo)},
			{ID: actBreakOther, Title: "Different…", Input: true, Placeholder: "12:30-13:15", Button: "Save"},
			{ID: actNotYet, Title: "No break yet"},
		}
	case End:
		n.Title = "Finished for today?"
		if day != nil {
			since := ""
			if len(day.Presences) > 0 {
				since = "Working since " + day.Presences[0].From + " · "
			}
			n.Body = fmt.Sprintf("%s%s present, %s logged.%s", since,
				timeutil.FormatSeconds(day.PresentSeconds), timeutil.FormatSeconds(day.LoggedSeconds), timerNote)
		}
		n.Actions = []notify.Action{
			{ID: actEndDefault, Title: "Yes, " + s.End},
			{ID: actEndNow, Title: "Yes, now"},
		}
		if timerNote != "" {
			n.Actions = append(n.Actions, notify.Action{ID: actEndTimer, Title: "Yes, now + stop timer…", Input: true,
				Placeholder: "What did you do? (timer description)", Button: "Stop"})
		}
		n.Actions = append(n.Actions, notify.Action{ID: actStillWorking, Title: "Still working"})
	}
	return n
}

// result is a plain notification with the outcome of an answer.
func result(id, title, body string) notify.Notification {
	return notify.Notification{ID: id + ".result", Category: "result", Title: title, Body: body}
}
