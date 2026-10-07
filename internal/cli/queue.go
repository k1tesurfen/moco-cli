package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"

	"github.com/charmbracelet/lipgloss"
	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

var (
	stderrStyle = lipgloss.NewRenderer(os.Stderr)
	alarm       = stderrStyle.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
	okStyle     = stderrStyle.NewStyle().Foreground(lipgloss.Color("2"))
	alarmOut    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true) // for stdout
)

// queued reports whether err means the write went into the offline queue, and if so prints the
// unmistakable warning. Callers then return nil: the entry is safe, just not in MOCO yet.
func queued(err error) bool {
	var q *service.QueuedError
	if !errors.As(err, &q) {
		return false
	}
	fmt.Fprintln(os.Stderr, alarm.Render("⚠  MOCO NOT REACHABLE — NOT SAVED IN MOCO YET"))
	fmt.Fprintf(os.Stderr, "   Queued #%d: %s\n", q.Item.ID, q.Item.Summary())
	fmt.Fprintf(os.Stderr, "   Cause: %v\n", q.Cause)
	fmt.Fprintf(os.Stderr, "   It is sent with your next moco command, or now with `moco queue sync`.\n")
	if q.Pending > 1 {
		fmt.Fprintf(os.Stderr, "   %d entries are waiting in the queue (`moco queue`).\n", q.Pending)
	}
	return true
}

// autoSync sends pending queue items before a command talks to MOCO. It stays quiet while MOCO
// is still unreachable; the command itself will say so.
func autoSync(ctx context.Context, svc *service.Service) {
	st, err := svc.Store.Load()
	if err != nil {
		return
	}
	pending, failed := st.QueueCounts()
	if pending > 0 {
		res, err := svc.SyncQueue(ctx, false)
		if err != nil {
			fmt.Fprintln(os.Stderr, "Warning: queue sync:", err)
			return
		}
		printSync(res)
		failed = res.Failed
	}
	if failed > 0 {
		fmt.Fprintln(os.Stderr, alarm.Render(fmt.Sprintf("⚠  %d queued %s rejected by MOCO and NOT saved — see `moco queue`",
			failed, plural(failed, "entry was", "entries were"))))
	}
}

func printSync(res service.SyncResult) {
	for _, it := range res.Sent {
		fmt.Fprintln(os.Stderr, okStyle.Render(fmt.Sprintf("Synced queued #%d to MOCO: %s", it.ID, it.Summary())))
	}
	for _, it := range res.Rejected {
		fmt.Fprintln(os.Stderr, alarm.Render(fmt.Sprintf("⚠  MOCO rejected queued #%d: %s", it.ID, it.Summary())))
		fmt.Fprintf(os.Stderr, "   %s\n", it.LastError)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

func queueCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "queue",
		Short: "Show writes waiting for MOCO (offline queue)",
		Long: `Writes that could not reach MOCO (network down, MOCO server errors) are queued locally
and sent automatically with the next moco command. Entries MOCO rejects stay in the queue as
failed until you retry them (moco queue sync) or drop them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return queueList() },
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List queued writes",
		Args:  cobra.NoArgs,
		RunE:  func(cmd *cobra.Command, args []string) error { return queueList() },
	}, &cobra.Command{
		Use:   "sync",
		Short: "Send queued writes now, retrying failed ones",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newServiceNoSync(cmd.Context())
			if err != nil {
				return err
			}
			res, err := svc.SyncQueue(cmd.Context(), true)
			if err != nil {
				return err
			}
			if res.Busy {
				return errors.New("another moco process is syncing the queue right now — try again in a moment")
			}
			printSync(res)
			switch {
			case res.Stopped != nil:
				return fmt.Errorf("%d %s still queued — %w", res.Pending+res.Failed, plural(res.Pending+res.Failed, "entry", "entries"), res.Stopped)
			case res.Failed > 0:
				return fmt.Errorf("%d queued %s rejected by MOCO — fix it in MOCO and `moco queue drop <#>`, or retry later",
					res.Failed, plural(res.Failed, "entry was", "entries were"))
			case len(res.Sent) == 0:
				fmt.Println("Queue is empty.")
			default:
				fmt.Println("Queue is empty, everything is in MOCO.")
			}
			return nil
		},
	}, queueDropCmd())
	return cmd
}

func queueList() error {
	items, err := store.New().Load()
	if err != nil {
		return err
	}
	if flags.json {
		return printJSON(items.Queue)
	}
	if len(items.Queue) == 0 {
		fmt.Println("Queue is empty.")
		return nil
	}
	w := newTable(os.Stdout)
	fmt.Fprintln(w, outHead.Render("#\tQUEUED\tSTATE\tENTRY"))
	for _, it := range items.Queue {
		state := "pending"
		if it.Failed {
			state = alarmOut.Render("FAILED")
		} else if it.Attempts > 0 {
			state = fmt.Sprintf("pending (%d tries)", it.Attempts)
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\n", it.ID, it.QueuedAt.Local().Format("Mon 2 Jan 15:04"), state, it.Summary())
		if it.LastError != "" {
			fmt.Fprintf(w, "\t\t\t↳ %s\n", oneLine(it.LastError, 100))
		}
	}
	return w.Flush()
}

func queueDropCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "drop <#>",
		Short: "Remove a queued write without sending it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("invalid queue number %q", args[0])
			}
			st, err := store.New().Load()
			if err != nil {
				return err
			}
			it := st.QueueItem(id)
			if it == nil {
				return fmt.Errorf("no queued item #%d", id)
			}
			if !yes && !confirm(fmt.Sprintf("Drop #%d (%s)? It will never reach MOCO.", id, it.Summary())) {
				return errors.New("aborted")
			}
			dropped, err := store.New().Drop(id)
			if err != nil {
				return err
			}
			fmt.Printf("Dropped #%d: %s\n", dropped.ID, dropped.Summary())
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}
