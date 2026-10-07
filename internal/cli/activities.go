package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

func isTTY() bool { return term.IsTerminal(int(os.Stdin.Fd())) && term.IsTerminal(int(os.Stdout.Fd())) }

func logCmd() *cobra.Command {
	var date, project, task string
	var gap bool
	cmd := &cobra.Command{
		Use:   "log [alias] [duration] [description...]",
		Short: "Log an activity; missing parts are asked interactively",
		Long: `Log an activity. Anything not given on the command line is asked in an inline wizard.

  moco log                                  # wizard
  moco log dev 1h30 Fixed the login form    # alias + duration + description
  moco log 45m Review -p scheidt -t kreation
  moco log --gap                            # duration = unlogged time of the day
  moco log -d yesterday

Durations: 1h30, 90m, 1:30, 1.5 — always rounded up to the configured step (default 15 min).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, d, err := serviceAndDate(ctx, date)
			if err != nil {
				return err
			}
			projects, err := svc.Projects(ctx, false)
			if err != nil {
				return err
			}
			a := wizard.Activity{Date: d}

			if len(args) > 0 {
				if al, ok := svc.Cfg.Aliases[args[0]]; ok {
					p, t, err := service.ResolveAlias(projects, args[0], al)
					if err != nil {
						return err
					}
					a.Project, a.Task = &p, &t
					args = args[1:]
				}
			}
			if len(args) > 0 {
				if sec, err := timeutil.ParseDuration(args[0]); err == nil {
					a.Seconds = sec
					args = args[1:]
				}
			}
			a.Description = strings.TrimSpace(strings.Join(args, " "))

			if project != "" {
				p, err := service.ResolveProject(projects, project)
				if err != nil {
					return err
				}
				a.Project, a.Task = &p, nil
			}
			if task != "" {
				if a.Project == nil {
					return errors.New("-t needs a project (-p or an alias)")
				}
				t, err := service.ResolveTask(*a.Project, task)
				if err != nil {
					return err
				}
				a.Task = &t
			}
			if a.Project != nil && a.Task == nil {
				if ts := service.ActiveTasks(*a.Project); len(ts) == 1 {
					a.Task = &ts[0]
				}
			}

			var day service.Day
			if gap || a.Seconds == 0 {
				if day, err = svc.Day(ctx, d); err != nil {
					return err
				}
			}
			if gap {
				if day.Gap() <= 0 {
					return fmt.Errorf("nothing unlogged on %s (present %s, logged %s)", d.Format("Mon 2 Jan"),
						timeutil.FormatSeconds(day.PresentSeconds), timeutil.FormatSeconds(day.LoggedSeconds))
				}
				a.Seconds = day.Gap()
			}

			if a.Project == nil || a.Task == nil || a.Seconds == 0 || a.Description == "" {
				if !isTTY() {
					return fmt.Errorf("missing %s (no terminal for the wizard)", strings.Join(missing(a), ", "))
				}
				env, err := wizardEnv(svc, projects)
				if err != nil {
					return err
				}
				env.GapSeconds = day.Gap()
				if err := wizard.Run(ctx, &a, env); err != nil {
					return err
				}
			}

			act, err := svc.LogActivity(ctx, d, *a.Project, *a.Task, a.Seconds, a.Description)
			if err != nil {
				return err
			}
			rounding := ""
			if act.Seconds != a.Seconds {
				rounding = fmt.Sprintf(" (%s rounded up)", timeutil.FormatSeconds(a.Seconds))
			}
			fmt.Printf("Logged %s%s on %s / %s, %s.\n", timeutil.FormatSeconds(act.Seconds), rounding,
				act.Project.Name, act.Task.Name, d.Format("Mon 2 Jan"))
			return printBalance(ctx, svc, d)
		},
	}
	dayFlag(cmd, &date)
	cmd.Flags().StringVarP(&project, "project", "p", "", "project (name, customer or identifier; words must all match)")
	cmd.Flags().StringVarP(&task, "task", "t", "", "task of the project")
	cmd.Flags().BoolVar(&gap, "gap", false, "use the unlogged time of the day (present − logged) as duration")
	return cmd
}

func missing(a wizard.Activity) []string {
	var m []string
	if a.Project == nil {
		m = append(m, "project (-p or alias)")
	}
	if a.Task == nil {
		m = append(m, "task (-t)")
	}
	if a.Seconds == 0 {
		m = append(m, "duration")
	}
	if a.Description == "" {
		m = append(m, "description")
	}
	return m
}

func wizardEnv(svc *service.Service, projects []api.Project) (wizard.Env, error) {
	recent, err := svc.RecentPicks(projects, 5)
	if err != nil {
		return wizard.Env{}, err
	}
	aliases := map[string]service.Pick{}
	for name, al := range svc.Cfg.Aliases {
		p, t, err := service.ResolveAlias(projects, name, al)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Warning:", err)
			continue
		}
		aliases[name] = service.Pick{Project: p, Task: t}
	}
	return wizard.Env{
		Projects:        projects,
		Recent:          recent,
		Aliases:         aliases,
		LastTask:        svc.LastTask,
		Round:           svc.Round,
		RoundingMinutes: svc.Cfg.RoundingMinutes,
	}, nil
}

// printBalance prints present vs. logged for a day.
func printBalance(ctx context.Context, svc *service.Service, d time.Time) error {
	day, err := svc.Day(ctx, d)
	if err != nil {
		return err
	}
	line := fmt.Sprintf("%s: present %s · logged %s", d.Format("Mon 2 Jan"),
		timeutil.FormatSeconds(day.PresentSeconds), timeutil.FormatSeconds(day.LoggedSeconds))
	switch g := day.Gap(); {
	case len(day.Presences) == 0:
		line += " · no presence recorded"
	case g > 0:
		line += " · missing " + timeutil.FormatSeconds(g)
	case g < 0:
		line += " · " + timeutil.FormatSeconds(-g) + " more logged than present"
	default:
		line += " · complete"
	}
	fmt.Println(line)
	return nil
}

func listCmd() *cobra.Command {
	var date, from, to string
	var week bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List your activities (default today)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, d, err := serviceAndDate(ctx, date)
			if err != nil {
				return err
			}
			start, end := d, d
			switch {
			case from != "" || to != "":
				if start, err = timeutil.ParseDate(from, svc.Now()); err != nil {
					return err
				}
				if end, err = timeutil.ParseDate(to, svc.Now()); err != nil {
					return err
				}
			case week:
				start = d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7))
				end = start.AddDate(0, 0, 6)
			}
			acts, err := svc.ActivitiesBetween(ctx, start, end)
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(acts)
			}
			if len(acts) == 0 {
				fmt.Println("No activities.")
				return nil
			}
			multiDay := timeutil.Date(start) != timeutil.Date(end)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			if multiDay {
				fmt.Fprintln(w, "ID\tDATE\tTIME\tPROJECT\tTASK\tDESCRIPTION")
			} else {
				fmt.Fprintln(w, "ID\tTIME\tPROJECT\tTASK\tDESCRIPTION")
			}
			total := 0
			perDay := map[string]int{}
			for _, a := range acts {
				total += a.Seconds
				perDay[a.Date] += a.Seconds
				desc := oneLine(a.Description, 50)
				if a.TimerRunning() {
					desc = "⏱ " + desc
				}
				if multiDay {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", a.ID, a.Date, timeutil.FormatSeconds(a.Seconds), oneLine(a.Project.Name, 30), a.Task.Name, desc)
				} else {
					fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", a.ID, timeutil.FormatSeconds(a.Seconds), oneLine(a.Project.Name, 30), a.Task.Name, desc)
				}
			}
			w.Flush()
			if multiDay {
				days := make([]string, 0, len(perDay))
				for k := range perDay {
					days = append(days, k)
				}
				sort.Strings(days)
				fmt.Println()
				for _, k := range days {
					t, _ := time.ParseInLocation(timeutil.DateLayout, k, time.Local)
					fmt.Printf("%s  %s\n", t.Format("Mon 2 Jan"), timeutil.FormatSeconds(perDay[k]))
				}
			}
			fmt.Printf("Total %s\n", timeutil.FormatSeconds(total))
			return nil
		},
	}
	dayFlag(cmd, &date)
	cmd.Flags().BoolVar(&week, "week", false, "the whole week (Mon–Sun) of the day")
	cmd.Flags().StringVar(&from, "from", "", "range start (same formats as --date)")
	cmd.Flags().StringVar(&to, "to", "", "range end (same formats as --date)")
	return cmd
}

func editCmd() *cobra.Command {
	var date, project, task, duration, description string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Edit one of your activities (wizard if no flags are given)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id %q", args[0])
			}
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			cur, err := svc.OwnActivity(ctx, id)
			if err != nil {
				return err
			}
			if cur.TimerRunning() {
				return fmt.Errorf("activity %d has a running timer — stop it first", id)
			}
			projects, err := svc.Projects(ctx, false)
			if err != nil {
				return err
			}

			var ch service.ActivityChange
			anyFlag := false
			if date != "" {
				d, err := timeutil.ParseDate(date, svc.Now())
				if err != nil {
					return err
				}
				ch.Date, anyFlag = &d, true
			}
			if project != "" {
				p, err := service.ResolveProject(projects, project)
				if err != nil {
					return err
				}
				ch.Project, anyFlag = &p, true
			}
			if task != "" {
				p := ch.Project
				if p == nil {
					cp, err := service.ResolveProject(projects, cur.Project.Name)
					if err != nil {
						return fmt.Errorf("current project: %w", err)
					}
					p = &cp
				}
				t, err := service.ResolveTask(*p, task)
				if err != nil {
					return err
				}
				ch.Task, anyFlag = &t, true
			} else if ch.Project != nil {
				ts := service.ActiveTasks(*ch.Project)
				if len(ts) != 1 {
					return errors.New("changing the project needs a task too (-t)")
				}
				ch.Task = &ts[0]
			}
			if duration != "" {
				sec, err := timeutil.ParseDuration(duration)
				if err != nil {
					return err
				}
				ch.Seconds, anyFlag = &sec, true
			}
			if cmd.Flags().Changed("description") {
				ch.Description, anyFlag = &description, true
			}

			if !anyFlag {
				if !isTTY() {
					return errors.New("nothing to change — pass flags or run in a terminal for the wizard")
				}
				ch, err = editWizard(ctx, svc, projects, cur)
				if err != nil {
					return err
				}
			}
			a, err := svc.EditActivity(ctx, id, ch)
			if err != nil {
				return err
			}
			fmt.Printf("Updated %d: %s · %s / %s · %s · %s\n", a.ID, a.Date, a.Project.Name, a.Task.Name,
				timeutil.FormatSeconds(a.Seconds), oneLine(a.Description, 60))
			return nil
		},
	}
	cmd.Flags().StringVarP(&date, "date", "d", "", "move to another day")
	cmd.Flags().StringVarP(&project, "project", "p", "", "new project")
	cmd.Flags().StringVarP(&task, "task", "t", "", "new task")
	cmd.Flags().StringVar(&duration, "duration", "", "new duration (rounded up)")
	cmd.Flags().StringVarP(&description, "description", "m", "", "new description")
	return cmd
}

func editWizard(ctx context.Context, svc *service.Service, projects []api.Project, cur api.Activity) (service.ActivityChange, error) {
	env, err := wizardEnv(svc, projects)
	if err != nil {
		return service.ActivityChange{}, err
	}
	env.AskAll = true
	env.Title = "Save changes?"
	d, _ := time.ParseInLocation(timeutil.DateLayout, cur.Date, time.Local)
	a := wizard.Activity{Date: d, Seconds: cur.Seconds, Description: cur.Description}
	for _, p := range projects {
		if p.ID == cur.Project.ID {
			p := p
			a.Project = &p
			for _, t := range p.Tasks {
				if t.ID == cur.Task.ID {
					t := t
					a.Task = &t
				}
			}
		}
	}
	if err := wizard.Run(ctx, &a, env); err != nil {
		return service.ActivityChange{}, err
	}
	var ch service.ActivityChange
	if a.Project.ID != cur.Project.ID {
		ch.Project = a.Project
	}
	if a.Task.ID != cur.Task.ID || ch.Project != nil {
		ch.Task = a.Task
	}
	if a.Seconds != cur.Seconds {
		ch.Seconds = &a.Seconds
	}
	if a.Description != cur.Description {
		ch.Description = &a.Description
	}
	return ch, nil
}

func deleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete one of your activities",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id %q", args[0])
			}
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			a, err := svc.OwnActivity(ctx, id)
			if err != nil {
				return err
			}
			summary := fmt.Sprintf("%s · %s / %s · %s · %s", a.Date, a.Project.Name, a.Task.Name,
				timeutil.FormatSeconds(a.Seconds), oneLine(a.Description, 60))
			if !yes && !confirm("Delete "+summary+"?") {
				return errors.New("aborted")
			}
			if _, err := svc.DeleteActivity(ctx, id); err != nil {
				return err
			}
			fmt.Println("Deleted", summary)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}
