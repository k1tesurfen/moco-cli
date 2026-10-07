// Package wizard holds the inline (non-fullscreen) interactive flows built with huh.
package wizard

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/huh"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// ErrAborted is returned when the user cancels a wizard.
var ErrAborted = errors.New("aborted")

// Activity is the state of an activity being entered. Nil/zero fields are asked for.
type Activity struct {
	Date        time.Time
	Project     *api.Project
	Task        *api.Task
	Seconds     int // unrounded; 0 = ask
	Description string
}

// Env provides what the wizard needs to offer choices.
type Env struct {
	Projects        []api.Project
	Recent          []service.Pick
	Aliases         map[string]service.Pick
	LastTask        func(projectID int64) int64
	Round           func(sec int) int
	RoundingMinutes int
	GapSeconds      int  // prefill for the duration if > 0
	AskAll          bool // ask every field, with the current values preselected (edit mode)
	Title           string
}

type choice struct{ project, task int64 }

// Run asks for the missing fields of a and shows a confirmation summary.
func Run(ctx context.Context, a *Activity, env Env) error {
	if err := askPair(ctx, a, env); err != nil {
		return err
	}

	if a.Seconds == 0 || a.Description == "" || env.AskAll {
		if err := askDetails(ctx, a, env); err != nil {
			return err
		}
	}

	ok := true
	summary := fmt.Sprintf("%s  %s  %s\n%s",
		styleMuted.Render(a.Date.Format("Mon 2 Jan")),
		styleAccent.Render(a.Project.Name+" / "+a.Task.Name),
		roundingPreview(a.Seconds, env),
		a.Description)
	title := "Log this?"
	if env.Title != "" {
		title = env.Title
	}
	err := run(ctx, huh.NewConfirm().Title(title).Description(summary).Affirmative("Yes").Negative("Cancel").Value(&ok))
	if err != nil {
		return err
	}
	if !ok {
		return ErrAborted
	}
	return nil
}

// PickPair asks for a project and task only.
func PickPair(ctx context.Context, env Env) (service.Pick, error) {
	var a Activity
	if err := askPair(ctx, &a, env); err != nil {
		return service.Pick{}, err
	}
	return service.Pick{Project: *a.Project, Task: *a.Task}, nil
}

// askPair fills a.Project and a.Task (asking only what is missing, or everything in AskAll mode).
func askPair(ctx context.Context, a *Activity, env Env) error {
	if a.Project == nil || env.AskAll {
		c, err := askProject(ctx, a, env)
		if err != nil {
			return err
		}
		var p api.Project
		for _, q := range env.Projects {
			if q.ID == c.project {
				p = q
			}
		}
		if a.Project == nil || a.Project.ID != p.ID {
			a.Task = nil
		}
		a.Project = &p
		for _, t := range p.Tasks {
			if c.task != 0 && t.ID == c.task {
				t := t
				a.Task = &t
			}
		}
	}
	if a.Task == nil || env.AskAll {
		t, err := askTask(ctx, *a.Project, a.Task, env)
		if err != nil {
			return err
		}
		a.Task = &t
	}
	return nil
}

func askProject(ctx context.Context, a *Activity, env Env) (choice, error) {
	var opts []huh.Option[choice]
	seen := map[choice]bool{}
	add := func(label string, c choice) {
		if !seen[c] {
			seen[c] = true
			opts = append(opts, huh.NewOption(label, c))
		}
	}
	for _, r := range env.Recent {
		add(fmt.Sprintf("↺ %s / %s", r.Project.Name, r.Task.Name), choice{r.Project.ID, r.Task.ID})
	}
	names := make([]string, 0, len(env.Aliases))
	for n := range env.Aliases {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p := env.Aliases[n]
		add(fmt.Sprintf("@%s  %s / %s", n, p.Project.Name, p.Task.Name), choice{p.Project.ID, p.Task.ID})
	}
	for _, p := range env.Projects {
		add(fmt.Sprintf("%s · %s · %s", p.Name, p.Customer.Name, p.Identifier), choice{p.ID, 0})
	}

	var c choice
	if a.Project != nil {
		c = choice{a.Project.ID, 0}
	}
	sel := huh.NewSelect[choice]().
		Title("Project").
		Description("type to filter · ↺ recent · @ alias").
		Options(opts...).
		Filtering(a.Project == nil).
		Height(14).
		Value(&c)
	return c, run(ctx, sel)
}

