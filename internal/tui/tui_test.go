package tui

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/fakemoco"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

var now = time.Date(2026, 10, 6, 15, 0, 0, 0, time.Local) // Tuesday

func newTestModel(t *testing.T) (*model, *fakemoco.Server) {
	t.Helper()
	fake := fakemoco.New(t)
	fake.Now = func() time.Time { return now }
	svc := &service.Service{
		Cfg:    config.Default(),
		API:    fake.Client(),
		Store:  store.NewAt(t.TempDir()),
		UserID: fakemoco.UserID,
		Now:    func() time.Time { return now },
	}
	m := newModel(context.Background(), svc)
	m.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	return m, fake
}

// do runs a command and feeds its messages back into the model (synchronously). Tick and exec
// commands are not used by the tests.
func do(m *model, cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			do(m, c)
		}
		return
	}
	if msg == nil {
		return
	}
	_, next := m.Update(msg)
	do(m, next)
}

func press(m *model, keys ...string) {
	for _, k := range keys {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "tab":
			msg = tea.KeyMsg{Type: tea.KeyTab}
		case "ctrl+u":
			msg = tea.KeyMsg{Type: tea.KeyCtrlU}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		_, cmd := m.Update(msg)
		do(m, cmd)
	}
}

func seed(fake *fakemoco.Server) {
	fake.AddProject(api.Project{ID: 1, Name: "Intern", Customer: api.Ref{Name: "artismedia"}, Identifier: "P1",
		Active: true, Tasks: []api.Task{{ID: 10, Name: "Orga", Active: true}}})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00", To: "12:00"})
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "13:00"})
	fake.AddActivity(api.Activity{Date: "2026-10-06", Seconds: 3 * 3600, Description: "Planning",
		Project: api.Ref{ID: 1, Name: "Intern"}, Task: api.Ref{ID: 10, Name: "Orga"}})
}

func TestDayView(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.loadDay())
	v := m.View()
	for _, want := range []string{"Tuesday, 6 October 2026", "08:00–12:00", "13:00–…", "running", "Intern / Orga",
		"Planning", "Present 6h00", "Logged 3h00", "Missing 3h00"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestAddPresenceViaPrompt(t *testing.T) {
	m, fake := newTestModel(t)
	do(m, m.setDay(now.AddDate(0, 0, -1))) // Monday, empty
	press(m, "n")
	if m.prompt == nil {
		t.Fatal("no prompt")
	}
	press(m, "8", "tab", "1", "2", "3", "0", "enter")
	if m.prompt != nil {
		t.Fatalf("prompt still open: %s", m.prompt.err)
	}
	ps := fake.Presences("2026-10-05")
	if len(ps) != 1 || ps[0].From != "08:00" || ps[0].To != "12:30" {
		t.Fatalf("presences %+v", ps)
	}
	if !strings.Contains(m.msg, "Added presence 08:00–12:30") {
		t.Errorf("msg %q", m.msg)
	}
	if !strings.Contains(m.View(), "08:00–12:30") {
		t.Error("day not reloaded")
	}
}

func TestPromptValidation(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.loadDay())
	press(m, "e") // first presence 08:00–12:00
	press(m, "tab", "ctrl+u", "7", "enter")
	if m.prompt == nil || !strings.Contains(m.prompt.err, "not after the start") {
		t.Fatalf("expected validation error, prompt = %+v", m.prompt)
	}
	press(m, "esc")
	if m.prompt != nil {
		t.Fatal("esc did not close the prompt")
	}
}

func TestBreakAndMerge(t *testing.T) {
	m, fake := newTestModel(t)
	fake.AddPresence(api.Presence{Date: "2026-10-06", From: "08:00"})
	do(m, m.loadDay())
	press(m, "b", "enter", "enter") // configured 13:00–14:00
	if ps := fake.Presences("2026-10-06"); len(ps) != 2 || ps[0].To != "13:00" || ps[1].From != "14:00" {
		t.Fatalf("after break %+v (msg %q)", ps, m.msg)
	}
	m.day.cursor = 0
	press(m, "m")
	if m.confirm == nil {
		t.Fatal("no confirm")
	}
	press(m, "y")
	if ps := fake.Presences("2026-10-06"); len(ps) != 1 || ps[0].From != "08:00" || ps[0].To != "" {
		t.Fatalf("after merge %+v (msg %q)", ps, m.msg)
	}
}

