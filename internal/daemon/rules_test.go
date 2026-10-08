package daemon

import (
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/service"
)

var (
	cfg  = config.Default()
	thu  = time.Date(2026, 10, 8, 0, 0, 0, 0, time.Local) // Thursday
	clk  = func(hhmm string) time.Time { return at(thu, hhmm) }
	none = &service.Day{Date: "2026-10-08"}
)

func dayWith(presences []api.Presence, present, logged int) *service.Day {
	d := &service.Day{Date: "2026-10-08", Presences: presences, PresentSeconds: present, LoggedSeconds: logged}
	for i := range d.Presences {
		if d.Presences[i].To == "" {
			d.OpenPresence = &d.Presences[i]
		}
	}
	return d
}

var (
	open8     = dayWith([]api.Presence{{From: "08:00"}}, 4*3600+30*60, 2*3600) // 12:30, 2h30 missing
	split     = dayWith([]api.Presence{{From: "08:00", To: "13:00"}, {From: "14:00"}}, 7*3600, 7*3600)
	finished  = dayWith([]api.Presence{{From: "08:00", To: "17:00"}}, 9*3600, 9*3600)
	open8Full = dayWith([]api.Presence{{From: "08:00"}}, 4*3600, 4*3600)
)

func TestNormalDay(t *testing.T) {
	d := NewDay(cfg, thu, false)
	if ev, ok := Pick(d, cfg, clk("07:59"), none); ok {
		t.Fatalf("nothing due before 08:00, got %s", ev)
	}
	if ev, _ := Pick(d, cfg, clk("08:00"), none); ev != Start {
		t.Fatalf("08:00: %s", ev)
	}
	Shown(d, cfg, Start, clk("08:00"))
	// No reaction → asked again at 09:00, once.
	if ev, ok := Pick(d, cfg, clk("08:59"), none); ok {
		t.Fatalf("08:59: %s", ev)
	}
	if ev, _ := Pick(d, cfg, clk("09:00"), none); ev != Start {
		t.Fatalf("09:00 re-ask: %s", ev)
	}
	Shown(d, cfg, Start, clk("09:00"))
	if !d.Events["start"].Done {
		t.Error("start must be done after the re-ask")
	}

	if ev, _ := Pick(d, cfg, clk("12:30"), open8); ev != Morning {
		t.Fatalf("12:30: %s", ev)
	}
	Shown(d, cfg, Morning, clk("12:30"))
	if ev, _ := Pick(d, cfg, clk("14:00"), open8); ev != Break {
		t.Fatalf("14:00: %s", ev)
	}
	Answered(d, Break)
	// Afternoon: everything logged → skipped silently.
	if ev, ok := Pick(d, cfg, clk("16:30"), split); ok || !d.Events["afternoon_log"].Done {
		t.Fatalf("16:30 should skip: %s", ev)
	}
	if ev, _ := Pick(d, cfg, clk("17:00"), split); ev != End {
		t.Fatalf("17:00: %s", ev)
	}
	Shown(d, cfg, End, clk("17:00"))
	StillWorking(d, cfg, clk("17:05"))
	if ev, ok := Pick(d, cfg, clk("17:30"), split); ok {
		t.Fatalf("17:30 too early after 'still working' at 17:05: %s", ev)
	}
	if ev, _ := Pick(d, cfg, clk("17:35"), split); ev != End {
		t.Fatalf("17:35: %s", ev)
	}
	Shown(d, cfg, End, clk("17:35"))
	// Day finished elsewhere → no further question.
	if ev, ok := Pick(d, cfg, clk("18:05"), finished); ok || !d.Events["end"].Done {
		t.Fatalf("18:05: %s", ev)
	}
}

func TestSkipWhenAlreadyDone(t *testing.T) {
	d := NewDay(cfg, thu, false)
	if ev, ok := Pick(d, cfg, clk("08:00"), open8Full); ok {
		t.Errorf("presence exists → no start question, got %s", ev)
	}
	if ev, ok := Pick(d, cfg, clk("12:30"), open8Full); ok {
		t.Errorf("all logged → no morning reminder, got %s", ev)
	}
	if ev, ok := Pick(d, cfg, clk("14:00"), split); ok {
		t.Errorf("break recorded → no question, got %s", ev)
	}
}

func TestWakeShowsOnlyLatestRelevant(t *testing.T) {
	// Laptop closed all morning, opened at 13:30 without having started the day in MOCO:
	// start, re-ask and morning reminder are due; morning is irrelevant without a presence,
	// so exactly the start question appears.
	d := NewDay(cfg, thu, false)
	if ev, _ := Pick(d, cfg, clk("13:30"), none); ev != Start {
		t.Fatalf("got %s", ev)
	}
	Shown(d, cfg, Start, clk("13:30"))
	if !d.Events["start"].Done || !d.Events["morning_log"].Done {
		t.Errorf("state: %+v %+v", d.Events["start"], d.Events["morning_log"])
	}

	// Opened at 18:10 with an open presence: only the end question, the rest is dropped.
	d = NewDay(cfg, thu, false)
	if ev, _ := Pick(d, cfg, clk("18:10"), open8); ev != End {
		t.Fatalf("got %s", ev)
	}
	Shown(d, cfg, End, clk("18:10"))
	for _, ev := range []Event{Start, Morning, Break, Afternoon} {
		if !d.Events[string(ev)].Done {
			t.Errorf("%s should be closed", ev)
		}
	}
	if ev, ok := Pick(d, cfg, clk("18:11"), open8); ok {
		t.Errorf("burst: %s", ev)
	}
}

