// Package tui is the fullscreen interface (`moco` / `moco ui`) in the style of lazygit: one
// screen of bordered panels — week, presence, projects on the left; activities (or tasks) and
// details on the right. All MOCO access goes through internal/service, like the CLI.
package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type panel int

const (
	pWeek panel = iota
	pPresence
	pActivities
	pProjects
	panelCount
)

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
	width  int
	height int

	focus      panel
	date       time.Time // selected day
	weeks      map[string]*weekCache
	weekSeq    int
	hoursSeq   int
	presCursor int
	actCursor  int
	offsets    [panelCount]int
	projects   projectsState

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
	m := &model{ctx: ctx, svc: svc, width: 100, height: 30, focus: pActivities, weeks: map[string]*weekCache{}}
	m.date = midnight(svc.Now())
	m.projects.hours = map[string]*service.Hours{}
	m.projects.loading = map[string]bool{}
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

// doneMsg is the result of a write on date. err may be a *service.QueuedError.
type doneMsg struct {
	date time.Time
	text string
	err  error
}

func tick() tea.Cmd {
	return tea.Tick(30*time.Second, func(t time.Time) tea.Msg { return tickMsg(t) })
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(m.ensureWeek(false), m.loadTimer(), m.loadQueue(), m.loadProjects(false), m.ensureHours(false), tick())
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

// write runs a MOCO write for the selected day in the background and reports a doneMsg.
func (m *model) write(fn func(ctx context.Context) (string, error)) tea.Cmd {
	m.busy++
	m.setMsg(msgInfo, "Saving…")
	date := m.date
	return func() tea.Msg {
		text, err := fn(m.ctx)
		return doneMsg{date, text, err}
	}
}

// refresh drops cached data of date's week and the project hours, then reloads what is shown.
func (m *model) refresh(date time.Time) tea.Cmd {
	m.invalidate(date)
	m.projects.hours = map[string]*service.Hours{}
	m.projects.loading = map[string]bool{}
	return tea.Batch(m.loadTimer(), m.loadQueue(), m.ensureWeek(false), m.ensureHours(false))
}

func (m *model) setMsg(k msgKind, text string) {
	m.msgKind, m.msg = k, text
}

// noteErr shows a load error and tracks whether MOCO is reachable.
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
			m.prompt.setWidth(m.popupWidth())
		}
		return m, nil

	case tickMsg:
		m.ticks++
		cmds := []tea.Cmd{tick(), m.loadQueue()}
		if m.pending > 0 && !m.syncing {
			cmds = append(cmds, m.syncQueue())
		}
		if m.ticks%10 == 0 { // every 5 minutes; the week reloads once its cache is stale
			cmds = append(cmds, m.loadTimer(), m.ensureWeek(false))
		}
		return m, tea.Batch(cmds...)

	case debounceMsg:
		switch {
		case msg.kind == debounceWeek && msg.seq == m.weekSeq:
			return m, m.ensureWeek(false)
		case msg.kind == debounceHours && msg.seq == m.hoursSeq:
			return m, m.ensureHours(false)
		}
		return m, nil

	case weekMsg:
		return m, m.weekLoaded(msg)

	case projectsMsg:
		m.noteErr(msg.err)
		if msg.err == nil {
			m.projects.list = msg.projects
		}
		return m, nil

	case hoursMsg:
		m.hoursLoaded(msg)
		return m, nil

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
			for _, it := range msg.res.Sent {
				if d, err := time.ParseInLocation(timeutil.DateLayout, it.Date, m.svc.Now().Location()); err == nil {
					m.invalidate(d)
				}
			}
			return m, m.refresh(m.date)
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
		return m, m.refresh(msg.date)

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
	if m.focus == pProjects && m.projects.filtering {
		return m.projectsKey(msg)
	}

	switch msg.String() {
	case "q":
		return tea.Quit
	case "?":
		m.help = true
		return nil
	case "1", "2", "3", "4":
		m.focus = panel(msg.String()[0] - '1')
		return nil
	case "tab":
		m.focus = (m.focus + 1) % panelCount
		return nil
	case "shift+tab":
		m.focus = (m.focus + panelCount - 1) % panelCount
		return nil
	case "esc":
		if m.focus != pProjects || m.projects.filter == "" {
			m.focus = pActivities
			return nil
		}
	case "r":
		m.setMsg(msgInfo, "Reloading…")
		cmds := []tea.Cmd{m.refresh(m.date)}
		if m.focus == pProjects {
			cmds = append(cmds, m.loadProjects(true))
		}
		return tea.Batch(cmds...)
	case "T":
		return m.timerKey()
	}
	if m.focus == pProjects {
		return m.projectsKey(msg)
	}
	return m.dayKey(msg)
}

