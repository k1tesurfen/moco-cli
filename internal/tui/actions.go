package tui

import (
	"context"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

func (m *model) openPrompt(p *prompt) {
	p.setWidth(m.popupWidth())
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
	date := m.date
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
	date := m.date
	from := ""
	if m.isToday() && m.selDay() != nil && len(m.selDay().Presences) == 0 {
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