func TestAnswers(t *testing.T) {
	d := NewDay(cfg, thu, false)
	Pick(d, cfg, clk("08:00"), none)
	Shown(d, cfg, Start, clk("08:00"))
	NotYet(d, cfg, Start, clk("08:02"))
	if st := d.Events["start"]; st.Done || !st.Next.Equal(clk("09:00")) {
		t.Errorf("not yet → 09:00: %+v", st)
	}
	Pick(d, cfg, clk("09:00"), none)
	Shown(d, cfg, Start, clk("09:00"))
	NotYet(d, cfg, Start, clk("09:01"))
	if !d.Events["start"].Done {
		t.Error("second not yet → done")
	}

	Pick(d, cfg, clk("12:30"), open8)
	Shown(d, cfg, Morning, clk("12:30"))
	Snooze(d, cfg, Morning, clk("12:31"))
	if st := d.Events["morning_log"]; st.Done || !st.Next.Equal(clk("13:01")) {
		t.Errorf("snooze: %+v", st)
	}

	Pick(d, cfg, clk("14:00"), open8)
	Shown(d, cfg, Break, clk("14:00"))
	NotYet(d, cfg, Break, clk("14:00"))
	if st := d.Events["break"]; st.Done || !st.Next.Equal(clk("14:30")) {
		t.Errorf("no break yet: %+v", st)
	}

	DayOff(d)
	if ev, ok := Pick(d, cfg, clk("23:00"), open8); ok {
		t.Errorf("day off: %s", ev)
	}
}

func TestEndReaskStopsAtLimit(t *testing.T) {
	d := NewDay(cfg, thu, false)
	Pick(d, cfg, clk("19:45"), open8)
	Shown(d, cfg, End, clk("19:45"))
	if !d.Events["end"].Done {
		t.Errorf("next ask 20:15 is after the 20:00 limit: %+v", d.Events["end"])
	}
}

func TestDaysOffAndUnknownState(t *testing.T) {
	if d := NewDay(cfg, thu.AddDate(0, 0, 2), false); !d.Events["start"].Done {
		t.Error("Saturday must have no reminders")
	}
	if d := NewDay(cfg, thu, true); !d.Events["end"].Done {
		t.Error("paused day must have no reminders")
	}
	// MOCO unreachable: the start question is asked, the others wait.
	d := NewDay(cfg, thu, false)
	if ev, _ := Pick(d, cfg, clk("12:30"), nil); ev != Start {
		t.Errorf("got %s", ev)
	}
	if d.Events["morning_log"].Done {
		t.Error("morning reminder must wait for MOCO, not be dropped")
	}
}

func TestSkippedStartIsRevived(t *testing.T) {
	d := NewDay(cfg, thu, false)
	early := dayWith([]api.Presence{{From: "08:00", To: "13:00"}}, 0, 0) // a test entry
	if ev, ok := Pick(d, cfg, clk("08:00"), early); ok {
		t.Fatalf("start asked although a presence exists: %s", ev)
	}
	st := d.Events["start"]
	if !st.Done || !st.Skipped {
		t.Fatalf("start state %+v, want done+skipped", st)
	}
	if got := SkipReason(Start, early); got != "presence already recorded (08:00–13:00)" {
		t.Errorf("reason %q", got)
	}
	if Revive(d, cfg, clk("08:05"), early) {
		t.Error("revived while the presence still exists")
	}
	if !Revive(d, cfg, clk("08:10"), none) {
		t.Fatal("not revived after the presence was deleted")
	}
	if ev, _ := Pick(d, cfg, clk("08:10"), none); ev != Start {
		t.Fatalf("after revival: %s", ev)
	}

	// An answered start question is never revived, and nothing after the end of the day.
	d = NewDay(cfg, thu, false)
	Pick(d, cfg, clk("08:00"), none)
	Shown(d, cfg, Start, clk("08:00"))
	Answered(d, Start)
	if Revive(d, cfg, clk("10:00"), none) {
		t.Error("answered start revived")
	}
	d = NewDay(cfg, thu, false)
	Pick(d, cfg, clk("08:00"), early)
	if Revive(d, cfg, clk("17:00"), none) {
		t.Error("revived after the end of the day")
	}
}

func TestPausedDayIsNotRevived(t *testing.T) {
	d := NewDay(cfg, thu, false)
	Pick(d, cfg, clk("08:00"), dayWith([]api.Presence{{From: "08:00", To: "13:00"}}, 0, 0))
	DayOff(d)
	if Revive(d, cfg, clk("09:00"), none) {
		t.Error("revived on a day off")
	}
}
