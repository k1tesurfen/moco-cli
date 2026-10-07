package cli

import (
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

func projectsCmd() *cobra.Command {
	var withTasks, refresh bool
	cmd := &cobra.Command{
		Use:   "projects [filter]",
		Short: "List your assigned projects (cached for a day)",
		Args:  cobra.MaximumNArgs(1),
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
				projects = filterProjects(projects, args[0])
			}
			if flags.json {
				return printJSON(projects)
			}
			if len(projects) == 0 {
				fmt.Println("No matching projects.")
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
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
	return cmd
}

// filterProjects keeps projects whose name, customer or identifier contain every word of the filter.
func filterProjects(projects []api.Project, filter string) []api.Project {
	words := strings.Fields(strings.ToLower(filter))
	var out []api.Project
	for _, p := range projects {
		hay := strings.ToLower(p.Name + " " + p.Customer.Name + " " + p.Identifier)
		ok := true
		for _, w := range words {
			if !strings.Contains(hay, w) {
				ok = false
				break
			}
		}
		if ok {
			out = append(out, p)
		}
	}
	return out
}
