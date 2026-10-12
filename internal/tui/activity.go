package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

// openForm builds the activity popup; nil (with a message) while the projects aren't loaded.
func (m *model) openForm(title string) *activityForm {
	if m.projects.list == nil {
		m.setMsg(msgInfo, "Projects are still loading — try again in a moment.")
		return nil
	}
	env, warnings, err := wizard.NewEnv(m.svc, m.projects.list)
	if err != nil {
		m.setMsg(msgErr, err.Error())
		return nil
	}
	if len(warnings) > 0 {
		m.setMsg(msgErr, "Warning: "+warnings[0].Error())
	}
	f := newActivityForm(title, env)
	f.setWidth(m.formWidth() - 4)
	m.form = f
	return f
}

func (m *model) formWidth() int { return clamp(m.width-6, 40, 96) }

func (m *model) addActivity() tea.Cmd {
	f := m.openForm("Log activity · " + m.date.Format("Mon 2 Jan"))
	if f == nil {
		return nil
	}
	if d := m.selDay(); d != nil && d.Gap() > 0 {
		f.fallback = d.Gap()
		f.fallbackHint = timeutil.FormatSeconds(d.Gap()) + " not logged yet · empty = take it"
		f.dur.Placeholder = timeutil.FormatSeconds(d.Gap())
	}
	date := m.date
	f.submit = func(f *activityForm, sec int) (tea.Cmd, error) {
		p, desc := *f.chosen, strings.TrimSpace(f.desc.Value())
		return m.write(func(ctx context.Context) (string, error) {
			a, err := m.svc.LogActivity(ctx, date, p.Project, p.Task, sec, desc)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Logged %s on %s / %s, %s.", timeutil.FormatSeconds(a.Seconds),
				a.Project.Name, a.Task.Name, date.Format("Mon 2 Jan")), nil
		}), nil
	}
	return nil
}

func (m *model) editActivity(a api.Activity) tea.Cmd {
	if a.TimerRunning() {
		m.setMsg(msgErr, "This activity has a running timer — stop it first (T).")
		return nil
	}
	if a.User.ID != 0 && a.User.ID != m.svc.UserID {
		m.setMsg(msgErr, "This activity belongs to someone else.")
		return nil
	}
	f := m.openForm("Edit activity · " + a.Date)
	if f == nil {
		return nil
	}
	cur := service.Pick{Project: api.Project{ID: a.Project.ID, Name: a.Project.Name}, Task: api.Task{ID: a.Task.ID, Name: a.Task.Name}}
	for _, o := range f.options {
		if o.pick.Project.ID == a.Project.ID && o.pick.Task.ID == a.Task.ID {
			cur = o.pick
		}
	}
	f.preselect(cur)
	f.fallback = a.Seconds
	f.fallbackHint = "currently " + timeutil.FormatSeconds(a.Seconds) + " · empty = keep"
	f.dur.Placeholder = timeutil.FormatSeconds(a.Seconds)
	f.desc.SetValue(a.Description)
	f.desc.CursorEnd()
	f.setFocus(fDuration)
	f.submit = func(f *activityForm, sec int) (tea.Cmd, error) {
		var ch service.ActivityChange
		if p := *f.chosen; p.Project.ID != a.Project.ID || p.Task.ID != a.Task.ID {
			ch.Project, ch.Task = &p.Project, &p.Task
		}
		if sec != a.Seconds {
			ch.Seconds = &sec
		}
		if d := strings.TrimSpace(f.desc.Value()); d != a.Description {
			ch.Description = &d
		}
		if ch == (service.ActivityChange{}) {
			return nil, errors.New("nothing changed — esc closes")
		}
		return m.write(func(ctx context.Context) (string, error) {
			u, err := m.svc.EditActivity(ctx, a.ID, ch)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated: %s · %s / %s · %s", timeutil.FormatSeconds(u.Seconds), u.Project.Name,
				u.Task.Name, oneLine(u.Description)), nil
		}), nil
	}
	return nil
}

// timerKey stops the running timer (asking for the description) or starts one (popup).
func (m *model) timerKey() tea.Cmd {
	if t := m.timer; t != nil {
		initial := ""
		if service.HasDescription(*t) {
			initial = t.Description
		}
		pr := newPrompt("Stop timer · "+t.Project.Name+" / "+t.Task.Name, func(v []string) (tea.Cmd, error) {
			if v[0] == "" {
				return nil, errors.New("a description is required")
			}
			desc := v[0]
			return m.write(func(ctx context.Context) (string, error) {
				res, err := m.svc.StopTimer(ctx, desc)
				if err != nil {
					return "", err
				}
				return fmt.Sprintf("Timer stopped: %s tracked, saved as %s.", timeutil.FormatSeconds(res.Tracked),
					timeutil.FormatSeconds(res.Activity.Seconds)), nil
			}), nil
		})
		pr.note = fmt.Sprintf("Running %s · the time is rounded up to %d min.",
			timeutil.FormatSeconds(service.TimerSeconds(*t, m.svc.Now())), m.svc.Cfg.RoundingMinutes)
		pr.add("Description", initial, "what did you do?")
		m.openPrompt(pr)
		return nil
	}
	f := m.openForm("Start timer · today")
	if f == nil {
		return nil
	}
	f.timer = true
	f.submit = func(f *activityForm, _ int) (tea.Cmd, error) {
		p, desc := *f.chosen, strings.TrimSpace(f.desc.Value())
		return m.write(func(ctx context.Context) (string, error) {
			a, err := m.svc.StartTimer(ctx, p.Project, p.Task, desc)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Timer started on %s / %s at %s.", a.Project.Name, a.Task.Name, timeutil.Clock(m.svc.Now())), nil
		}), nil
	}
	return nil
}
