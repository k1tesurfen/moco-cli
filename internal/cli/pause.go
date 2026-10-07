package cli

import (
	"errors"
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

func pauseCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "pause <today|date|until date>",
		Short: "Take days off: no reminders on those days",
		Long: `Days off get no reminders. Weekends (non-workdays) never get any.

  moco pause today
  moco pause fri
  moco pause 2026-12-24
  moco pause until 2026-10-16     # every workday from today to that date
  moco pause list
  moco pause clear 2026-12-24`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			now := time.Now()
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			var dates []string
			if args[0] == "until" {
				if len(args) != 2 {
					return errors.New("usage: moco pause until <date>")
				}
				end, err := timeutil.ParseFutureDate(args[1], now)
				if err != nil {
					return err
				}
				today, _ := timeutil.ParseFutureDate("today", now)
				for d := today; !d.After(end); d = d.AddDate(0, 0, 1) {
					if cfg.IsWorkday(d) {
						dates = append(dates, timeutil.Date(d))
					}
				}
				if len(dates) == 0 {
					return fmt.Errorf("no workdays until %s", timeutil.Date(end))
				}
			} else {
				if len(args) != 1 {
					return errors.New("usage: moco pause <today|date>")
				}
				d, err := timeutil.ParseFutureDate(args[0], now)
				if err != nil {
					return err
				}
				if timeutil.Date(d) < timeutil.Date(now) {
					return fmt.Errorf("%s is in the past", timeutil.Date(d))
				}
				dates = []string{timeutil.Date(d)}
			}
			err = store.New().Update(func(st *store.State) error {
				st.Pause(timeutil.Date(now), dates...)
				return nil
			})
			if err != nil {
				return err
			}
			if len(dates) == 1 {
				fmt.Printf("Paused %s — no reminders that day.\n", dates[0])
			} else {
				fmt.Printf("Paused %d workdays, %s to %s.\n", len(dates), dates[0], dates[len(dates)-1])
			}
			return nil
		},
	}
	cmd.AddCommand(&cobra.Command{
		Use:   "list",
		Short: "List days off",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := store.New().Load()
			if err != nil {
				return err
			}
			today := timeutil.Date(time.Now())
			n := 0
			for _, d := range st.Pauses {
				if d >= today {
					t, _ := time.ParseInLocation(timeutil.DateLayout, d, time.Local)
					fmt.Println(t.Format("Mon 2 Jan 2006"))
					n++
				}
			}
			if n == 0 {
				fmt.Println("No days off planned.")
			}
			return nil
		},
	}, &cobra.Command{
		Use:   "clear <date>",
		Short: "Remove a day off",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			d, err := timeutil.ParseFutureDate(args[0], time.Now())
			if err != nil {
				return err
			}
			found := false
			err = store.New().Update(func(st *store.State) error {
				found = st.Unpause(timeutil.Date(d))
				return nil
			})
			if err != nil {
				return err
			}
			if !found {
				return fmt.Errorf("%s is not a day off", timeutil.Date(d))
			}
			fmt.Printf("%s is a workday again.\n", timeutil.Date(d))
			if timeutil.Date(d) == timeutil.Date(time.Now()) {
				fmt.Println("Reminders already skipped today stay skipped.")
			}
			return nil
		},
	})
	return cmd
}
