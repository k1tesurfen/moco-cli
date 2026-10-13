package tui

import (
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

// Joining projects: the company's projects the user is not on are loaded once per session
// (when a popup that lists them opens) and offered as "+" rows; joining one adds the user to
// its team in MOCO and makes its tasks bookable right away.

type joinableMsg struct {
	projects []api.Project
	err      error
}

// joinedMsg is the result of joining (left false) or leaving (left true) project p.
type joinedMsg struct {
	project  api.Project // as assigned after joining (with tasks); the left project when leaving
	left     bool
	projects []api.Project // the assigned projects afterwards
	err      error
}

// loadJoinable loads the joinable projects unless they are loaded or loading.
func (m *model) loadJoinable() tea.Cmd {
	if m.joinable != nil || m.joinableLoading {
		return nil
	}
	m.joinableLoading = true
	return func() tea.Msg {
		ps, err := m.svc.JoinableProjects(m.ctx)
		return joinableMsg{ps, err}
	}
}

// joinableNow is the loaded joinable projects minus those joined since.
func (m *model) joinableNow() []api.Project {
	mine := map[int64]bool{}
	for _, p := range m.projects.list {
		mine[p.ID] = true
	}
	var out []api.Project
	for _, p := range m.joinable {
		if !mine[p.ID] {
			out = append(out, p)
		}
	}
	return out
}

func (m *model) joinableLoaded(msg joinableMsg) {
	m.joinableLoading = false
	if msg.err != nil {
		m.noteErr(msg.err)
		if m.form != nil {
			m.form.joinPending = false
			m.form.err = "Couldn't load the projects you could join: " + msg.err.Error()
		}
		return
	}
	m.joinable = msg.projects
	if m.joinable == nil {
		m.joinable = []api.Project{}
	}
	if m.form != nil {
		m.form.setJoinable(m.joinableNow())
	}
}

// enableJoin lets the open form offer the joinable projects; it returns the load command.
func (m *model) enableJoin(f *activityForm) tea.Cmd {
	f.onJoin = m.join
	if m.joinable != nil {
		f.setJoinable(m.joinableNow())
		return nil
	}
	f.joinPending = true
	return m.loadJoinable()
}

func (m *model) join(p api.Project) tea.Cmd {
	m.busy++
	m.setMsg(msgInfo, "Joining "+p.Name+"…")
	return func() tea.Msg {
		joined, err := m.svc.JoinProject(m.ctx, p)
		if err != nil {
			return joinedMsg{project: p, err: err}
		}
		ps, err := m.svc.Projects(m.ctx, false)
		return joinedMsg{project: joined, projects: ps, err: err}
	}
}

// joinProject opens the join popup (projects panel).
func (m *model) joinProject() tea.Cmd {
	f := newJoinForm()
	f.setWidth(m.formWidth() - 4)
	m.form = f
	return m.enableJoin(f)
}

// leaveProject asks to take the user off the selected project's team.
func (m *model) leaveProject() {
	sel := m.selProject()
	if sel == nil {
		return
	}
	p := *sel
	m.confirm = &confirmBox{
		question: "Leave " + p.Name + "?",
		detail:   "Removes you from the project's team in MOCO. MOCO only allows this while you have no hours booked on it.",
		yes: func() tea.Cmd {
			m.busy++
			m.setMsg(msgInfo, "Leaving "+p.Name+"…")
			return func() tea.Msg {
				if err := m.svc.LeaveProject(m.ctx, p); err != nil {
					return joinedMsg{project: p, left: true, err: err}
				}
				ps, err := m.svc.Projects(m.ctx, false)
				return joinedMsg{project: p, left: true, projects: ps, err: err}
			}
		},
	}
}

func (m *model) joinedDone(msg joinedMsg) {
	m.busy--
	if msg.projects != nil {
		m.projects.list = msg.projects
		m.projects.cursor = clamp(m.projects.cursor, 0, max(0, len(m.visibleProjects())-1))
	}
	if msg.err != nil {
		if api.IsUnreachable(msg.err) {
			m.offline = true
		}
		m.setMsg(msgErr, msg.err.Error())
		if f := m.form; f != nil && f.joining != "" {
			f.joining, f.err = "", msg.err.Error()
		}
		return
	}
	m.offline = false
	if msg.left {
		if m.joinable != nil {
			m.joinable = append(m.joinable, msg.project) // offered again
		}
		m.setMsg(msgOK, fmt.Sprintf("Left %s.", msg.project.Name))
		return
	}
	m.setMsg(msgOK, fmt.Sprintf("Joined %s — its tasks are bookable now.", msg.project.Name))
	if f := m.form; f != nil {
		if f.joinOnly {
			m.form = nil
			m.focusProject(msg.project.ID)
			return
		}
		f.joined(msg.project)
	}
}

// focusProject moves the projects panel cursor onto project id.
func (m *model) focusProject(id int64) {
	m.projects.filter, m.projects.filtering = "", false
	for i, p := range m.visibleProjects() {
		if p.ID == id {
			m.projects.cursor = i
		}
	}
}