// dayKey handles the keys of the week, presence and activities panels.
func (m *model) dayKey(msg tea.KeyMsg) tea.Cmd {
	d := m.selDay()
	switch msg.String() {
	case "left", "h":
		return m.stepDay(-1)
	case "right", "l":
		return m.stepDay(1)
	case "H", "[":
		return m.selectDate(m.date.AddDate(0, 0, -7))
	case "L", "]":
		return m.selectDate(m.date.AddDate(0, 0, 7))
	case "t":
		return m.selectDate(m.today())
	case "up", "k":
		switch m.focus {
		case pWeek:
			return m.stepDay(-1)
		case pPresence:
			m.presCursor = max(0, m.presCursor-1)
		case pActivities:
			m.actCursor = max(0, m.actCursor-1)
		}
	case "down", "j":
		switch {
		case m.focus == pWeek:
			return m.stepDay(1)
		case m.focus == pPresence && d != nil:
			m.presCursor = clamp(m.presCursor+1, 0, len(d.Presences)-1)
		case m.focus == pActivities && d != nil:
			m.actCursor = clamp(m.actCursor+1, 0, len(d.Activities)-1)
		}
	case "home", "g":
		m.presCursor, m.actCursor = 0, 0
	case "end", "G":
		if d != nil {
			m.presCursor, m.actCursor = max(0, len(d.Presences)-1), max(0, len(d.Activities)-1)
		}
	case "enter":
		if m.focus == pWeek {
			m.focus = pActivities
			return nil
		}
		return m.editSelected()
	case "e":
		return m.editSelected()
	case "a":
		return m.addActivity()
	case "d", "x":
		m.deleteSelected()
	case "n":
		m.newPresence()
	case "s":
		m.stopPresence()
	case "b":
		m.breakPresence()
	case "m":
		m.mergePresence()
	case "o":
		return m.toggleLocation()
	}
	return nil
}

// ---- view ----

func (m *model) View() string {
	footer := m.footer()
	height := m.height - lineCount(footer)
	var screen string
	if height >= 8 && m.width >= 40 {
		screen = m.screen(height)
	} else {
		screen = sMuted.Render("Window too small.")
	}
	lines := strings.Split(screen, "\n")
	for len(lines) < height {
		lines = append(lines, "")
	}
	screen = strings.Join(lines[:max(0, height)], "\n")

	popup := func(title, foot string, lines []string, w, h int) {
		screen = overlay(screen, box{title: title, footer: foot, lines: lines, cursor: -1, focused: true}.render(w, h), m.width)
	}
	switch {
	case m.help:
		hl := helpLines()
		popup("Keys", "any key closes", hl, min(m.width, 92), min(height, len(hl)+2))
	case m.prompt != nil:
		pl := m.prompt.lines()
		popup(m.prompt.title, "enter save · tab next · esc cancel", pl, m.popupWidth(), len(pl)+2)
	case m.confirm != nil:
		cl := m.confirm.lines(m.popupWidth() - 4)
		popup(m.confirm.question, "y yes · n no", cl, m.popupWidth(), len(cl)+2)
	}
	return screen + "\n" + footer
}

func (m *model) popupWidth() int { return clamp(m.width-8, 30, 72) }

func lineCount(s string) int { return strings.Count(s, "\n") + 1 }

// footer is the status line (timer, queue, connection, last message) and the key hints.
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
		status = append(status, sOffline.Render("OFFLINE"))
	}
	if m.busy > 0 {
		status = append(status, sWarn.Render("saving…"))
	}
	switch m.msgKind {
	case msgOK:
		status = append(status, sOK.Render(m.msg))
	case msgErr:
		status = append(status, sErr.Render(m.msg))
	case msgAlarm:
		status = append(status, sAlarm.Render(m.msg))
	default:
		if m.msg != "" {
			status = append(status, sMuted.Render(m.msg))
		}
	}
	line := " " + strings.Join(status, sMuted.Render(" · "))
	return clip(line, m.width) + "\n" + clip(" "+m.hints(), m.width)
}

func (m *model) hints() string {
	switch {
	case m.prompt != nil, m.confirm != nil:
		return ""
	case m.help:
		return keys("any key", "close help")
	case m.focus == pProjects && m.projects.filtering:
		return keys("type", "filter", "enter", "keep", "esc", "clear")
	case m.focus == pProjects:
		return keys("↑↓", "project", "p/P", "period", "/", "filter", "tab", "panel", "?", "help", "q", "quit")
	case m.focus == pPresence:
		return keys("←→", "day", "↑↓", "select", "e", "edit", "n", "new", "s", "stop", "b", "break", "m", "merge", "o", "home/office", "d", "delete", "?", "help")
	case m.focus == pWeek:
		return keys("↑↓/←→", "day", "H/L", "week", "t", "today", "enter", "activities", "a", "add", "tab", "panel", "?", "help", "q", "quit")
	}
	return keys("←→", "day", "↑↓", "select", "a", "add", "e", "edit", "d", "delete", "T", "timer", "tab", "panel", "?", "help", "q", "quit")
}

func helpLines() []string {
	rows := [][2]string{
		{"Panels", ""},
		{"1 2 3 4 · tab", "week · presence · activities · projects"},
		{"esc", "back to activities"},
		{"r", "reload from MOCO"},
		{"T", "timer: start (wizard) / stop"},
		{"q · ctrl+c", "quit"},
		{"Days", ""},
		{"← → · h l", "previous / next day (also ↑↓ in the week panel)"},
		{"H L · [ ]", "previous / next week"},
		{"t", "today"},
		{"Entries", ""},
		{"a", "add an activity (wizard; Enter on the empty duration = unlogged time)"},
		{"e · enter", "edit the selected activity or presence"},
		{"d · x", "delete the selected activity or presence"},
		{"n s b", "new presence (empty end = open) · stop · break"},
		{"m", "merge the presence with the next one (removes the break)"},
		{"o", "toggle home office / office for the whole day"},
		{"Projects", ""},
		{"p P", "next / previous period"},
		{"/ · esc", "filter · clear filter"},
	}
	var lines []string
	for _, r := range rows {
		if r[1] == "" {
			if len(lines) > 0 {
				lines = append(lines, "")
			}
			lines = append(lines, sSection.Render(r[0]))
			continue
		}
		lines = append(lines, "  "+pad(sKey.Render(r[0]), 16)+r[1])
	}
	return lines
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
