package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/service"
)

func projectsCmd() *cobra.Command {
	var withTasks, refresh bool
	cmd := &cobra.Command{
		Use:     "projects [filter]",
		Aliases: []string{"project"},
		Short:   "List your assigned projects (cached for a day); join or leave projects",
		Args:    cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			projects, err := svc.Projects(cmd.Context(), refresh)
			if err != nil {
				return err
			}
			if len(args) == 1 {
				projects = service.FindProjects(projects, args[0])
			}
			if flags.json {
				return printJSON(projects)
			}
			if len(projects) == 0 {
				fmt.Println("No matching projects.")
				return nil
			}
			w := newTable(os.Stdout)
			for _, p := range projects {
				fmt.Fprintf(w, "%s\t%s\t%s\n", p.Identifier, p.Name, p.Customer.Name)
				if withTasks {
					for _, t := range p.Tasks {
						if t.Active {
							fmt.Fprintf(w, "\t  · %s\t\n", t.Name)
						}
					}
				}
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVar(&withTasks, "tasks", false, "also list each project's active tasks")
	cmd.Flags().BoolVar(&refresh, "refresh", false, "reload from MOCO instead of the cache")
	cmd.AddCommand(projectJoinCmd(), projectLeaveCmd())
	return cmd
}

func projectJoinCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "join <search>",
		Short: "Add yourself to the team of a project, so you can book time on it",
		Long: `Add yourself to the team of a company project you are not on yet – like "add me" in
the MOCO web app. <search> matches the identifier, name or customer of all active projects.`,
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			query := strings.Join(args, " ")
			joinable, err := svc.JoinableProjects(cmd.Context())
			if err != nil {
				return err
			}
			p, err := service.ResolveJoinable(joinable, query)
			if err != nil {
				mine, _ := svc.Projects(cmd.Context(), true)
				if found := service.FindProjects(mine, query); len(found) == 1 {
					return fmt.Errorf("you are already on %s", found[0].Name)
				}
				return err
			}
			joined, err := svc.JoinProject(cmd.Context(), p)
			if err != nil {
				return err
			}
			if flags.json {
				return printJSON(joined)
			}
			fmt.Printf("Joined %s %s (%s). Bookable tasks:\n", joined.Identifier, joined.Name, joined.Customer.Name)
			for _, t := range service.ActiveTasks(joined) {
				fmt.Printf("  · %s\n", t.Name)
			}
			return nil
		},
	}
}

func projectLeaveCmd() *cobra.Command {
	var yes bool
	cmd := &cobra.Command{
		Use:   "leave <search>",
		Short: "Remove yourself from the team of a project (only without booked hours)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			svc, err := newService(cmd.Context())
			if err != nil {
				return err
			}
			projects, err := svc.Projects(cmd.Context(), true)
			if err != nil {
				return err
			}
			p, err := service.ResolveProject(projects, strings.Join(args, " "))
			if err != nil {
				return err
			}
			if !yes && !confirm(fmt.Sprintf("Leave %s %s?", p.Identifier, p.Name)) {
				return nil
			}
			if err := svc.LeaveProject(cmd.Context(), p); err != nil {
				return err
			}
			fmt.Printf("Left %s %s.\n", p.Identifier, p.Name)
			return nil
		},
	}
	cmd.Flags().BoolVarP(&yes, "yes", "y", false, "don't ask for confirmation")
	return cmd
}