func askTask(ctx context.Context, p api.Project, current *api.Task, env Env) (api.Task, error) {
	tasks := service.ActiveTasks(p)
	if len(tasks) == 0 {
		return api.Task{}, fmt.Errorf("project %q has no active tasks", p.Name)
	}
	if len(tasks) == 1 && !env.AskAll {
		return tasks[0], nil
	}
	var id int64
	switch {
	case current != nil:
		id = current.ID
	case env.LastTask != nil:
		id = env.LastTask(p.ID)
	}
	var opts []huh.Option[int64]
	for _, t := range tasks {
		opts = append(opts, huh.NewOption(t.Name, t.ID))
	}
	sel := huh.NewSelect[int64]().
		Title("Task · " + p.Name).
		Description("type / to filter").
		Options(opts...).
		Height(14).
		Value(&id)
	if err := run(ctx, sel); err != nil {
		return api.Task{}, err
	}
	for _, t := range tasks {
		if t.ID == id {
			return t, nil
		}
	}
	return tasks[0], nil
}

func askDetails(ctx context.Context, a *Activity, env Env) error {
	// The duration field starts empty; the fallback (current value when editing, otherwise the
	// unlogged time of the day) is shown as a hint and used when Enter is pressed on an empty field.
	fallback, hint := 0, "e.g. 1h30, 90m, 1:30, 1.5"
	switch {
	case env.AskAll && a.Seconds > 0:
		fallback = a.Seconds
		hint = "currently " + timeutil.FormatSeconds(fallback) + " · Enter keeps it · " + hint
	case env.GapSeconds > 0:
		fallback = env.GapSeconds
		hint = timeutil.FormatSeconds(fallback) + " not logged yet · Enter takes it · " + hint
	case a.Seconds > 0:
		fallback = a.Seconds
	}
	seconds := func(s string) (int, error) {
		if strings.TrimSpace(s) == "" && fallback > 0 {
			return fallback, nil
		}
		return timeutil.ParseDuration(s)
	}

	dur := ""
	desc := a.Description
	durInput := huh.NewInput().
		Title("Duration").
		Description(hint).
		Value(&dur).
		Validate(func(s string) error { _, err := seconds(s); return err })
	if fallback > 0 {
		durInput.Placeholder(timeutil.FormatSeconds(fallback))
	}
	form := huh.NewForm(huh.NewGroup(
		durInput,
		huh.NewNote().
			DescriptionFunc(func() string {
				sec, err := seconds(dur)
				if err != nil {
					return ""
				}
				return "→ " + roundingPreview(sec, env)
			}, &dur),
		huh.NewInput().
			Title("Description").
			Description("what did you do?").
			Value(&desc).
			CharLimit(2000).
			Validate(func(s string) error {
				if strings.TrimSpace(s) == "" {
					return errors.New("a description is required")
				}
				return nil
			}),
	)).WithShowHelp(false).WithTheme(theme())
	if err := form.RunWithContext(ctx); err != nil {
		return mapErr(err)
	}
	sec, err := seconds(dur)
	if err != nil {
		return err
	}
	a.Seconds = sec
	a.Description = strings.TrimSpace(desc)
	return nil
}

// roundingPreview renders "1h07 → 1h15" or just "1h15" when no rounding happens.
func roundingPreview(sec int, env Env) string {
	r := sec
	if env.Round != nil {
		r = env.Round(sec)
	}
	if r == sec {
		return timeutil.FormatSeconds(sec)
	}
	return fmt.Sprintf("%s → %s (rounded up to %d min)", timeutil.FormatSeconds(sec), timeutil.FormatSeconds(r), env.RoundingMinutes)
}

func run(ctx context.Context, f huh.Field) error {
	return mapErr(huh.NewForm(huh.NewGroup(f)).WithShowHelp(false).WithTheme(theme()).RunWithContext(ctx))
}

func mapErr(err error) error {
	if errors.Is(err, huh.ErrUserAborted) {
		return ErrAborted
	}
	return err
}

// AskDescription asks for a required description; initial is prefilled.
func AskDescription(ctx context.Context, title, hint, initial string) (string, error) {
	desc := initial
	in := huh.NewInput().
		Title(title).
		Description(hint).
		Value(&desc).
		CharLimit(2000).
		Validate(func(s string) error {
			if strings.TrimSpace(s) == "" {
				return errors.New("a description is required")
			}
			return nil
		})
	if err := run(ctx, in); err != nil {
		return "", err
	}
	return strings.TrimSpace(desc), nil
}
