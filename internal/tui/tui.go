// Package tui is the fullscreen interface (`moco` / `moco ui`): day view, week view, project
// browser and presence editing. All MOCO access goes through internal/service, like the CLI.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type view int

const (
	dayView view = iota
	weekView
	projectsView
)

var viewNames = []string{"Day", "Week", "Projects"}

// Run starts the TUI and blocks until the user quits.
func Run(ctx context.Context, svc *service.Service) error {
	_, err := tea.NewProgram(newModel(ctx, svc), tea.WithAltScreen(), tea.WithContext(ctx)).Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

type msgKind int

const (
	msgInfo msgKind = iota
	msgOK
	msgErr
	msgAlarm
)

type model struct {
	ctx    context.Context
	svc    *service.Service
	view   view
	width  int
	height int

	day      dayState
	week     weekState
	projects projectsState

	timer           *api.Activity
	pending, failed int
	offline         bool
	syncing         bool
	busy            int // writes in flight

	msg     string
	msgKind msgKind
	help    bool

	prompt  *prompt
	confirm *confirmBox
	ticks   int
}

func newModel(ctx context.Context, svc *service.Service) *model {
	now := svc.Now()
	m := &model{ctx: ctx, svc: svc, width: 80, height: 24}
	m.day.date = midnight(now)
	m.week.start = service.Monday(now)
	m.week.cursor = int(m.day.date.Sub(m.week.start).Hours() / 24)
	return m
}

func midnight(t time.Time) time.Time {
	y, mo, d := t.Date()
	return time.Date(y, mo, d, 0, 0, 0, 0, t.Location())
}

func (m *model) today() time.Time { return midnight(m.svc.Now()) }

// ---- messages ----

type tickMsg time.Time

type timerMsg struct {
	timer *api.Activity
	err   error
}

type queueMsg struct{ pending, failed int }

type syncMsg struct {
	res service.SyncResult
	err error
}

// doneMsg is the result of a write. err may be a *service.QueuedError.
type doneMsg struct {
	text string
	err  error
}

func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.loadDay(), m.loadTimer(), m.loadQueue(), m.loadProjects(false), tick())
}

func (m *model) loadTimer() tea.Cmd {
	return func() tea.Msg {
		t, err := m.svc.RunningTimer(m.ctx)
		return timerMsg{t, err}
	}
}

func (m *model) loadQueue() tea.Cmd {
	return func() tea.Msg {
		st, err := m.svc.Store.Load()
		if err != nil {
			return queueMsg{}
		}
		p, f := st.QueueCounts()
		return queueMsg{p, f}
	}
}

func (m *model) syncQueue() tea.Cmd {
	m.syncing = true
	return func() tea.Msg {
		res, err := m.svc.SyncQueue(m.ctx, false)
		return syncMsg{res, err}
	}
}

// write runs a MOCO write in the background and reports its outcome as a doneMsg.
func (m *model) write(fn func(ctx context.Context) (string, error)) tea.Cmd {
	m.busy++
	m.setMsg(msgInfo, "Saving…")
	return func() tea.Msg {
		text, err := fn(m.ctx)
		return doneMsg{text, err}
	}
}

// reload refreshes whatever the current view shows plus timer and queue.
func (m *model) reload() tea.Cmd {
	cmds := []tea.Cmd{m.loadTimer(), m.loadQueue(), m.loadDay()}
	if m.view == weekView || m.week.days != nil {
		cmds = append(cmds, m.loadWeek())
	}
	if m.view == projectsView {
		cmds = append(cmds, m.loadHours(true))
	} else {
		m.projects.hours = nil // reloaded when the view is opened again
	}
	return tea.Batch(cmds...)
}

func (m *model) setMsg(k msgKind, text string) {
	m.msgKind, m.msg = k, text
}

// noteErr shows an error and tracks whether MOCO is reachable.
func (m *model) noteErr(err error) {
	if err == nil {
		m.offline = false
		return
	}
	if api.IsUnreachable(err) {
		m.offline = true
		if m.msgKind != msgAlarm { // never hide a "not saved" warning
			m.setMsg(msgAlarm, "MOCO not reachable — showing the last loaded data. "+err.Error())
		}
		return
	}
	m.setMsg(msgErr, err.Error())
}

