package tui

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

func (m *model) openPrompt(p *prompt) {
	p.setWidth(m.popupWidth())
	p.recheck()
	m.prompt = p
}

// editSelected edits the presence or activity under the cursor of the focused panel.
func (m *model) editSelected() tea.Cmd {
	switch m.focus {
	case pPresence:
		if p := m.selPresence(); p != nil {
			m.editPresence(*p)
		}
	case pActivities:
		if a := m.selActivity(); a != nil {
			return m.editActivity(*a)
		}
	}
	return nil
}

func (m *model) dayLabel() string { return m.date.Format("Mon 2 Jan") }

func (m *model) isToday() bool { return m.date.Equal(m.today()) }

func (m *model) editPresence(p api.Presence) {
	date, ps := m.date, m.selDay().Presences
	pr := newPrompt("Edit presence "+span(p)+" · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		if v[1] == "" && p.To != "" {
			return nil, fmt.Errorf("MOCO can't reopen a closed presence — delete it and add an open one (n)")
		}
		from, to, err := service.CheckPresence(ps, p.ID, v[0], v[1])
		if err != nil {
			return nil, err
		}
		if from == p.From && to == p.To {
			return nil, fmt.Errorf("nothing changed")
		}
		return m.write(func(ctx context.Context) (string, error) {
			np, err := m.svc.SetPresence(ctx, date, p.ID, from, to)
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
	pr.note = dayPresences(ps)
	m.checkPresence(pr, ps, p.ID)
	m.openPrompt(pr)
}

func (m *model) newPresence() {
	d := m.selDay()
	if d == nil {
		m.setMsg(msgInfo, "Still loading "+m.dayLabel()+"…")
		return
	}
	date, ps := m.date, d.Presences
	from := ""
	if m.isToday() && len(ps) == 0 {
		from = timeutil.Clock(m.svc.Now())
	}
	pr := newPrompt("Add presence · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		from, to, err := service.CheckPresence(ps, 0, v[0], v[1])
		if err != nil {
			return nil, err
		}
		return m.write(func(ctx context.Context) (string, error) {
			p, err := m.svc.AddPresence(ctx, date, from, to)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Added presence %s on %s.", span(p), date.Format("Mon 2 Jan")), nil
		}), nil
	})
	pr.add("From", from, "HH:MM").add("To", "", "empty = open (still working)")
	pr.note = dayPresences(ps)
	m.checkPresence(pr, ps, 0)
	m.openPrompt(pr)
}

// presenceForm is `p`, the one form for the day's working time. With an open presence it shows
// its start and asks for the end (the start can be corrected too); otherwise it adds the next
// block, starting where the last one ended (an empty end leaves it open). Collisions with the
// day's other presences are shown while typing, before anything is sent to MOCO.
func (m *model) presenceForm() {
	d := m.selDay()
	if d == nil {
		m.setMsg(msgInfo, "Still loading "+m.dayLabel()+"…")
		return
	}
	if d.OpenPresence == nil {
		m.newPresence()
		if m.prompt != nil && len(d.Presences) > 0 {
			last := d.Presences[len(d.Presences)-1]
			m.prompt.fields[0].input.SetValue(last.To)
			m.prompt.recheck()
		}
		return
	}
	date, ps, open := m.date, d.Presences, *d.OpenPresence
	timerNote := m.timer != nil && m.timer.Date == timeutil.Date(date)
	pr := newPrompt("Presence "+span(open)+" · "+m.dayLabel(), func(v []string) (tea.Cmd, error) {
		from, to, err := service.CheckPresence(ps, open.ID, v[0], v[1])
		if err != nil {
			return nil, err
		}
		if from == open.From && to == "" {
			return nil, fmt.Errorf("give an end time — esc keeps it open")
		}
		return m.write(func(ctx context.Context) (string, error) {
			var p api.Presence
			var err error
			if from == open.From {
				p, err = m.svc.Stop(ctx, date, to) // queued when offline
			} else {
				p, err = m.svc.SetPresence(ctx, date, open.ID, from, to)
			}
			if err != nil {
				return "", err
			}
			text := fmt.Sprintf("Presence is now %s on %s.", span(p), date.Format("Mon 2 Jan"))
			if timerNote && p.To != "" {
				text += " A timer is still running — T stops it."
			}
			return text, nil
		}), nil
	})
	pr.add("From", open.From, "HH:MM").add("To", "", "empty = still open")
	pr.move(1)
	pr.note = dayPresences(ps)
	if timerNote {
		pr.note += "\nA timer is running; it is not stopped with the presence."
	}
	m.checkPresence(pr, ps, open.ID)
	m.openPrompt(pr)
}

// checkPresence makes pr warn live when its From/To collide with the day's presences ps (the
// presence id excluded). Times still being typed ("1", "13:") are not judged yet.
func (m *model) checkPresence(pr *prompt, ps []api.Presence, id int64) {
	pr.retry = true
	pr.check = func(v []string) string {
		if !typedClock(v[0]) || (v[1] != "" && !typedClock(v[1])) {
			return ""
		}
		if _, _, err := service.CheckPresence(ps, id, v[0], v[1]); err != nil {
			return err.Error()
		}
		return ""
	}
}

// typedClock reports whether s looks like a finished time: H:MM, HH:MM or 3–4 digits.
func typedClock(s string) bool {
	if i := strings.IndexAny(s, ":."); i >= 0 {
		return len(s)-i-1 == 2
	}
	return len(s) >= 3
}

// dayPresences lists the day's presences for a form's note.
func dayPresences(ps []api.Presence) string {
	if len(ps) == 0 {
		return "No presence recorded on this day yet."
	}
	spans := make([]string, len(ps))
	for i, p := range ps {
		spans[i] = span(p)
	}
	return "Recorded: " + strings.Join(spans, " · ")
}

func (m *model) stopPresence() {
	d := m.selDay()
	if d == nil || d.OpenPresence == nil {
		m.setMsg(msgErr, "No open presence on "+m.dayLabel()+" — nothing to stop.")
		return
	}
	date := m.date
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
	date := m.date
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
	p := m.selPresence()
	d := m.selDay()
	if p == nil || m.focus != pPresence {
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
	date, id := m.date, p.ID
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
	d := m.selDay()
	if d == nil || len(d.Presences) == 0 {
		m.setMsg(msgErr, "No presence on "+m.dayLabel()+" — the location is stored with the presences.")
		return nil
	}
	home := !d.Presences[0].IsHomeOffice
	date := m.date
	return m.write(func(ctx context.Context) (string, error) {
		if err := m.svc.SetDayLocation(ctx, date, home); err != nil {
			return "", err
		}
		return fmt.Sprintf("%s: %s for the whole day.", date.Format("Mon 2 Jan"), where(home)), nil
	})
}

func (m *model) deleteSelected() {
	var p *api.Presence
	var a *api.Activity
	switch m.focus {
	case pPresence:
		p = m.selPresence()
	case pActivities:
		a = m.selActivity()
	default:
		m.setMsg(msgInfo, "Select a presence (2) or an activity (3) to delete.")
		return
	}
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
