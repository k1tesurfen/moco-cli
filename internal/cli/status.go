package cli

import (
	"fmt"
	"os"
	"strings"

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
			st, err := svc.Store.Load()
			if err != nil {
				return err
			}
			pending, failed := st.QueueCounts()
			day, err := svc.Day(cmd.Context(), now)
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(map[string]any{
					"queue_pending":   pending,
					"queue_failed":    failed,
					"date":            day.Date,
					"presences":       day.Presences,
					"activities":      day.Activities,
					"present_seconds": day.PresentSeconds,
					"logged_seconds":  day.LoggedSeconds,
					"gap_seconds":     day.Gap(),
					"running_timer":   day.RunningTimer,
				})
			}

			fmt.Println(outTitle.Render(now.Format("Monday, 2 Jan 2006")))
			fmt.Println()
			w := newTable(os.Stdout)

			if len(day.Presences) == 0 {
				fmt.Fprintln(w, outHead.Render("Presence")+"\t"+outMuted.Render("none recorded — `moco start`"))
			}
			for i, p := range day.Presences {
				label := ""
				if i == 0 {
					label = outHead.Render("Presence")
				}
				fmt.Fprintf(w, "%s\t%s–%s  %s\n", label, p.From, orDots(p.To), outMuted.Render("("+where(p.IsHomeOffice)+")"))
			}
			fmt.Fprintf(w, outHead.Render("Present")+"\t%s\n", timeutil.FormatSeconds(day.PresentSeconds))
			fmt.Fprintf(w, outHead.Render("Logged")+"\t%s\n", timeutil.FormatSeconds(day.LoggedSeconds))
			switch gap := day.Gap(); {
			case gap > 0:
				fmt.Fprintf(w, "%s\t%s\n", outHead.Render("Missing"), outErr.Render(timeutil.FormatSeconds(gap)))
			case gap < 0:
				fmt.Fprintf(w, "%s\t%s\n", outHead.Render("Over"), outWarn.Render(timeutil.FormatSeconds(-gap)+" logged beyond presence"))
			case len(day.Presences) > 0:
				fmt.Fprintf(w, "%s\t%s\n", outHead.Render("Missing"), outOK.Render("nothing — complete ✓"))
			}
			if t := day.RunningTimer; t != nil {
				fmt.Fprintf(w, "%s\t%s %s since %s\n", outHead.Render("Timer"), outOK.Render("⏱ running on"),
					outProject.Render(t.Project.Name+" / "+t.Task.Name), t.TimerStartedAt.In(now.Location()).Format("15:04"))
			}
			w.Flush()
			printQueueLine(pending, failed)

			if len(day.Activities) > 0 {
				fmt.Println()
				w = newTable(os.Stdout)
				for _, a := range day.Activities {
					sec, desc := shownActivity(a, now, 60)
					fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", timeutil.FormatSeconds(sec), outProject.Render(a.Project.Name), outProject.Render(a.Task.Name), desc)
				}
				w.Flush()
			}
			if len(day.Presences) == 0 && len(day.Activities) > 0 {
				fmt.Println()
				fmt.Println(outWarn.Render("Note: activities logged without a presence today."))
			}
			return nil
		},
	}
}

// printQueueLine mentions the offline queue if it is not empty.
func printQueueLine(pending, failed int) {
	if pending > 0 {
		fmt.Println(alarmOut.Render(fmt.Sprintf("Queue: %d %s NOT in MOCO yet — `moco queue`", pending, plural(pending, "entry", "entries"))))
	}
	if failed > 0 {
		fmt.Println(alarmOut.Render(fmt.Sprintf("Queue: %d %s rejected by MOCO — `moco queue`", failed, plural(failed, "entry", "entries"))))
	}
}

func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		return string(r[:max-1]) + "…"
	}
	return s
}
