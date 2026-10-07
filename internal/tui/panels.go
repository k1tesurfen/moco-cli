package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// screen lays out the panels: left column week, presence, projects; right column the main panel
// (activities, or the tasks of the selected project) above the details panel.
func (m *model) screen(height int) string {
	leftW := clamp(m.width*38/100, 30, 48)
	if m.width-leftW < 30 {
		leftW = m.width / 2
	}
	rightW := m.width - leftW

	weekLines, weekCursor := m.weekLines()
	weekH := min(len(weekLines)+2, 9)
	presLines := m.presenceLines()
	presH := clamp(len(presLines)+2, 4, 7)
	projH := height - weekH - presH
	if projH < 3 {
		projH = 3
		presH = max(3, height-weekH-projH)
	}

	week := m.weekBox(weekLines, weekCursor)
	pres := m.presenceBox(presLines)
	proj := m.projectsBox()
	left := lipgloss.JoinVertical(lipgloss.Left, week.render(leftW, weekH), pres.render(leftW, presH), proj.render(leftW, projH))

	detailsH := clamp(height/3, 6, 12)
	mainH := height - detailsH
	var main box
	if m.focus == pProjects {
		main = m.tasksBox()
	} else {
		main = m.activitiesBox(rightW - 4)
	}
	details := box{title: "Details", lines: m.detailLines(rightW - 4), cursor: -1}
	right := lipgloss.JoinVertical(lipgloss.Left, main.render(rightW, mainH), details.render(rightW, detailsH))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// ---- week ----

// weekLines lists the visible days of the selected week and the index of the selected one.
func (m *model) weekLines() ([]string, int) {
	mon := service.Monday(m.date)
	w := m.week()
	today := m.today()
	var lines []string
	cur := 0
	for i := 0; i < 7; i++ {
		d := mon.AddDate(0, 0, i)
		if !m.visible(d) {
			continue
		}
		if d.Equal(m.date) {
			cur = len(lines)
		}
		label := pad(d.Format("Mon 2"), 7)
		if d.Equal(today) {
			label = sToday.Render(label)
		} else if d.After(today) {
			label = sMuted.Render(label)
		}
		if w == nil || w.days == nil || d.After(today) {
			lines = append(lines, label)
			continue
		}
		day, _ := service.BuildDay(w.days[i].Date, w.days[i].Presences, w.days[i].Activities, m.svc.Now())
		lines = append(lines, label+padLeft(dashIfZero(day.PresentSeconds), 6)+padLeft(dashIfZero(day.LoggedSeconds), 7)+"  "+m.dayStatus(day, w.paused[day.Date]))
	}
	return lines, cur
}

// dayStatus is a short verdict for a day: ✓, missing time, over, open, no presence, off.
func (m *model) dayStatus(d service.Day, paused bool) string {
	today := timeutil.Date(m.svc.Now())
	gap := d.Gap()
	switch {
	case paused && len(d.Presences) == 0:
		return sMuted.Render("off")
	case len(d.Presences) == 0 && len(d.Activities) == 0:
		if d.Date < today {
			return sWarn.Render("empty")
		}
		return ""
	case len(d.Presences) == 0:
		return sWarn.Render("no presence")
	case d.OpenPresence != nil && d.Date != today:
		return sErr.Render("open")
	case gap > 0:
		return sErr.Render("-" + timeutil.FormatSeconds(gap))
	case gap < 0:
		return sWarn.Render("+" + timeutil.FormatSeconds(-gap))
	}
	return sOK.Render("✓")
}

func (m *model) weekBox(lines []string, cursor int) box {
	mon := service.Monday(m.date)
	b := box{
		title:   fmt.Sprintf("[1] Week %d · %s–%s", isoWeek(mon), mon.Format("2"), mon.AddDate(0, 0, 6).Format("2 Jan")),
		lines:   lines,
		cursor:  cursor,
		focused: m.focus == pWeek,
		offset:  &m.offsets[pWeek],
	}
	w := m.week()
	switch {
	case w == nil || (w.days == nil && !w.loading):
		b.footer = "…"
	case w.loading:
		b.footer = "loading…"
	default:
		present, logged := 0, 0
		for _, d := range w.days {
			day, _ := service.BuildDay(d.Date, d.Presences, d.Activities, m.svc.Now())
			present += day.PresentSeconds
			logged += day.LoggedSeconds
		}
		b.footer = "present " + timeutil.FormatSeconds(present) + " · logged " + timeutil.FormatSeconds(logged)
	}
	return b
}

// ---- presence ----

func (m *model) presenceLines() []string {
	d := m.selDay()
	if d == nil {
		return nil
	}
	now := m.svc.Now()
	var lines []string
	for _, p := range d.Presences {
		dur := timeutil.FormatSeconds(service.PresenceSeconds(p, now))
		note := ""
		if p.To == "" {
			if p.Date == timeutil.Date(now) {
				note = sOK.Render("running")
			} else {
				dur, note = "", sErr.Render("not closed")
			}
		}
		lines = append(lines, pad(span(p), 12)+padLeft(dur, 6)+"  "+note)
	}
	return lines
}

func (m *model) presenceBox(lines []string) box {
	b := box{title: "[2] Presence", lines: lines, cursor: m.presCursor, focused: m.focus == pPresence, offset: &m.offsets[pPresence]}
	d := m.selDay()
	switch {
	case d == nil:
		b.lines, b.cursor = []string{sMuted.Render("loading…")}, -1
	case len(d.Presences) == 0:
		b.lines, b.cursor = []string{sMuted.Render("none — n adds one")}, -1
	default:
		b.title += " · " + where(d.Presences[0].IsHomeOffice)
		b.footer = timeutil.FormatSeconds(d.PresentSeconds)
	}
	return b
}

// ---- activities ----

func (m *model) activitiesBox(iw int) box {
	b := box{
		title:   "[3] Activities · " + m.date.Format("Mon 2 Jan"),
		cursor:  m.actCursor,
		focused: m.focus == pActivities,
		offset:  &m.offsets[pActivities],
	}
	if m.date.Equal(m.today()) {
		b.title += " · today"
	}
	d := m.selDay()
	if d == nil {
		b.lines, b.cursor = []string{sMuted.Render("loading…")}, -1
		return b
	}
	if len(d.Activities) == 0 {
		b.lines, b.cursor = []string{sMuted.Render("nothing logged — a adds an activity")}, -1
	}
	now := m.svc.Now()
	nameW := 0
	for _, a := range d.Activities {
		nameW = max(nameW, lipgloss.Width(a.Project.Name+" / "+a.Task.Name))
	}
	nameW = min(nameW, max(16, (iw-8)*60/100))
	for _, a := range d.Activities {
		dur := timeutil.FormatSeconds(service.TimerSeconds(a, now))
		if a.TimerRunning() {
			dur = sOK.Render("⏱" + dur)
		}
		desc := oneLine(a.Description)
		if !service.HasDescription(a) {
			desc = sMuted.Render("(no description yet)")
		}
		name := pad(pairLabel(a.Project.Name, a.Task.Name, nameW), nameW)
		b.lines = append(b.lines, padLeft(dur, 6)+"  "+sProject.Render(name)+"  "+desc)
	}
	foot := timeutil.FormatSeconds(d.LoggedSeconds) + " logged"
	switch g := d.Gap(); {
	case len(d.Presences) == 0:
	case g > 0:
		foot += " · " + sErr.Render(timeutil.FormatSeconds(g)+" missing")
	case g < 0:
		foot += " · " + sWarn.Render(timeutil.FormatSeconds(-g)+" over")
	default:
		foot += " · " + sOK.Render("complete ✓")
	}
	b.footer = foot
	return b
}

// ---- details ----

func (m *model) detailLines(w int) []string {
	label := func(k, v string) string { return sMuted.Render(pad(k, 10)) + v }
	wrap := func(s string) []string { return strings.Split(ansi.Wordwrap(s, max(10, w), " "), "\n") }
	now := m.svc.Now()
	d := m.selDay()

	switch m.focus {
	case pProjects:
		p := m.selProject()
		if p == nil {
			return []string{sMuted.Render("no project selected")}
		}
		lines := []string{sProject.Render(p.Name), label("Customer", p.Customer.Name), label("Number", p.Identifier)}
		if h := m.hours(); h != nil {
			lines = append(lines, label(periodNames[m.projects.period], timeutil.FormatSeconds(h.ByProject[p.ID])+
				sMuted.Render(" of "+timeutil.FormatSeconds(h.Total)+" in total")))
		}
		return append(lines, label("Tasks", fmt.Sprint(len(service.ActiveTasks(*p)))+" active"))

	case pActivities:
		if a := m.selActivity(); a != nil {
			lines := []string{
				sProject.Render(a.Project.Name + " / " + a.Task.Name),
				label("Customer", a.Customer.Name),
				label("Duration", timeutil.FormatSeconds(service.TimerSeconds(*a, now))+sMuted.Render(" · "+a.Date+" · #"+fmt.Sprint(a.ID))),
			}
			if a.TimerRunning() {
				lines = append(lines, label("Timer", sOK.Render("running since "+a.TimerStartedAt.In(now.Location()).Format("15:04"))))
			}
			lines = append(lines, "")
			if service.HasDescription(*a) {
				return append(lines, wrap(a.Description)...)
			}
			return append(lines, sMuted.Render("(no description yet)"))
		}

	case pPresence:
		if p := m.selPresence(); p != nil {
			state := "closed"
			if p.To == "" {
				state = sOK.Render("open")
			}
			return []string{
				sTitle.Render(span(*p)) + sMuted.Render(" · "+m.date.Format("Mon 2 Jan")),
				label("Duration", timeutil.FormatSeconds(service.PresenceSeconds(*p, now))),
				label("Location", where(p.IsHomeOffice)+sMuted.Render(" (whole day · o toggles)")),
				label("State", state),
				label("ID", sMuted.Render(fmt.Sprint(p.ID))),
			}
		}
	}

	// Week focus, or nothing selected: the day's summary.
	title := sTitle.Render(m.date.Format("Monday, 2 January 2006"))
	if d == nil {
		return []string{title, sMuted.Render("loading…")}
	}
	lines := []string{title}
	if w := m.week(); w != nil && w.paused[d.Date] {
		lines = append(lines, sWarn.Render("paused (day off)"))
	}
	var spans []string
	for _, p := range d.Presences {
		spans = append(spans, span(p))
	}
	if len(spans) == 0 {
		spans = []string{sMuted.Render("none")}
	}
	lines = append(lines,
		label("Presence", strings.Join(spans, ", ")),
		label("Present", timeutil.FormatSeconds(d.PresentSeconds)),
		label("Logged", timeutil.FormatSeconds(d.LoggedSeconds)+sMuted.Render(fmt.Sprintf(" in %d %s", len(d.Activities), plural(len(d.Activities), "activity", "activities")))),
	)
	if len(d.Presences) > 0 {
		lines = append(lines, label("Status", m.dayStatus(*d, false)))
	}
	return lines
}

// ---- selection helpers ----

func (m *model) selPresence() *api.Presence {
	d := m.selDay()
	if d == nil || m.presCursor >= len(d.Presences) {
		return nil
	}
	return &d.Presences[m.presCursor]
}

func (m *model) selActivity() *api.Activity {
	d := m.selDay()
	if d == nil || m.actCursor >= len(d.Activities) {
		return nil
	}
	return &d.Activities[m.actCursor]
}

// pairLabel renders "project / task" in w cells, shortening the project first so the task stays
// readable.
func pairLabel(project, task string, w int) string {
	full := project + " / " + task
	if lipgloss.Width(full) <= w {
		return full
	}
	if room := w - lipgloss.Width(task) - 3; room >= 8 {
		return clip(project, room) + " / " + task
	}
	return clip(full, w)
}

func span(p api.Presence) string {
	to := p.To
	if to == "" {
		to = "…"
	}
	return p.From + "–" + to
}

func where(home bool) string {
	if home {
		return "home office"
	}
	return "office"
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