// ---- update ----

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		if m.prompt != nil {
			m.prompt.setWidth(m.width)
		}
		return m, nil

	case tickMsg:
		m.ticks++
		cmds := []tea.Cmd{tick(), m.loadQueue()}
		if m.pending > 0 && !m.syncing {
			cmds = append(cmds, m.syncQueue())
		}
		if m.ticks%10 == 0 { // every 5 minutes
			cmds = append(cmds, m.loadTimer(), m.loadDay())
			if m.view == weekView {
				cmds = append(cmds, m.loadWeek())
			}
		}
		return m, tea.Batch(cmds...)

	case timerMsg:
		if msg.err == nil {
			m.timer = msg.timer
		}
		return m, nil

	case queueMsg:
		m.pending, m.failed = msg.pending, msg.failed
		return m, nil

	case syncMsg:
		m.syncing = false
		if msg.err != nil {
			return m, m.loadQueue()
		}
		switch {
		case len(msg.res.Rejected) > 0:
			it := msg.res.Rejected[0]
			m.setMsg(msgAlarm, fmt.Sprintf("MOCO rejected queued #%d: %s — %s", it.ID, it.Summary(), it.LastError))
		case len(msg.res.Sent) > 0:
			m.offline = false
			m.setMsg(msgOK, fmt.Sprintf("Synced %d queued %s to MOCO.", len(msg.res.Sent), plural(len(msg.res.Sent), "entry", "entries")))
			return m, m.reload()
		}
		return m, m.loadQueue()

	case doneMsg:
		m.busy--
		var q *service.QueuedError
		switch {
		case errors.As(msg.err, &q):
			m.offline = true
			m.setMsg(msgAlarm, fmt.Sprintf("⚠ MOCO NOT REACHABLE — NOT SAVED IN MOCO YET. Queued #%d: %s", q.Item.ID, q.Item.Summary()))
		case api.IsUnreachable(msg.err):
			m.offline = true
			m.setMsg(msgAlarm, "MOCO not reachable — nothing was changed. "+msg.err.Error())
		case msg.err != nil:
			m.setMsg(msgErr, msg.err.Error())
		default:
			m.offline = false
			m.setMsg(msgOK, msg.text)
		}
		return m, m.reload()

	case dayMsg, weekMsg, projectsMsg, hoursMsg:
		return m, m.updateData(msg)

	case wizardMsg:
		return m, m.wizardDone(msg)

	case tea.KeyMsg:
		return m, m.key(msg)
	}
	return m, nil
}

func (m *model) key(msg tea.KeyMsg) tea.Cmd {
	if msg.String() == "ctrl+c" {
		return tea.Quit
	}
	if m.prompt != nil {
		cmd, done := m.prompt.update(msg)
		if done {
			m.prompt = nil
		}
		return cmd
	}
	if m.confirm != nil {
		cmd, done := m.confirm.update(msg)
		if done {
			m.confirm = nil
		}
		return cmd
	}
	if m.help {
		m.help = false
		return nil
	}
	if m.view == projectsView && m.projects.filtering {
		return m.projectsKey(msg)
	}

	switch msg.String() {
	case "q":
		return tea.Quit
	case "?":
		m.help = true
		return nil
	case "1":
		return m.switchView(dayView)
	case "2":
		return m.switchView(weekView)
	case "3":
		return m.switchView(projectsView)
	case "tab":
		return m.switchView((m.view + 1) % 3)
	case "shift+tab":
		return m.switchView((m.view + 2) % 3)
	case "r":
		m.setMsg(msgInfo, "Reloading…")
		if m.view == projectsView {
			return tea.Batch(m.reload(), m.loadProjects(true))
		}
		return m.reload()
	case "T":
		return m.timerKey()
	}
	switch m.view {
	case dayView:
		return m.dayKey(msg)
	case weekView:
		return m.weekKey(msg)
	default:
		return m.projectsKey(msg)
	}
}

func (m *model) switchView(v view) tea.Cmd {
	m.view = v
	switch v {
	case weekView:
		if m.week.days == nil {
			return m.loadWeek()
		}
	case projectsView:
		if m.projects.hours == nil {
			return m.loadHours(false)
		}
	}
	return nil
}

// ---- view ----

func (m *model) View() string {
	header := m.tabs()
	footer := m.footer()
	bodyHeight := m.height - lineCount(header) - lineCount(footer) - 1
	var body string
	switch {
	case m.help:
		body = helpText(m.width)
	case m.prompt != nil:
		body = m.prompt.view()
	case m.confirm != nil:
		body = m.confirm.view()
	case m.view == dayView:
		body = m.dayView(bodyHeight)
	case m.view == weekView:
		body = m.weekView(bodyHeight)
	default:
		body = m.projectsView(bodyHeight)
	}
	lines := strings.Split(body, "\n")
	if len(lines) > bodyHeight && bodyHeight > 0 {
		lines = lines[:bodyHeight]
	}
	for len(lines) < bodyHeight {
		lines = append(lines, "")
	}
	for i := range lines {
		lines[i] = clip(lines[i], m.width)
	}
	return header + "\n" + strings.Join(lines, "\n") + "\n" + footer
}

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

