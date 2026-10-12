package tui

import (
	"sort"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type projectsState struct {
	list      []api.Project
	period    int
	hours     map[string]*service.Hours // by period key
	loading   map[string]bool
	cursor    int
	filtering bool
	filter    string
}

type projectsMsg struct {
	projects []api.Project
	err      error
}

type hoursMsg struct {
	key   string
	hours service.Hours
	err   error
}

var periodNames = []string{"This week", "Last week", "This month", "Last month", "This year"}

// periodRange returns the dates of period i relative to now.
func periodRange(i int, now time.Time) (time.Time, time.Time) {
	today := midnight(now)
	mon := service.Monday(today)
	first := time.Date(today.Year(), today.Month(), 1, 0, 0, 0, 0, today.Location())
	switch i {
	case 1:
		return mon.AddDate(0, 0, -7), mon.AddDate(0, 0, -1)
	case 2:
		return first, first.AddDate(0, 1, -1)
	case 3:
		return first.AddDate(0, -1, 0), first.AddDate(0, 0, -1)
	case 4:
		return time.Date(today.Year(), 1, 1, 0, 0, 0, 0, today.Location()), time.Date(today.Year(), 12, 31, 0, 0, 0, 0, today.Location())
	}
	return mon, mon.AddDate(0, 0, 6)
}

func (m *model) periodKey() string {
	from, to := periodRange(m.projects.period, m.svc.Now())
	return timeutil.Date(from) + "/" + timeutil.Date(to)
}

// hours returns the cached hours of the selected period (nil while not loaded).
func (m *model) hours() *service.Hours { return m.projects.hours[m.periodKey()] }

func (m *model) loadProjects(refresh bool) tea.Cmd {
	return func() tea.Msg {
		ps, err := m.svc.Projects(m.ctx, refresh)
		return projectsMsg{ps, err}
	}
}

// ensureHours loads the hours of the selected period unless cached; with delay it waits for
// the period switching to settle.
func (m *model) ensureHours(delay bool) tea.Cmd {
	key := m.periodKey()
	if m.projects.hours[key] != nil || m.projects.loading[key] {
		return nil
	}
	m.hoursSeq++
	if delay {
		seq := m.hoursSeq
		return tea.Tick(debounceDelay, func(time.Time) tea.Msg { return debounceMsg{seq, debounceHours} })
	}
	m.projects.loading[key] = true
	from, to := periodRange(m.projects.period, m.svc.Now())
	return func() tea.Msg {
		acts, err := m.svc.ActivitiesBetween(m.ctx, from, to)
		return hoursMsg{key, service.SumHours(acts, m.svc.Now()), err}
	}
}

func (m *model) hoursLoaded(msg hoursMsg) {
	delete(m.projects.loading, msg.key)
	m.noteErr(msg.err)
	if msg.err == nil {
		m.projects.hours[msg.key] = &msg.hours
	}
}

// visibleProjects is the filtered project list, projects with hours in the period first.
func (m *model) visibleProjects() []api.Project {
	ps := m.projects.list
	if m.projects.filter != "" {
		ps = service.FindProjects(ps, m.projects.filter)
	}
	out := append([]api.Project{}, ps...)
	if h := m.hours(); h != nil {
		sort.SliceStable(out, func(i, j int) bool { return h.ByProject[out[i].ID] > h.ByProject[out[j].ID] })
	}
	return out
}

func (m *model) selProject() *api.Project {
	ps := m.visibleProjects()
	if m.projects.cursor < len(ps) {
		return &ps[m.projects.cursor]
	}
	return nil
}

func (m *model) projectsKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.projects
	n := len(m.visibleProjects())
	if s.filtering {
		switch msg.Type {
		case tea.KeyEsc:
			s.filtering, s.filter, s.cursor = false, "", 0
		case tea.KeyEnter:
			s.filtering = false
		case tea.KeyBackspace:
			if r := []rune(s.filter); len(r) > 0 {
				s.filter, s.cursor = string(r[:len(r)-1]), 0
			}
		case tea.KeyUp:
			s.cursor = max(0, s.cursor-1)
		case tea.KeyDown:
			s.cursor = clamp(s.cursor+1, 0, n-1)
		case tea.KeyRunes, tea.KeySpace:
			s.filter += string(msg.Runes)
			s.cursor = 0
		}
		return nil
	}
	switch msg.String() {
	case "p":
		s.period = (s.period + 1) % len(periodNames)
		return m.ensureHours(true)
	case "P":
		s.period = (s.period + len(periodNames) - 1) % len(periodNames)
		return m.ensureHours(true)
	case "/":
		s.filtering = true
	case "esc":
		s.filter, s.cursor = "", 0
	case "up", "k":
		s.cursor = max(0, s.cursor-1)
	case "down", "j":
		s.cursor = clamp(s.cursor+1, 0, n-1)
	case "home", "g":
		s.cursor = 0
	case "end", "G":
		s.cursor = max(0, n-1)
	}
	return nil
}

func (m *model) projectsBox() box {
	s := &m.projects
	b := box{
		title:   "[4] Projects · " + periodNames[s.period],
		cursor:  s.cursor,
		focused: m.focus == pProjects,
		offset:  &m.offsets[pProjects],
	}
	if s.list == nil {
		b.lines, b.cursor = []string{sMuted.Render("loading…")}, -1
		return b
	}
	h := m.hours()
	for _, p := range m.visibleProjects() {
		sec := 0
		if h != nil {
			sec = h.ByProject[p.ID]
		}
		name := p.Name
		hrs := padLeft(dashIfZero(sec), 6)
		if sec == 0 {
			hrs = sMuted.Render(hrs)
		}
		b.lines = append(b.lines, sProject.Render(name))
		b.right = append(b.right, hrs)
	}
	if len(b.lines) == 0 {
		b.lines, b.cursor = []string{sMuted.Render("no matching project")}, -1
	}
	switch {
	case s.filtering:
		b.footer = "/" + s.filter + "█"
	case s.filter != "":
		b.footer = "/" + s.filter
	case m.projects.loading[m.periodKey()]:
		b.footer = "loading…"
	case h != nil:
		b.footer = timeutil.FormatSeconds(h.Total)
	}
	return b
}

// tasksBox is the main panel while the projects panel is focused: the selected project's tasks
// with their hours in the period.
func (m *model) tasksBox() box {
	p := m.selProject()
	b := box{title: "Tasks", cursor: -1}
	if p == nil {
		return b
	}
	b.title = "Tasks · " + p.Name
	var byTask map[int64]int
	if h := m.hours(); h != nil {
		byTask = h.ByTask[p.ID]
		b.footer = periodNames[m.projects.period] + " · " + timeutil.FormatSeconds(h.ByProject[p.ID])
	}
	type row struct {
		t   api.Task
		sec int
	}
	var rows []row
	for _, t := range p.Tasks {
		if t.Active || byTask[t.ID] > 0 {
			rows = append(rows, row{t, byTask[t.ID]})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].sec > rows[j].sec })
	for _, r := range rows {
		name := r.t.Name
		if !r.t.Active {
			name += sMuted.Render(" (inactive)")
		}
		hrs := padLeft(dashIfZero(r.sec), 6)
		if r.sec == 0 {
			hrs = sMuted.Render(hrs)
		}
		b.lines = append(b.lines, name)
		b.right = append(b.right, hrs)
	}
	if len(rows) == 0 {
		b.lines = []string{sMuted.Render("no active tasks")}
	}
	return b
}
