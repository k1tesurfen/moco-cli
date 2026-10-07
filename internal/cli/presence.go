package cli

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// dayFlag adds -d/--date to a command.
func dayFlag(cmd *cobra.Command, target *string) {
	cmd.Flags().StringVarP(target, "date", "d", "", "day: YYYY-MM-DD, today, yesterday, -N or a weekday (default today)")
}

// locationFlags adds --home/--office and returns a func resolving them to *bool.
func locationFlags(cmd *cobra.Command) func() *bool {
	var home, office bool
	cmd.Flags().BoolVar(&home, "home", false, "home office (applies to the whole day in MOCO)")
	cmd.Flags().BoolVar(&office, "office", false, "in the office (applies to the whole day in MOCO)")
	cmd.MarkFlagsMutuallyExclusive("home", "office")
	return func() *bool {
		switch {
		case home:
			return ptr(true)
		case office:
			return ptr(false)
		}
		return nil
	}
}

func ptr[T any](v T) *T { return &v }

// timeArg returns args[0] or, for today only, the current time.
func timeArg(args []string, date time.Time, now time.Time, what string) (string, error) {
	if len(args) == 1 {
		return args[0], nil
	}
	if timeutil.Date(date) != timeutil.Date(now) {
		return "", fmt.Errorf("give the %s time for %s, e.g. 08:00", what, date.Format("Mon 2 Jan"))
	}
	return timeutil.Clock(now), nil
}

func startCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "start [HH:MM]",
		Short: "Start working (open a presence), default now",
		Args:  cobra.MaximumNArgs(1),
	}
	dayFlag(cmd, &date)
	location := locationFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		svc, d, err := serviceAndDate(cmd.Context(), date)
		if err != nil {
			return err
		}
		from, err := timeArg(args, d, svc.Now(), "start")
		if err != nil {
			return err
		}
		p, err := svc.Start(cmd.Context(), d, from, location())
		if queued(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("Started at %s on %s (%s).\n", p.From, d.Format("Mon 2 Jan"), where(p.IsHomeOffice))
		return printDay(cmd.Context(), svc, d)
	}
	return cmd
}

func stopCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "stop [HH:MM]",
		Short: "Finish working (close the open presence), default now",
		Args:  cobra.MaximumNArgs(1),
	}
	dayFlag(cmd, &date)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		svc, d, err := serviceAndDate(cmd.Context(), date)
		if err != nil {
			return err
		}
		to, err := timeArg(args, d, svc.Now(), "end")
		if err != nil {
			return err
		}
		if timeutil.Date(d) == timeutil.Date(svc.Now()) {
			if err := offerTimerStop(cmd.Context(), svc); err != nil {
				return err
			}
		}
		p, err := svc.Stop(cmd.Context(), d, to)
		if queued(err) {
			return nil
		}
		if err != nil {
			return err
		}
		fmt.Printf("Stopped at %s on %s.\n", p.To, d.Format("Mon 2 Jan"))
		return printDay(cmd.Context(), svc, d)
	}
	return cmd
}

func breakCmd() *cobra.Command {
	var date string
	cmd := &cobra.Command{
		Use:   "break [HH:MM-HH:MM]",
		Short: "Record a break by splitting the presence (default: configured break window)",
		Args:  cobra.MaximumNArgs(1),
	}
	dayFlag(cmd, &date)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		svc, d, err := serviceAndDate(cmd.Context(), date)
		if err != nil {
			return err
		}
		from, to := svc.Cfg.Schedule.BreakFrom, svc.Cfg.Schedule.BreakTo
		if len(args) == 1 {
			parts := strings.FieldsFunc(args[0], func(r rune) bool { return r == '-' || r == '–' })
			if len(parts) != 2 {
				return fmt.Errorf("expected a range like 13:00-14:00, got %q", args[0])
			}
			from, to = parts[0], parts[1]
		}
		res, err := svc.Break(cmd.Context(), d, from, to)
		if queued(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if res.After != nil {
			fmt.Printf("Break %s–%s recorded on %s.\n", res.Before.To, res.After.From, d.Format("Mon 2 Jan"))
		} else {
			fmt.Printf("Presence now ends at %s on %s (the break reaches past its end).\n", res.Before.To, d.Format("Mon 2 Jan"))
		}
		return printDay(cmd.Context(), svc, d)
	}
	return cmd
}

func presenceCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "presence",
		Short: "List, edit or delete presences (working time blocks)",
	}
	cmd.AddCommand(presenceListCmd(), presenceEditCmd(), presenceDeleteCmd())
	return cmd
}

func presenceListCmd() *cobra.Command {
	var date string
	var week bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List presences of a day (default today) or its week",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, d, err := serviceAndDate(cmd.Context(), date)
			if err != nil {
				return err
			}
			from, to := d, d
			if week {
				from = d.AddDate(0, 0, -((int(d.Weekday()) + 6) % 7)) // Monday
				to = from.AddDate(0, 0, 6)
			}
			ps, err := svc.PresencesBetween(cmd.Context(), from, to)
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(ps)
			}
			if len(ps) == 0 {
				fmt.Println("No presences.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "ID\tDATE\tFROM\tTO\tDURATION\tLOCATION")
			for _, p := range ps {
				fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\t%s\n", p.ID, p.Date, p.From, orDots(p.To), presenceDuration(p, svc.Now()), where(p.IsHomeOffice))
			}
			return w.Flush()
		},
	}
	dayFlag(cmd, &date)
	cmd.Flags().BoolVar(&week, "week", false, "the whole week (Mon–Sun) of the day")
	return cmd
}