func (m *model) tabs() string {
	var parts []string
	for i, n := range viewNames {
		label := fmt.Sprintf("%d %s", i+1, n)
		if view(i) == m.view {
			parts = append(parts, sTabOn.Render(label))
		} else {
			parts = append(parts, sTab.Render(label))
		}
	}
	left := strings.Join(parts, " ")
	right := sMuted.Render("moco ")
	if m.offline {
		right = sOffline.Render("OFFLINE") + " "
	}
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(right)
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + right
}

func (m *model) footer() string {
	var status []string
	if t := m.timer; t != nil {
		sec := service.TimerSeconds(*t, m.svc.Now())
		status = append(status, sOK.Render("⏱ "+timeutil.FormatSeconds(sec))+" "+sProject.Render(t.Project.Name+" / "+t.Task.Name))
	}
	if m.pending > 0 {
		status = append(status, sAlarm.Render(fmt.Sprintf("%d queued — NOT in MOCO yet", m.pending)))
	}
	if m.failed > 0 {
		status = append(status, sAlarm.Render(fmt.Sprintf("%d rejected by MOCO (moco queue)", m.failed)))
	}
	if m.offline {
		status = append(status, sOffline.Render("MOCO not reachable"))
	}
	if m.busy > 0 {
		status = append(status, sWarn.Render("saving…"))
	}
	bar := strings.Join(status, sMuted.Render(" · "))
	if bar == "" {
		bar = sMuted.Render("no timer running")
	}

	var line string
	switch m.msgKind {
	case msgOK:
		line = sOK.Render(m.msg)
	case msgErr:
		line = sErr.Render(m.msg)
	case msgAlarm:
		line = sAlarm.Render(m.msg)
	default:
		line = sMuted.Render(m.msg)
	}
	rule := sBar.Render(strings.Repeat("─", max(0, m.width)))
	return rule + "\n" + clip(bar, m.width) + "\n" + clip(line, m.width) + "\n" + clip(m.hints(), m.width)
}

func (m *model) hints() string {
	switch {
	case m.prompt != nil, m.confirm != nil:
		return ""
	case m.help:
		return keys("any key", "close help")
	case m.view == dayView:
		return keys("←→", "day", "↑↓", "select", "a", "add", "e", "edit", "d", "delete", "n", "presence", "b", "break", "s", "stop", "?", "more", "q", "quit")
	case m.view == weekView:
		return keys("←→", "week", "↑↓", "day", "enter", "open day", "t", "this week", "?", "help", "q", "quit")
	case m.projects.filtering:
		return keys("type", "filter", "enter", "done", "esc", "clear")
	case m.projects.open != nil:
		return keys("↑↓", "select", "esc", "back", "p", "period", "/", "filter", "q", "quit")
	default:
		return keys("↑↓", "select", "enter", "tasks", "p", "period", "/", "filter", "r", "reload", "q", "quit")
	}
}

func helpText(width int) string {
	general := [][2]string{
		{"General", ""},
		{"1 2 3 / tab", "day · week · projects"},
		{"r", "reload from MOCO"},
		{"T", "timer: start (wizard) / stop"},
		{"q / ctrl+c", "quit"},
		{"", ""},
		{"Day", ""},
		{"← → / h l", "previous / next day"},
		{"t", "today"},
		{"↑ ↓ / k j", "select a presence or activity"},
		{"a", "add an activity (wizard)"},
		{"e / enter", "edit the selected entry"},
		{"d / x", "delete the selected entry"},
		{"n", "add a presence (empty end = open)"},
		{"s", "stop: close the open presence"},
		{"b", "break: split the presence"},
		{"m", "merge the presence with the next one"},
		{"o", "toggle home office / office"},
	}
	other := [][2]string{
		{"Week", ""},
		{"← → / h l", "previous / next week"},
		{"t", "this week"},
		{"enter", "open the selected day"},
		{"", ""},
		{"Projects", ""},
		{"enter / esc", "tasks / back"},
		{"p / P", "next / previous period"},
		{"/", "filter projects"},
		{"", ""},
		{"Wizard: Enter on the empty duration", ""},
		{"takes the day's unlogged time.", ""},
	}
	render := func(rows [][2]string) string {
		var b strings.Builder
		for _, r := range rows {
			switch {
			case r[0] == "":
				b.WriteString("\n")
			case r[1] == "" && strings.Contains(r[0], " "):
				b.WriteString(sMuted.Render(r[0]) + "\n")
			case r[1] == "":
				b.WriteString(sSection.Render(r[0]) + "\n")
			default:
				b.WriteString("  " + pad(sKey.Render(r[0]), 14) + r[1] + "\n")
			}
		}
		return b.String()
	}
	left, right := render(general), render(other)
	if width >= lipgloss.Width(left)+lipgloss.Width(right)+4 {
		return sTitle.Render("Keys") + "\n\n" + lipgloss.JoinHorizontal(lipgloss.Top, left, "    ", right)
	}
	return sTitle.Render("Keys") + "\n\n" + left + "\n" + right
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
