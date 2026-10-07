// Package cli wires the cobra commands.
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/secrets"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

type globalFlags struct {
	json bool
}

var flags globalFlags

// Execute runs the moco command tree.
func Execute() int {
	root := &cobra.Command{
		Use:           "moco",
		Short:         "Log working time in MOCO from the terminal",
		SilenceUsage:  true,
		SilenceErrors: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The full TUI comes in a later phase.
			return cmd.Help()
		},
	}
	root.PersistentFlags().BoolVar(&flags.json, "json", false, "machine-readable output")
	root.AddCommand(loginCmd(), logoutCmd(), statusCmd(), projectsCmd(),
		startCmd(), breakCmd(), stopCmd(), presenceCmd(),
		logCmd(), listCmd(), editCmd(), deleteCmd(), aliasCmd(), timerCmd(), queueCmd(), notifierCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		if api.IsUnreachable(err) {
			fmt.Fprintln(os.Stderr, "Nothing was changed in MOCO.")
			if st, err := store.New().Load(); err == nil {
				if pending, _ := st.QueueCounts(); pending > 0 {
					fmt.Fprintf(os.Stderr, "%d %s waiting in the offline queue (`moco queue`).\n", pending, plural(pending, "entry is", "entries are"))
				}
			}
		}
		return 1
	}
	return 0
}

// newService builds a Service for the logged-in user and first sends any queued writes, so they
// reach MOCO before (and in order with) whatever the command does.
func newService(ctx context.Context) (*service.Service, error) {
	svc, err := newServiceNoSync(ctx)
	if err == nil {
		autoSync(ctx, svc)
	}
	return svc, err
}

// newServiceNoSync builds a Service without touching the queue. The user id is cached in the state file.
func newServiceNoSync(ctx context.Context) (*service.Service, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if cfg.Subdomain == "" {
		return nil, errors.New("not logged in — run `moco login`")
	}
	token, err := secrets.GetToken(cfg.Subdomain)
	if err != nil {
		return nil, err
	}
	st := store.New()
	svc := &service.Service{
		Cfg:   cfg,
		API:   api.New(cfg.Subdomain, token),
		Store: st,
		Now:   time.Now,
	}
	state, err := st.Load()
	if err != nil {
		return nil, err
	}
	if state.Me != nil && state.Me.Subdomain == cfg.Subdomain {
		svc.UserID = state.Me.UserID
		return svc, nil
	}
	sess, err := svc.API.Session(ctx)
	if err != nil {
		return nil, err
	}
	svc.UserID = sess.ID
	return svc, st.Update(func(s *store.State) error {
		s.Me = &store.Me{Subdomain: cfg.Subdomain, UserID: sess.ID}
		return nil
	})
}

func printJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
