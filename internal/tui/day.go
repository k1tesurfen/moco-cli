package tui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

type dayState struct {
	date    time.Time
	data    *service.Day // nil until loaded
	paused  bool
	loading bool
	cursor  int // over presences, then activities
}

type dayMsg struct {
	date   string
	day    service.Day
	paused bool
	err    error
}

func (m *model) loadDay() tea.Cmd {
	date := m.day.date
	m.day.loading = true
	return func() tea.Msg {
		d, err := m.svc.Day(m.ctx, date)
		paused := false
		if st, err := m.svc.Store.Load(); err == nil {
			paused = st.Paused(timeutil.Date(date))
		}
		return dayMsg{timeutil.Date(date), d, paused, err}
	}
}

// setDay selects another day and loads it.
func (m *model) setDay(d time.Time) tea.Cmd {
	if d.After(m.today()) {
		m.setMsg(msgInfo, "That's in the future.")
		return nil
	}
	if !d.Equal(m.day.date) {
		m.day = dayState{date: d}
	}
	return m.loadDay()
}

func (m *model) updateData(msg tea.Msg) tea.Cmd {
	switch msg := msg.(type) {
	case dayMsg:
		if msg.date != timeutil.Date(m.day.date) {
			return nil // answer for a day that is no longer shown
		}
		m.day.loading = false
		m.noteErr(msg.err)
		if msg.err == nil {
			m.day.data, m.day.paused = &msg.day, msg.paused
			m.day.cursor = min(m.day.cursor, max(0, m.dayItems()-1))
		}
	case weekMsg:
		m.weekLoaded(msg)
	case projectsMsg:
		m.noteErr(msg.err)
		if msg.err == nil {
			m.projects.list = msg.projects
		}
	case hoursMsg:
		m.hoursLoaded(msg)
	}
	return nil
}

func (m *model) dayItems() int {
	if m.day.data == nil {
		return 0
	}
	return len(m.day.data.Presences) + len(m.day.data.Activities)
}

// selected returns the presence or activity under the cursor.
func (m *model) selected() (*api.Presence, *api.Activity) {
	d := m.day.data
	if d == nil || m.dayItems() == 0 {
		return nil, nil
	}
	if i := m.day.cursor; i < len(d.Presences) {
		return &d.Presences[i], nil
	}
	return nil, &d.Activities[m.day.cursor-len(d.Presences)]
}

func (m *model) dayKey(msg tea.KeyMsg) tea.Cmd {
	date := m.day.date
	switch msg.String() {
	case "left", "h":
		return m.setDay(date.AddDate(0, 0, -1))
	case "right", "l":
		return m.setDay(date.AddDate(0, 0, 1))
	case "t":
		return m.setDay(m.today())
	case "up", "k":
		if m.day.cursor > 0 {
			m.day.cursor--
		}
	case "down", "j":
		if m.day.cursor < m.dayItems()-1 {
			m.day.cursor++
		}
	case "home", "g":
		m.day.cursor = 0
	case "end", "G":
		m.day.cursor = max(0, m.dayItems()-1)
	case "a":
		return m.addActivity()
	case "e", "enter":
		p, a := m.selected()
		switch {
		case p != nil:
			m.editPresence(*p)
		case a != nil:
			return m.editActivity(*a)
		}
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

// ---- presence editing ----

func (m *model) openPrompt(p *prompt) {
	p.setWidth(m.width)
	m.prompt = p
}

func (m *model) dayLabel() string { return m.day.date.Format("Mon 2 Jan") }

func (m *model) isToday() bool { return m.day.date.Equal(m.today()) }

func (m *model) editPresence(p api.Presence) {
	date := m.day.date
	pr := newPrompt("Edit presence "+span(p)+" · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		from, err := timeutil.NormalizeClock(v[0])
		if err != nil {
			return nil, err
		}
		to := ""
		if v[1] != "" {
			if to, err = timeutil.NormalizeClock(v[1]); err != nil {
				return nil, err
			}
		}
		switch {
		case to == "" && p.To != "":
			return nil, fmt.Errorf("MOCO can't reopen a closed presence — delete it and add an open one (n)")
		case to != "" && to <= from:
			return nil, fmt.Errorf("end %s is not after the start %s", to, from)
		case from == p.From && to == p.To:
			return nil, fmt.Errorf("nothing changed")
		}
		if from == p.From {
			from = ""
		}
		if to == p.To {
			to = ""
		}
		return m.write(func(ctx context.Context) (string, error) {
			np, err := m.svc.EditPresence(ctx, p.ID, from, to, nil)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Presence is now %s on %s.", span(np), date.Format("Mon 2 Jan")), nil
		}), nil
	})
	hint := ""
	if p.To == "" {
		hint = "empty = still open"
	}
	pr.add("From", p.From, "HH:MM").add("To", p.To, hint)
	m.openPrompt(pr)
}

func (m *model) newPresence() {
	date := m.day.date
	from := ""
	if m.isToday() && m.day.data != nil && len(m.day.data.Presences) == 0 {
		from = timeutil.Clock(m.svc.Now())
	}
	pr := newPrompt("Add presence · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		if v[0] == "" {
			return nil, fmt.Errorf("a start time is required")
		}
		from, to := v[0], v[1]
		return m.write(func(ctx context.Context) (string, error) {
			p, err := m.svc.AddPresence(ctx, date, from, to)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Added presence %s on %s.", span(p), date.Format("Mon 2 Jan")), nil
		}), nil
	})
	pr.add("From", from, "HH:MM").add("To", "", "empty = open (still working)")
	m.openPrompt(pr)
}

