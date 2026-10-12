package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

// notifierSocket is where MocoNotifier listens.
func notifierSocket() string { return filepath.Join(store.Dir(), "notifier.sock") }

func dialNotifier(ctx context.Context) (*notify.Client, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return notify.Dial(ctx, notifierSocket(), notify.DefaultApp())
}

// notifierCmd is a debugging aid for the notification helper; the daemon is its real user.
func notifierCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:    "notifier",
		Short:  "Check the notification helper (MocoNotifier.app)",
		Hidden: true,
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Start the helper if needed and show its notification permission",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dialNotifier(cmd.Context())
			if err != nil {
				return err
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(cmd.Context(), 5*time.Second)
			defer cancel()
			st, err := c.Ping(ctx)
			if err != nil {
				return err
			}
			fmt.Printf("MocoNotifier %s on %s · notifications: %s\n", st.Version, notifierSocket(), st.Authorization)
			if st.Authorization != "authorized" {
				fmt.Println("Allow notifications for \"MOCO Reminders\" in System Settings → Notifications.")
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "test",
		Short: "Show a sample start question and print the answer (waits 2 minutes)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := dialNotifier(cmd.Context())
			if err != nil {
				return err
			}
			defer c.Close()
			ctx, cancel := context.WithTimeout(cmd.Context(), 2*time.Minute)
			defer cancel()
			n := notify.Notification{
				ID: fmt.Sprintf("test-%d", time.Now().Unix()), Category: "test",
				Title: "Did you start working?", Body: "Test from `moco notifier test` — nothing is saved.",
				Sound: true,
				Actions: []notify.Action{
					{ID: "yes_0800", Title: "Yes, 08:00"},
					{ID: "yes_now", Title: "Yes, now"},
					{ID: "other", Title: "Other time…", Input: true, Placeholder: "HH:MM", Button: "Set"},
					{ID: "not_yet", Title: "Not yet"},
				},
			}
			if err := c.Notify(ctx, n); err != nil {
				return err
			}
			fmt.Println("Notification shown — answer it (waiting up to 2 minutes) …")
			for {
				var r notify.Response
				var ok bool
				select {
				case r, ok = <-c.Responses:
				case <-ctx.Done():
					c.Remove(n.ID)
					return fmt.Errorf("no answer within 2 minutes")
				}
				if !ok {
					return fmt.Errorf("the notifier closed the connection")
				}
				if r.ID != n.ID {
					continue // an answer to the daemon's notifications
				}
				switch {
				case r.Dismissed:
					fmt.Println("Dismissed.")
				case r.Text != "":
					fmt.Printf("Answer: %s, text %q\n", r.Action, r.Text)
				default:
					fmt.Printf("Answer: %s\n", r.Action)
				}
				return nil
			}
		},
	}, &cobra.Command{
		Use:   "quit",
		Short: "Stop the helper",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := notify.Dial(cmd.Context(), notifierSocket(), "")
			if err != nil {
				fmt.Println("Not running.")
				return nil
			}
			defer c.Close()
			return c.Quit()
		},
	})
	return cmd
}
