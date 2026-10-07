package cli

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/wizard"
)

func aliasCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "alias",
		Short: "Short names for project/task pairs, e.g. `moco log dev 1h …`",
	}
	cmd.AddCommand(aliasAddCmd(), aliasListCmd(), aliasRmCmd())
	return cmd
}

func aliasAddCmd() *cobra.Command {
	var project, task string
	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Add or replace an alias (wizard if -p/-t are omitted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := cmd.Context()
			name := args[0]
			if !config.ValidAliasName(name) {
				return fmt.Errorf("invalid alias name %q: use lowercase letters, digits, - and _", name)
			}
			svc, err := newService(ctx)
			if err != nil {
				return err
			}
			projects, err := svc.Projects(ctx, false)
			if err != nil {
				return err
			}
			var p api.Project
			var t api.Task
			switch {
			case project != "":
				if p, err = service.ResolveProject(projects, project); err != nil {
					return err
				}
				if task == "" {
					ts := service.ActiveTasks(p)
					if len(ts) != 1 {
						return errors.New("give the task too (-t)")
					}
					t = ts[0]
				} else if t, err = service.ResolveTask(p, task); err != nil {
					return err
				}
			case task != "":
				return errors.New("-t needs -p")
			default:
				if !isTTY() {
					return errors.New("give -p and -t (no terminal for the wizard)")
				}
				env, err := wizardEnv(svc, projects)
				if err != nil {
					return err
				}
				pick, err := wizard.PickPair(ctx, env)
				if err != nil {
					return err
				}
				p, t = pick.Project, pick.Task
			}
			if err := config.SetAlias(name, config.Alias{Project: p.Name, Task: t.Name}); err != nil {
				return err
			}
			fmt.Printf("Alias %s → %s / %s\n", name, p.Name, t.Name)
			return nil
		},
	}
	cmd.Flags().StringVarP(&project, "project", "p", "", "project")
	cmd.Flags().StringVarP(&task, "task", "t", "", "task")
	return cmd
}

func aliasListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "List aliases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(cfg.Aliases)
			}
			if len(cfg.Aliases) == 0 {
				fmt.Println("No aliases. Add one with `moco alias add <name>`.")
				return nil
			}
			// Check against the project cache only; a broken alias is flagged, not fatal.
			var projects []api.Project
			if svc, err := newService(cmd.Context()); err == nil {
				projects, _ = svc.Projects(cmd.Context(), false)
			}
			names := make([]string, 0, len(cfg.Aliases))
			for n := range cfg.Aliases {
				names = append(names, n)
			}
			sort.Strings(names)
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			for _, n := range names {
				a := cfg.Aliases[n]
				status := ""
				if projects != nil {
					if _, _, err := service.ResolveAlias(projects, n, a); err != nil {
						status = "⚠ " + err.Error()
					}
				}
				fmt.Fprintf(w, "%s\t%s / %s\t%s\n", n, a.Project, a.Task, status)
			}
			return w.Flush()
		},
	}
}

func aliasRmCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "rm <name>",
		Aliases: []string{"remove", "delete"},
		Short:   "Remove an alias",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := config.RemoveAlias(args[0]); err != nil {
				return err
			}
			fmt.Printf("Removed alias %s.\n", args[0])
			return nil
		},
	}
}
