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

func count(fake *fakemoco.Server, path string) int {
	n := 0
	for _, r := range fake.Requests {
		if strings.HasSuffix(r, path) {
			n++
		}
	}
	return n
}

func TestDayView(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.ensureWeek(false))
	v := m.View()
	for _, want := range []string{"[1] Week 41", "[2] Presence", "[3] Activities · Tue 6 Oct · today", "08:00–12:00", "13:00–…",
		"running", "Intern / Orga", "Planning", "3h00 missing", "-3h00", "Details"} {
		if !strings.Contains(v, want) {
			t.Errorf("view lacks %q:\n%s", want, v)
		}
	}
}

func TestDaysOfAWeekComeFromTheCache(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.ensureWeek(false))
	before := count(fake, "/users/presences")
	press(m, "h", "l", "h", "l") // Mon, Tue, Mon, Tue: all in the cached week
	if got := count(fake, "/users/presences") - before; got != 0 {
		t.Errorf("%d requests for days of a cached week", got)
	}
	press(m, "h", "h") // Mon, then the previous Friday (weekend skipped): another week
	if m.date.Format("2006-01-02") != "2026-10-02" {
		t.Errorf("date %s, want Fri 2026-10-02", m.date)
	}
}

func TestWeekNavigationIsDebounced(t *testing.T) {
	m, fake := newTestModel(t)
	do(m, m.ensureWeek(false))
	before := count(fake, "/users/presences")
	var cmds []tea.Cmd
	for i := 0; i < 3; i++ { // three weeks back in quick succession
		_, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("H")})
		cmds = append(cmds, cmd)
	}
	for _, c := range cmds { // the debounce timers fire afterwards
		do(m, c)
	}
	if got := count(fake, "/users/presences") - before; got != 1 {
		t.Errorf("%d week loads, want 1 (only the week where the navigation stopped)", got)
	}
	if !strings.Contains(m.View(), "Week 38") || m.week() == nil || m.week().days == nil {
		t.Error("target week not loaded")
	}
}

func TestAddPresenceViaPrompt(t *testing.T) {
	m, fake := newTestModel(t)
	do(m, m.ensureWeek(false))
	do(m, m.selectDate(now.AddDate(0, 0, -1))) // Monday, empty, same (cached) week
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
		t.Error("week not reloaded after the write")
	}
}

func TestPromptValidation(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, m.ensureWeek(false))
	press(m, "2", "e") // first presence 08:00–12:00
	if m.prompt == nil {
		t.Fatal("no prompt")
	}
	if !strings.Contains(m.View(), "Edit presence 08:00–12:00") {
		t.Error("popup not drawn")
	}
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
	do(m, m.ensureWeek(false))
	press(m, "b", "enter", "enter") // configured 13:00–14:00
	if ps := fake.Presences("2026-10-06"); len(ps) != 2 || ps[0].To != "13:00" || ps[1].From != "14:00" {
		t.Fatalf("after break %+v (msg %q)", ps, m.msg)
	}
	press(m, "2", "g", "m")
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
	do(m, m.ensureWeek(false))
	press(m, "3", "d", "n")
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
	do(m, m.ensureWeek(false))
	fake.SetDown(http.StatusServiceUnavailable)
	press(m, "s", "enter")
	if m.msgKind != msgAlarm || !strings.Contains(m.msg, "NOT SAVED IN MOCO YET") {
		t.Fatalf("msg %q", m.msg)
	}
	if !m.offline || m.pending != 1 {
		t.Errorf("offline %v pending %d", m.offline, m.pending)
	}
	if v := m.View(); !strings.Contains(v, "1 queued — NOT in MOCO yet") || !strings.Contains(v, "13:00–…") {
		t.Errorf("status bar lacks the queue warning or the cached day is gone:\n%s", v)
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

func TestWeekPanel(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	fake.AddPresence(api.Presence{Date: "2026-10-05", From: "08:00", To: "16:00"})
	fake.AddActivity(api.Activity{Date: "2026-10-05", Seconds: 8 * 3600, Project: api.Ref{ID: 1}, Task: api.Ref{ID: 10}})
	do(m, m.ensureWeek(false))
	lines, cur := m.weekLines()
	text := strings.Join(lines, "\n")
	for _, want := range []string{"Mon 5", "✓", "Tue 6", "-3h00", "Fri 9"} {
		if !strings.Contains(text, want) {
			t.Errorf("week lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Sat 10") {
		t.Error("empty weekend shown")
	}
	if cur != 1 {
		t.Errorf("cursor %d, want Tuesday", cur)
	}
	press(m, "1", "k", "enter")
	if m.focus != pActivities || m.date.Format("2006-01-02") != "2026-10-05" {
		t.Errorf("focus %v day %s", m.focus, m.date)
	}
	press(m, "l", "l") // never into the future
	if m.date.Format("2006-01-02") != "2026-10-06" {
		t.Errorf("date %s", m.date)
	}
}

func TestProjectsPanel(t *testing.T) {
	m, fake := newTestModel(t)
	seed(fake)
	do(m, func() tea.Msg {
		return projectsMsg{projects: []api.Project{
			{ID: 2, Name: "ACME Website", Customer: api.Ref{Name: "ACME"}, Identifier: "P2", Tasks: []api.Task{{ID: 20, Name: "Dev", Active: true}}},
			{ID: 1, Name: "Intern", Customer: api.Ref{Name: "artismedia"}, Identifier: "P1", Tasks: []api.Task{{ID: 10, Name: "Orga", Active: true}}},
		}}
	})
	do(m, m.ensureHours(false))
	press(m, "4")
	v := m.View()
	if !strings.Contains(v, "[4] Projects · This week") || !strings.Contains(v, "Tasks · Intern") || !strings.Contains(v, "Orga") {
		t.Fatalf("projects view:\n%s", v)
	}
	if ps := m.visibleProjects(); ps[0].ID != 1 {
		t.Error("project with hours should come first")
	}
	press(m, "/", "a", "c", "m", "e", "enter")
	if ps := m.visibleProjects(); len(ps) != 1 || ps[0].ID != 2 {
		t.Errorf("filtered %+v", ps)
	}
	if !strings.Contains(m.View(), "Tasks · ACME Website") {
		t.Error("main panel does not follow the selected project")
	}
	press(m, "esc")
	if m.projects.filter != "" || m.focus != pProjects {
		t.Errorf("esc: filter %q focus %v", m.projects.filter, m.focus)
	}
}
