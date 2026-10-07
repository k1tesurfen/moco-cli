package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

func timerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "timer",
		Short: "Start, stop or show the MOCO timer (today only)",
	}
	cmd.AddCommand(timerStartCmd(), timerStopCmd(), timerStatusCmd(), timerCancelCmd())
	return cmd
}

func timerStartCmd() *cobra.Command {
	var project, task string
	cmd := &cobra.Command{
		Use:   "start [alias] [description...]",
		Short: "Start a timer on a new activity for today (wizard for project/task if no alias)",
		Long: `Start a timer on a new activity for today. The description can be given now or when
stopping. If a timer is already running, it is stopped first (asking for its description).

  moco timer start                  # pick project + task
  moco timer start dev              # alias
  moco timer start dev Login form   # alias + description
  moco timer start -p intern -t prog`,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			projects, err := svc.Projects(ctx, false)
			if err != nil {
				return err
			}
			var pick *service.Pick
			if len(args) > 0 {
				if al, ok := svc.Cfg.Aliases[args[0]]; ok {
					p, t, err := service.ResolveAlias(projects, args[0], al)
					if err != nil {
						return err
					}
					pick = &service.Pick{Project: p, Task: t}
					args = args[1:]
				}
			}
			if project != "" {
				p, err := service.ResolveProject(projects, project)
				if err != nil {
					return err
				}
				pick = &service.Pick{Project: p}
				switch ts := service.ActiveTasks(p); {
				case task != "":
					if pick.Task, err = service.ResolveTask(p, task); err != nil {
						return err
					}
				case len(ts) == 1:
					pick.Task = ts[0]
				default:
					return errors.New("-p needs a task too (-t)")
				}
			} else if task != "" {
				return errors.New("-t needs a project (-p)")
			}
			description := strings.Join(args, " ")

			// Stop a running timer first, so the question comes before the project wizard.
			running, err := svc.RunningTimer(ctx)
			if err != nil {
				return err
			}
			if running != nil {
				fmt.Printf("A timer is running on %s / %s (%s).\n", running.Project.Name, running.Task.Name,
					timeutil.FormatSeconds(service.TimerSeconds(*running, svc.Now())))
				if err := stopTimer(ctx, svc, *running, ""); err != nil {
					return err
				}
			}

			if pick == nil {
				if !isTTY() {
					return errors.New("missing project (alias or -p/-t; no terminal for the wizard)")
				}
				env, err := wizardEnv(svc, projects)
				if err != nil {
					return err
				}
				p, err := wizard.PickPair(ctx, env)
				if err != nil {
					return err
				}
				pick = &p
			}
			a, err := svc.StartTimer(ctx, pick.Project, pick.Task, description)
			if err != nil {
				return err
			}
			fmt.Printf("Timer started at %s on %s / %s.\n", a.TimerStartedAt.Local().Format("15:04"), a.Project.Name, a.Task.Name)
			// MOCO opens a presence by itself when a timer starts and none is open.
			if day, err := svc.Day(ctx, svc.Now()); err == nil && day.OpenPresence != nil {
				fmt.Printf("Working time is open since %s (MOCO opens it with the timer if needed).\n", day.OpenPresence.From)
			}
			if !service.HasDescription(a) {
				fmt.Println("Stop it with `moco timer stop` (asks for the description).")
			}
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project (name, customer or identifier)")
	cmd.Flags().StringVarP(&task, "task", "t", "", "task of the project")
	return cmd
}

func timerStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop [description...]",
		Short: "Stop the timer; the time is rounded up and the description asked for",
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			running, err := svc.RunningTimer(ctx)
			if err != nil {
				return err
			}
			if running == nil {
				return service.ErrNoTimer
			}
			if err := stopTimer(ctx, svc, *running, strings.Join(args, " ")); err != nil {
				return err
			}
			return printBalance(ctx, svc, svc.Now())
		},
	}
}

// stopTimer asks for the description (unless given) and stops the running timer.
func stopTimer(ctx context.Context, svc *service.Service, running api.Activity, description string) error {
	if strings.TrimSpace(description) == "" {
		initial := ""
		if service.HasDescription(running) {
			initial = running.Description
		}
		switch {
		case isTTY():
			var err error
			title := fmt.Sprintf("Stop timer · %s / %s · %s", running.Project.Name, running.Task.Name,
				timeutil.FormatSeconds(service.TimerSeconds(running, svc.Now())))
			if description, err = wizard.AskDescription(ctx, title, "what did you do?", initial); err != nil {
				return err
			}
		case initial == "":
			return errors.New("a description is required to stop the timer: `moco timer stop <description>`")
		}
	}
	res, err := svc.StopTimer(ctx, description)
	queuedUpdate := queued(err)
	if err != nil && !queuedUpdate {
		return err
	}
	a := res.Activity
	if queuedUpdate {
		fmt.Printf("Timer stopped on %s / %s after %s; the rounded time and description are queued.\n",
			a.Project.Name, a.Task.Name, timeutil.FormatSeconds(res.Tracked))
		return nil
	}
	rounding := ""
	if a.Seconds != res.Tracked {
		rounding = fmt.Sprintf(" (%s rounded up)", timeutil.FormatSeconds(res.Tracked))
	}
	fmt.Printf("Timer stopped: logged %s%s on %s / %s: %s\n", timeutil.FormatSeconds(a.Seconds), rounding,
		a.Project.Name, a.Task.Name, oneLine(a.Description, 60))
	return nil
}

func timerStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show the running timer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			running, err := svc.RunningTimer(cmd.Context())
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(map[string]any{"running_timer": running})
			}
			if running == nil {
				fmt.Println("No timer running.")
				return nil
			}
			since := running.TimerStartedAt.Local()
			when := since.Format("15:04")
			if running.Date != timeutil.Date(svc.Now()) {
				when = since.Format("Mon 2 Jan 15:04")
			}
			desc := ""
			if service.HasDescription(*running) {
				desc = " · " + oneLine(running.Description, 60)
			}
			fmt.Printf("⏱ %s on %s / %s since %s%s\n", timeutil.FormatSeconds(service.TimerSeconds(*running, svc.Now())),
				running.Project.Name, running.Task.Name, when, desc)
			return nil
		},
	}
}

func timerCancelCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "cancel",
		Short: "Stop the timer and delete its activity (nothing is logged)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			running, err := svc.RunningTimer(ctx)
			if err != nil {
				return err
			}
			if running == nil {
				return service.ErrNoTimer
			}
			q := fmt.Sprintf("Discard the timer on %s / %s (%s)?", running.Project.Name, running.Task.Name,
				timeutil.FormatSeconds(service.TimerSeconds(*running, svc.Now())))
			if !yes && !confirm(q) {
				return errors.New("aborted")
			}
			if _, err := svc.CancelTimer(ctx); err != nil {
				return err
			}
			fmt.Println("Timer discarded, nothing logged.")
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}
