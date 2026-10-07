package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

func statusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show today's presences, logged time, gap and running timer",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			now := svc.Now()
			day, err := svc.Day(cmd.Context(), now)
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(map[string]any{
					"date":            day.Date,
					"presences":       day.Presences,
					"activities":      day.Activities,
					"present_seconds": day.PresentSeconds,
					"logged_seconds":  day.LoggedSeconds,
					"gap_seconds":     day.Gap(),
					"running_timer":   day.RunningTimer,
				})
			}

			fmt.Println(now.Format("Monday, 2 Jan 2006"))
			fmt.Println()
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

			if len(day.Presences) == 0 {
				fmt.Fprintln(w, "Presence\tnone recorded — `moco start`")
			}
			for i, p := range day.Presences {
				label := ""
				if i == 0 {
					label = "Presence"
				}
				fmt.Fprintf(w, "%s\t%s–%s  (%s)\n", label, p.From, orDots(p.To), where(p.IsHomeOffice))
			}
			fmt.Fprintf(w, "Present\t%s\n", timeutil.FormatSeconds(day.PresentSeconds))
			fmt.Fprintf(w, "Logged\t%s\n", timeutil.FormatSeconds(day.LoggedSeconds))
			switch gap := day.Gap(); {
			case gap > 0:
				fmt.Fprintf(w, "Missing\t%s\n", timeutil.FormatSeconds(gap))
			case gap < 0:
				fmt.Fprintf(w, "Over\t%s logged beyond presence\n", timeutil.FormatSeconds(-gap))
			}
			if t := day.RunningTimer; t != nil {
				fmt.Fprintf(w, "Timer\trunning on %s / %s since %s\n",
					t.Project.Name, t.Task.Name, t.TimerStartedAt.In(now.Location()).Format("15:04"))
			}
			w.Flush()

			if len(day.Activities) > 0 {
				fmt.Println()
				w = tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
				for _, a := range day.Activities {
					desc := a.Description
					if a.TimerRunning() {
						desc = "⏱ " + desc
					}
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", timeutil.FormatSeconds(a.Seconds), a.Project.Name, a.Task.Name, oneLine(desc, 60))
				}
				w.Flush()
			}
			if len(day.Presences) == 0 && len(day.Activities) > 0 {
				fmt.Println()
				fmt.Println("Note: activities logged without a presence today.")
			}
			return nil
		},
	}
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
