package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type weekState struct {
	start   time.Time // Monday
	days    []service.Day
	paused  map[string]bool
	loading bool
	cursor  int // 0 = Monday
}

type weekMsg struct {
	start  string
	days   []service.Day
	paused map[string]bool
	err    error
}

func (m *model) loadWeek() tea.Cmd {
	start := m.week.start
	m.week.loading = true
	return func() tea.Msg {
		days, err := m.svc.Days(m.ctx, start, start.AddDate(0, 0, 6))
		paused := map[string]bool{}
		if st, err := m.svc.Store.Load(); err == nil {
			for i := 0; i < 7; i++ {
				ds := timeutil.Date(start.AddDate(0, 0, i))
				paused[ds] = st.Paused(ds)
			}
		}
		return weekMsg{timeutil.Date(start), days, paused, err}
	}
}

func (m *model) weekLoaded(msg weekMsg) {
	if msg.start != timeutil.Date(m.week.start) {
		return
	}
	m.week.loading = false
	m.noteErr(msg.err)
	if msg.err == nil {
		m.week.days, m.week.paused = msg.days, msg.paused
		if !m.weekVisible(m.week.cursor) {
			m.week.cursor = m.weekStep(m.week.cursor, -1)
		}
	}
}

// weekVisible: workdays are always shown, other days only if something is recorded.
func (m *model) weekVisible(i int) bool {
	d := m.week.start.AddDate(0, 0, i)
	if m.svc.Cfg.IsWorkday(d) {
		return true
	}
	if i < len(m.week.days) {
		day := m.week.days[i]
		return len(day.Presences) > 0 || len(day.Activities) > 0
	}
	return false
}

// weekStep moves from i in direction dir to the next visible day (or stays).
func (m *model) weekStep(i, dir int) int {
	for j := i + dir; j >= 0 && j < 7; j += dir {
		if m.weekVisible(j) {
			return j
		}
	}
	if m.weekVisible(i) {
		return i
	}
	for j := 0; j < 7; j++ {
		if m.weekVisible(j) {
			return j
		}
	}
	return 0
}

func (m *model) setWeek(start time.Time) tea.Cmd {
	if start.After(m.today()) {
		m.setMsg(msgInfo, "That week is in the future.")
		return nil
	}
	if !start.Equal(m.week.start) {
		m.week = weekState{start: start, cursor: m.week.cursor}
	}
	return m.loadWeek()
}

func (m *model) weekKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "left", "h":
		return m.setWeek(m.week.start.AddDate(0, 0, -7))
	case "right", "l":
		return m.setWeek(m.week.start.AddDate(0, 0, 7))
	case "t":
		m.week.cursor = int(m.today().Sub(service.Monday(m.today())).Hours() / 24)
		return m.setWeek(service.Monday(m.today()))
	case "up", "k":
		m.week.cursor = m.weekStep(m.week.cursor, -1)
	case "down", "j":
		m.week.cursor = m.weekStep(m.week.cursor, 1)
	case "enter":
		d := m.week.start.AddDate(0, 0, m.week.cursor)
		if d.After(m.today()) {
			m.setMsg(msgInfo, "That day is in the future.")
			return nil
		}
		m.view = dayView
		return m.setDay(d)
	}
	return nil
}

func (m *model) weekView(height int) string {
	var b strings.Builder
	end := m.week.start.AddDate(0, 0, 6)
	title := fmt.Sprintf("Week %d · %s – %s", isoWeek(m.week.start), m.week.start.Format("2 Jan"), end.Format("2 Jan 2006"))
	b.WriteString(sTitle.Render(title))
	if m.week.start.Equal(service.Monday(m.today())) {
		b.WriteString(sMuted.Render(" · this week"))
	}
	b.WriteString("\n\n")
	if m.week.days == nil {
		if m.week.loading {
			b.WriteString(sMuted.Render("Loading…"))
		}
		return b.String()
	}

	b.WriteString(sMuted.Render(fmt.Sprintf("  %-11s %-26s %8s %8s  %s", "", "Presence", "Present", "Logged", "")) + "\n")
	now := m.svc.Now()
	today := timeutil.Date(now)
	present, logged, missing := 0, 0, 0
	for i, d := range m.week.days {
		if !m.weekVisible(i) {
			continue
		}
		date := m.week.start.AddDate(0, 0, i)
		var spans []string
		for _, p := range d.Presences {
			spans = append(spans, span(p))
		}
		spanText := strings.Join(spans, ", ")
		if spanText == "" {
			spanText = "—"
		}
		var status string
		gap := d.Gap()
		switch {
		case d.Date > today:
			status = ""
		case m.week.paused[d.Date] && len(d.Presences) == 0:
			status = sMuted.Render("day off")
		case len(d.Presences) == 0 && len(d.Activities) == 0:
			if m.svc.Cfg.IsWorkday(date) && d.Date < today {
				status = sWarn.Render("nothing recorded")
			}
		case len(d.Presences) == 0:
			status = sWarn.Render("no presence")
		case d.OpenPresence != nil && d.Date != today:
			status = sErr.Render("presence not closed")
		case gap > 0:
			status = sErr.Render(timeutil.FormatSeconds(gap) + " missing")
			missing += gap
		case gap < 0:
			status = sWarn.Render(timeutil.FormatSeconds(-gap) + " over")
		default:
			status = sOK.Render("✓")
		}
		if d.Date == today && status != "" {
			status += sMuted.Render(" (so far)")
		}
		present += d.PresentSeconds
		logged += d.LoggedSeconds

		on := i == m.week.cursor
		label := date.Format("Mon 2 Jan")
		row := fmt.Sprintf("%-11s %s %8s %8s  ", label, pad(clip(spanText, 26), 26),
			dashIfZero(d.PresentSeconds), dashIfZero(d.LoggedSeconds))
		if on {
			row = sSelected.Render(row)
		} else if d.Date > today {
			row = sMuted.Render(row)
		}
		b.WriteString(cursor(on) + row + status + "\n")
	}
	b.WriteString("\n")
	total := fmt.Sprintf("  %-11s %-26s %8s %8s  ", "Total", "", timeutil.FormatSeconds(present), timeutil.FormatSeconds(logged))
	b.WriteString(sSection.Render(total))
	if missing > 0 {
		b.WriteString(sErr.Render(timeutil.FormatSeconds(missing) + " missing"))
	}
	b.WriteString("\n")
	return b.String()
}

func dashIfZero(sec int) string {
	if sec == 0 {
		return "—"
	}
	return timeutil.FormatSeconds(sec)
}

func isoWeek(t time.Time) int {
	_, w := t.ISOWeek()
	return w
}