func TestDeleteActivityConfirm(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.loadDay())
	m.day.cursor = 2 // the activity
	press(m, "d", "n")
	if len(fake.Activities("2026-10-06")) != 1 {
		t.Fatal("deleted although declined")
	}
	press(m, "d", "y")
	if len(fake.Activities("2026-10-06")) != 0 {
		t.Fatalf("not deleted (msg %q)", m.msg)
	}
}

func TestOfflineStopIsQueued(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.loadDay())
	fake.SetDown(http.StatusServiceUnavailable)
	press(m, "s", "enter")
	if m.msgKind != msgAlarm || !strings.Contains(m.msg, "NOT SAVED IN MOCO YET") {
		t.Fatalf("msg %q", m.msg)
	}
	if !m.offline || m.pending != 1 {
		t.Errorf("offline %v pending %d", m.offline, m.pending)
	}
	if !strings.Contains(m.View(), "1 queued — NOT in MOCO yet") {
		t.Error("status bar lacks the queue warning")
	}
	fake.SetDown(0)
	do(m, m.syncQueue())
	if ps := fake.Presences("2026-10-06"); ps[1].To != "15:00" {
		t.Errorf("after sync %+v", ps)
	}
	if m.pending != 0 || m.offline {
		t.Errorf("after sync pending %d offline %v", m.pending, m.offline)
	}
}

func TestWeekView(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	fake.AddPresence(api.Presence{Date: "2026-10-05", From: "08:00", To: "16:00"})
	fake.AddActivity(api.Activity{Date: "2026-10-05", Seconds: 8 * 3600, Project: api.Ref{ID: 1}, Task: api.Ref{ID: 10}})
	press(m, "2")
	v := m.View()
	for _, want := range []string{"Week 41", "Mon 5 Oct", "08:00–16:00", "✓", "Tue 6 Oct", "3h00 missing", "Fri 9 Oct"} {
		if !strings.Contains(v, want) {
			t.Errorf("week view lacks %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "Sat 10 Oct") {
		t.Error("empty weekend shown")
	}
	m.week.cursor = 1
	press(m, "k", "enter")
	if m.view != dayView || m.day.date.Format("2006-01-02") != "2026-10-05" {
		t.Errorf("view %v day %s", m.view, m.day.date)
	}
}

func TestProjectsView(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	fake.AddProject(api.Project{ID: 2, Name: "ACME Website", Customer: api.Ref{Name: "ACME"}, Identifier: "P2",
		Active: true, Tasks: []api.Task{{ID: 20, Name: "Dev", Active: true}}})
	// The fake has no /projects/assigned; feed the list directly.
	do(m, func() tea.Msg {
		return projectsMsg{projects: []api.Project{
			{ID: 2, Name: "ACME Website", Customer: api.Ref{Name: "ACME"}, Identifier: "P2", Tasks: []api.Task{{ID: 20, Name: "Dev", Active: true}}},
			{ID: 1, Name: "Intern", Customer: api.Ref{Name: "artismedia"}, Identifier: "P1", Tasks: []api.Task{{ID: 10, Name: "Orga", Active: true}}},
		}}
	})
	press(m, "3")
	v := m.View()
	if !strings.Contains(v, "This week") || !strings.Contains(v, "total 3h00") {
		t.Fatalf("projects view:\n%s", v)
	}
	if strings.Index(v, "Intern") > strings.Index(v, "ACME Website") {
		t.Error("project with hours should come first")
	}
	press(m, "/", "a", "c", "m", "e", "enter")
	if ps := m.visibleProjects(); len(ps) != 1 || ps[0].ID != 2 {
		t.Errorf("filtered %+v", ps)
	}
	press(m, "esc", "enter")
	if m.projects.open == nil || m.projects.open.ID != 1 {
		t.Fatalf("open %+v", m.projects.open)
	}
	if v := m.View(); !strings.Contains(v, "Orga") || !strings.Contains(v, "3h00") {
		t.Errorf("tasks view:\n%s", v)
	}
}