func (m *model) stopPresence() {
	d := m.day.data
	if d == nil || d.OpenPresence == nil {
		m.setMsg(msgErr, "No open presence on "+m.dayLabel()+" — nothing to stop.")
		return
	}
	date := m.day.date
	to := ""
	if m.isToday() {
		to = timeutil.Clock(m.svc.Now())
	}
	timerNote := m.timer != nil && m.timer.Date == timeutil.Date(date)
	pr := newPrompt("Stop "+span(*d.OpenPresence)+" · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		to := v[0]
		return m.write(func(ctx context.Context) (string, error) {
			p, err := m.svc.Stop(ctx, date, to)
			if err != nil {
				return "", err
			}
			text := fmt.Sprintf("Stopped at %s (%s).", p.To, span(p))
			if timerNote {
				text += " A timer is still running — T stops it."
			}
			return text, nil
		}), nil
	})
	pr.add("End", to, "HH:MM")
	if timerNote {
		pr.note = "A timer is running; it is not stopped with the presence."
	}
	m.openPrompt(pr)
}

func (m *model) breakPresence() {
	date := m.day.date
	cfg := m.svc.Cfg.Schedule
	pr := newPrompt("Break · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		from, to := v[0], v[1]
		return m.write(func(ctx context.Context) (string, error) {
			res, err := m.svc.Break(ctx, date, from, to)
			if err != nil {
				return "", err
			}
			if res.After == nil {
				return fmt.Sprintf("Presence now ends at %s.", res.Before.To), nil
			}
			return fmt.Sprintf("Break recorded: %s and %s.", span(res.Before), span(*res.After)), nil
		}), nil
	})
	pr.note = "Splits the presence covering the break into two."
	pr.add("Break from", cfg.BreakFrom, "HH:MM").add("Break to", cfg.BreakTo, "HH:MM")
	m.openPrompt(pr)
}

func (m *model) mergePresence() {
	p, _ := m.selected()
	d := m.day.data
	if p == nil {
		m.setMsg(msgErr, "Select a presence to merge with the next one.")
		return
	}
	var next *api.Presence
	for i := range d.Presences {
		if d.Presences[i].ID == p.ID && i+1 < len(d.Presences) {
			next = &d.Presences[i+1]
		}
	}
	if next == nil {
		m.setMsg(msgErr, span(*p)+" is the last presence of the day — nothing to merge it with.")
		return
	}
	date, id := m.day.date, p.ID
	m.confirm = &confirmBox{
		question: fmt.Sprintf("Merge %s with %s?", span(*p), span(*next)),
		detail:   fmt.Sprintf("Removes the break %s–%s. %s is deleted and %s starts at %s.", p.To, next.From, span(*p), span(*next), p.From),
		yes: func() tea.Cmd {
			return m.write(func(ctx context.Context) (string, error) {
				np, err := m.svc.MergePresences(ctx, date, id)
				if err != nil {
					return "", err
				}
				return "Merged: " + span(np) + ".", nil
			})
		},
	}
}

func (m *model) toggleLocation() tea.Cmd {
	d := m.day.data
	if d == nil || len(d.Presences) == 0 {
		m.setMsg(msgErr, "No presence on "+m.dayLabel()+" — the location is stored with the presences.")
		return nil
	}
	home := !d.Presences[0].IsHomeOffice
	date := m.day.date
	return m.write(func(ctx context.Context) (string, error) {
		if err := m.svc.SetDayLocation(ctx, date, home); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s for the whole day.", date.Format("Mon 2 Jan"), where(home)), nil
	})
}

