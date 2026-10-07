package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type projectsState struct {
	list      []api.Project
	period    int
	hours     *service.Hours
	hoursKey  string
	loading   bool
	cursor    int
	open      *api.Project
	task      int
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

func (m *model) loadProjects(refresh bool) tea.Cmd {
	return func() tea.Msg {
		ps, err := m.svc.Projects(m.ctx, refresh)
		return projectsMsg{ps, err}
	}
}

func (m *model) loadHours(force bool) tea.Cmd {
	from, to := periodRange(m.projects.period, m.svc.Now())
	key := timeutil.Date(from) + "/" + timeutil.Date(to)
	if !force && m.projects.hours != nil && m.projects.hoursKey == key {
		return nil
	}
	m.projects.loading = true
	m.projects.hoursKey = key
	return func() tea.Msg {
		acts, err := m.svc.ActivitiesBetween(m.ctx, from, to)
		return hoursMsg{key, service.SumHours(acts, m.svc.Now()), err}
	}
}

func (m *model) hoursLoaded(msg hoursMsg) {
	if msg.key != m.projects.hoursKey {
		return
	}
	m.projects.loading = false
	m.noteErr(msg.err)
	if msg.err == nil {
		m.projects.hours = &msg.hours
	}
}

// visibleProjects is the filtered project list, projects with hours in the period first.
func (m *model) visibleProjects() []api.Project {
	ps := m.projects.list
	if m.projects.filter != "" {
		ps = service.FindProjects(ps, m.projects.filter)
	}
	out := append([]api.Project{}, ps...)
	if h := m.projects.hours; h != nil {
		sort.SliceStable(out, func(i, j int) bool { return h.ByProject[out[i].ID] > h.ByProject[out[j].ID] })
	}
	return out
}

type taskRow struct {
	task    api.Task
	seconds int
}

// visibleTasks lists the open project's active tasks plus inactive ones with hours.
func (m *model) visibleTasks() []taskRow {
	p := m.projects.open
	var byTask map[int64]int
	if h := m.projects.hours; h != nil {
		byTask = h.ByTask[p.ID]
	}
	var rows []taskRow
	for _, t := range p.Tasks {
		if t.Active || byTask[t.ID] > 0 {
			rows = append(rows, taskRow{t, byTask[t.ID]})
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].seconds > rows[j].seconds })
	return rows
}

func (m *model) projectsKey(msg tea.KeyMsg) tea.Cmd {
	s := &m.projects
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
			s.cursor = min(len(m.visibleProjects())-1, s.cursor+1)
		case tea.KeyRunes, tea.KeySpace:
			s.filter += string(msg.Runes)
			s.cursor = 0
		}
		return nil
	}

	switch msg.String() {
	case "p":
		s.period = (s.period + 1) % len(periodNames)
		return m.loadHours(false)
	case "P":
		s.period = (s.period + len(periodNames) - 1) % len(periodNames)
		return m.loadHours(false)
	}
	if s.open != nil {
		switch msg.String() {
		case "esc", "left", "h", "backspace":
			s.open = nil
		case "up", "k":
			s.task = max(0, s.task-1)
		case "down", "j":
			s.task = min(len(m.visibleTasks())-1, s.task+1)
		}
		return nil
	}
	switch msg.String() {
	case "/":
		s.filtering = true
	case "esc":
		s.filter, s.cursor = "", 0
	case "up", "k":
		s.cursor = max(0, s.cursor-1)
	case "down", "j":
		s.cursor = min(len(m.visibleProjects())-1, s.cursor+1)
	case "enter", "right", "l":
		ps := m.visibleProjects()
		if s.cursor < len(ps) {
			p := ps[s.cursor]
			s.open, s.task = &p, 0
		}
	}
	return nil
}

func (m *model) projectsView(height int) string {
	s := &m.projects
	from, to := periodRange(s.period, m.svc.Now())
	var lines []string
	add := func(x string) { lines = append(lines, x) }

	header := sTitle.Render(periodNames[s.period]) + sMuted.Render(fmt.Sprintf(" · %s – %s", from.Format("2 Jan"), to.Format("2 Jan 2006")))
	switch {
	case s.loading:
		header += sMuted.Render(" · loading…")
	case s.hours != nil:
		header += sMuted.Render(" · total ") + timeutil.FormatSeconds(s.hours.Total)
	}
	add(header)
	if s.list == nil {
		add("")
		add(sMuted.Render("Loading projects…"))
		return strings.Join(lines, "\n")
	}

	if s.open != nil {
		p := s.open
		add(sProject.Render(p.Name) + sMuted.Render(" · "+p.Customer.Name+" · "+p.Identifier))
		add("")
		rows := m.visibleTasks()
		if len(rows) == 0 {
			add(sMuted.Render("  no active tasks"))
		}
		cursorLine := 0
		for i, r := range rows {
			on := i == s.task
			if on {
				cursorLine = len(lines)
			}
			name := r.task.Name
			if !r.task.Active {
				name += sMuted.Render(" (inactive)")
			}
			row := pad(clip(name, max(20, m.width-16)), max(20, m.width-16)) + padLeft(dashIfZero(r.seconds), 8)
			if on {
				row = sSelected.Render(row)
			}
			add(cursor(on) + row)
		}
		return window(lines, cursorLine, height)
	}

	filter := sMuted.Render("/ to filter")
	if s.filtering || s.filter != "" {
		filter = sSection.Render("Filter: ") + s.filter
		if s.filtering {
			filter += sCursor.Render("█")
		}
	}
	add(filter)
	ps := m.visibleProjects()
	if len(ps) == 0 {
		add(sMuted.Render("  no matching project"))
	}
	nameW := max(20, m.width-16)
	cursorLine := 0
	for i, p := range ps {
		on := i == s.cursor
		if on {
			cursorLine = len(lines)
		}
		sec := 0
		if s.hours != nil {
			sec = s.hours.ByProject[p.ID]
		}
		name := sProject.Render(p.Name) + sMuted.Render(" · "+p.Customer.Name+" · "+p.Identifier)
		row := pad(clip(name, nameW), nameW) + padLeft(dashIfZero(sec), 8)
		if on {
			row = sSelected.Render(row)
		}
		add(cursor(on) + row)
	}
	return window(lines, cursorLine, height)
}