func presenceEditCmd() *cobra.Command {
	var from, to string
	cmd := &cobra.Command{
		Use:   "edit <id>",
		Short: "Change start/end of a presence or the day's location",
		Args:  cobra.ExactArgs(1),
	}
	cmd.Flags().StringVar(&from, "from", "", "new start HH:MM")
	cmd.Flags().StringVar(&to, "to", "", "new end HH:MM")
	location := locationFlags(cmd)
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		id, err := strconv.ParseInt(args[0], 10, 64)
		if err != nil {
			return fmt.Errorf("invalid id %q", args[0])
		}
		svc, err := newService(cmd.Context())
		if err != nil {
			return err
		}
		p, err := svc.EditPresence(cmd.Context(), id, from, to, location())
		if err != nil {
			return err
		}
		fmt.Printf("Presence %d is now %s–%s on %s.\n", p.ID, p.From, orDots(p.To), p.Date)
		if location() != nil {
			fmt.Printf("Location for the whole day: %s.\n", where(p.IsHomeOffice))
		}
		d, _ := time.ParseInLocation(timeutil.DateLayout, p.Date, time.Local)
		return printDay(cmd.Context(), svc, d)
	}
	return cmd
}

func presenceDeleteCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "delete <id>",
		Short: "Delete a presence",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid id %q", args[0])
			}
			if !yes && !confirm(fmt.Sprintf("Delete presence %d?", id)) {
				return fmt.Errorf("aborted")
			}
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			if err := svc.API.DeletePresence(cmd.Context(), id); err != nil {
				return err
			}
			fmt.Printf("Deleted presence %d.\n", id)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}

// confirm asks a yes/no question on the terminal; without a terminal it answers no.
func confirm(question string) bool {
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Fprintln(os.Stderr, question, "(no terminal — pass --yes to confirm)")
		return false
	}
	fmt.Print(question + " [y/N] ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.EqualFold(strings.TrimSpace(line), "y") || strings.EqualFold(strings.TrimSpace(line), "yes")
}

func serviceAndDate(ctx context.Context, date string) (*service.Service, time.Time, error) {
	svc, err := newService(ctx)
	if err != nil {
		return nil, time.Time{}, err
	}
	d, err := timeutil.ParseDate(date, svc.Now())
	if err != nil {
		return nil, time.Time{}, err
	}
	if d.After(svc.Now()) {
		return nil, time.Time{}, fmt.Errorf("%s is in the future", timeutil.Date(d))
	}
	return svc, d, nil
}

// printDay prints a one-line summary of the day's presences after a change.
func printDay(ctx context.Context, svc *service.Service, d time.Time) error {
	ps, err := svc.PresencesBetween(ctx, d, d)
	if err != nil {
		return err
	}
	var spans []string
	total := 0
	home, openPast := false, false
	for _, p := range ps {
		spans = append(spans, p.From+"–"+orDots(p.To))
		total += service.PresenceSeconds(p, svc.Now())
		home = p.IsHomeOffice
		openPast = openPast || (p.To == "" && p.Date != timeutil.Date(svc.Now()))
	}
	if len(spans) == 0 {
		fmt.Printf("%s: no presences.\n", d.Format("Mon 2 Jan"))
		return nil
	}
	summary := timeutil.FormatSeconds(total) + " present"
	if openPast {
		summary = "not finished — close it with `moco stop HH:MM -d " + timeutil.Date(d) + "`"
		if total > 0 {
			summary = timeutil.FormatSeconds(total) + " present, " + summary
		}
	}
	fmt.Printf("%s: %s (%s) · %s\n", d.Format("Mon 2 Jan"), strings.Join(spans, ", "), where(home), summary)
	return nil
}

func presenceDuration(p api.Presence, now time.Time) string {
	if p.To == "" && p.Date != timeutil.Date(now) {
		return "open"
	}
	return timeutil.FormatSeconds(service.PresenceSeconds(p, now))
}

func where(home bool) string {
	if home {
		return "home"
	}
	return "office"
}

func orDots(s string) string {
	if s == "" {
		return "…"
	}
	return s
}

// offerTimerStop asks whether a running timer should be stopped along with the day.
func offerTimerStop(ctx context.Context, svc *service.Service) error {
	running, err := svc.RunningTimer(ctx)
	if err != nil || running == nil {
		return nil // the presence stop itself reports connection problems
	}
	what := fmt.Sprintf("A timer is running on %s / %s (%s).", running.Project.Name, running.Task.Name,
		timeutil.FormatSeconds(service.TimerSeconds(*running, svc.Now())))
	if !isTTY() {
		fmt.Fprintln(os.Stderr, "Note:", what, "It keeps running — `moco timer stop`.")
		return nil
	}
	if !confirm(what + " Stop it too?") {
		return nil
	}
	return stopTimer(ctx, svc, *running, "")
}