func (m *model) deleteSelected() {
	p, a := m.selected()
	switch {
	case p != nil:
		id, label := p.ID, span(*p)
		m.confirm = &confirmBox{
			question: fmt.Sprintf("Delete presence %s on %s?", label, m.dayLabel()),
			yes: func() tea.Cmd {
				return m.write(func(ctx context.Context) (string, error) {
					if err := m.svc.DeletePresence(ctx, id); err != nil {
						return "", err
					}
					return "Deleted presence " + label + ".", nil
				})
			},
		}
	case a != nil:
		if a.TimerRunning() {
			m.setMsg(msgErr, "This activity has a running timer — stop it first (T), or `moco timer cancel`.")
			return
		}
		act := *a
		summary := fmt.Sprintf("%s · %s / %s · %s", timeutil.FormatSeconds(act.Seconds), act.Project.Name, act.Task.Name, oneLine(act.Description))
		m.confirm = &confirmBox{
			question: "Delete this activity?",
			detail:   summary,
			yes: func() tea.Cmd {
				return m.write(func(ctx context.Context) (string, error) {
					if _, err := m.svc.DeleteActivity(ctx, act.ID); err != nil {
						return "", err
					}
					return "Deleted " + summary, nil
				})
			},
		}
	}
}

// ---- rendering ----

func (m *model) dayView(height int) string {
	var lines []string
	cursorLine := 0
	add := func(s string) { lines = append(lines, s) }

	title := sTitle.Render(m.day.date.Format("Monday, 2 January 2006"))
	switch {
	case m.isToday():
		title += sMuted.Render(" · today")
	case !m.svc.Cfg.IsWorkday(m.day.date):
		title += sMuted.Render(" · weekend")
	}
	if m.day.paused {
		title += sWarn.Render(" · paused (day off)")
	}
	d := m.day.data
	if d != nil && len(d.Presences) > 0 {
		title += sMuted.Render(" · ") + sProject.Render(where(d.Presences[0].IsHomeOffice))
	}
	add(title)
	add("")
	if d == nil {
		if m.day.loading {
			add(sMuted.Render("Loading…"))
		}
		return strings.Join(lines, "\n")
	}
	now := m.svc.Now()

	add(sSection.Render("Presence"))
	if len(d.Presences) == 0 {
		add(sMuted.Render("  none recorded — n adds one"))
	}
	for i, p := range d.Presences {
		on := m.day.cursor == i
		if on {
			cursorLine = len(lines)
		}
		dur := timeutil.FormatSeconds(service.PresenceSeconds(p, now))
		note := ""
		if p.To == "" {
			if p.Date == timeutil.Date(now) {
				note = sOK.Render("  running")
			} else {
				dur, note = "", sErr.Render("  not closed — s stops it")
			}
		}
		row := pad(span(p), 14) + padLeft(dur, 6) + note
		if on {
			row = sSelected.Render(row)
		}
		add(cursor(on) + row)
	}
	add("")

	add(sSection.Render("Activities"))
	if len(d.Activities) == 0 {
		add(sMuted.Render("  nothing logged — a adds an activity"))
	}
	nameW := 0
	for _, a := range d.Activities {
		nameW = max(nameW, len([]rune(a.Project.Name+" / "+a.Task.Name)))
	}
	nameW = min(nameW, max(20, m.width/2-10))
	for i, a := range d.Activities {
		idx := len(d.Presences) + i
		on := m.day.cursor == idx
		if on {
			cursorLine = len(lines)
		}
		desc := oneLine(a.Description)
		if !service.HasDescription(a) {
			desc = sMuted.Render("(no description yet)")
		}
		dur := timeutil.FormatSeconds(service.TimerSeconds(a, now))
		if a.TimerRunning() {
			dur = sOK.Render("⏱ " + dur)
		}
		name := clip(a.Project.Name+" / "+a.Task.Name, nameW)
		row := padLeft(dur, 7) + "  " + sProject.Render(pad(name, nameW)) + "  " + desc
		if on {
			row = sSelected.Render(row)
		}
		add(cursor(on) + row)
	}
	add("")

	totals := fmt.Sprintf("Present %s · Logged %s", timeutil.FormatSeconds(d.PresentSeconds), timeutil.FormatSeconds(d.LoggedSeconds))
	switch g := d.Gap(); {
	case len(d.Presences) == 0 && len(d.Activities) == 0:
	case len(d.Presences) == 0:
		totals += sWarn.Render(" · activities without a presence")
	case g > 0:
		totals += " · " + sErr.Render("Missing "+timeutil.FormatSeconds(g))
	case g < 0:
		totals += " · " + sWarn.Render(timeutil.FormatSeconds(-g)+" more logged than present")
	default:
		totals += " · " + sOK.Render("complete ✓")
	}
	add(totals)
	return window(lines, cursorLine, height)
}

// window cuts lines to height, keeping the title and the cursor line visible.
func window(lines []string, cursorLine, height int) string {
	if height <= 0 || len(lines) <= height {
		return strings.Join(lines, "\n")
	}
	const head = 2
	rest := height - head
	start := head
	if cursorLine >= start+rest-1 {
		start = cursorLine - rest + 2
	}
	end := min(len(lines), start+rest)
	if end-start < rest {
		start = max(head, end-rest)
	}
	out := append([]string{}, lines[:head]...)
	return strings.Join(append(out, lines[start:end]...), "\n")
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
