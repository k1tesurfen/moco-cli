package tui

import (
	"context"
	"errors"
	"fmt"
	"io"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

// The activity wizard is the same inline huh flow as `moco log`; the TUI hands it the terminal
// with tea.Exec and takes it back afterwards. MOCO is written by the TUI, not inside the wizard.

type execFn func() error

func (f execFn) Run() error        { return f() }
func (execFn) SetStdin(io.Reader)  {}
func (execFn) SetStdout(io.Writer) {}
func (execFn) SetStderr(io.Writer) {}

type wizardKind int

const (
	wizAdd wizardKind = iota
	wizEdit
	wizTimer
)

type wizardMsg struct {
	kind   wizardKind
	act    wizard.Activity
	id     int64
	change service.ActivityChange
	pick   service.Pick
	err    error
}

// runWizard releases the terminal, runs fn and delivers its result.
func (m *model) runWizard(res *wizardMsg, fn func(env wizard.Env) error) tea.Cmd {
	projects := m.projects.list
	return tea.Exec(execFn(func() error {
		if projects == nil {
			fmt.Println("Loading projects…")
			ps, err := m.svc.Projects(m.ctx, false)
			if err != nil {
				res.err = err
				return nil
			}
			projects = ps
		}
		env, _, err := wizard.NewEnv(m.svc, projects)
		if err != nil {
			res.err = err
			return nil
		}
		fmt.Println()
		res.err = fn(env)
		return nil
	}), func(error) tea.Msg { return *res })
}

func (m *model) addActivity() tea.Cmd {
	date, gap := m.date, 0
	if d := m.selDay(); d != nil {
		gap = d.Gap()
	}
	res := &wizardMsg{kind: wizAdd}
	return m.runWizard(res, func(env wizard.Env) error {
		env.GapSeconds = gap
		res.act = wizard.Activity{Date: date}
		return wizard.Run(m.ctx, &res.act, env)
	})
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
	res := &wizardMsg{kind: wizEdit, id: a.ID}
	return m.runWizard(res, func(env wizard.Env) error {
		var err error
		res.change, err = wizard.Edit(m.ctx, a, env)
		return err
	})
}

// timerKey stops the running timer (asking for the description) or starts one (wizard).
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
	res := &wizardMsg{kind: wizTimer}
	return m.runWizard(res, func(env wizard.Env) error {
		var err error
		res.pick, err = wizard.PickPair(m.ctx, env)
		return err
	})
}

func (m *model) wizardDone(msg wizardMsg) tea.Cmd {
	switch {
	case errors.Is(msg.err, wizard.ErrAborted):
		m.setMsg(msgInfo, "Cancelled — nothing saved.")
		return nil
	case msg.err != nil:
		m.noteErr(msg.err)
		m.setMsg(msgErr, msg.err.Error())
		return nil
	}
	switch msg.kind {
	case wizAdd:
		a := msg.act
		return m.write(func(ctx context.Context) (string, error) {
			act, err := m.svc.LogActivity(ctx, a.Date, *a.Project, *a.Task, a.Seconds, a.Description)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Logged %s on %s / %s, %s.", timeutil.FormatSeconds(act.Seconds),
				act.Project.Name, act.Task.Name, a.Date.Format("Mon 2 Jan")), nil
		})
	case wizEdit:
		ch := msg.change
		if ch == (service.ActivityChange{}) {
			m.setMsg(msgInfo, "Nothing changed.")
			return nil
		}
		id := msg.id
		return m.write(func(ctx context.Context) (string, error) {
			a, err := m.svc.EditActivity(ctx, id, ch)
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Updated: %s · %s / %s · %s", timeutil.FormatSeconds(a.Seconds), a.Project.Name,
				a.Task.Name, oneLine(a.Description)), nil
		})
	default:
		p := msg.pick
		return m.write(func(ctx context.Context) (string, error) {
			a, err := m.svc.StartTimer(ctx, p.Project, p.Task, "")
			if err != nil {
				return "", err
			}
			return fmt.Sprintf("Timer started on %s / %s at %s.", a.Project.Name, a.Task.Name, timeutil.Clock(m.svc.Now())), nil
		})
	}
}
