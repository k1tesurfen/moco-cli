package cli

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

// Shell completion only reads local files (config and the project cache); pressing Tab never
// talks to MOCO.

// setupCompletion registers completions on the finished command tree: every -p/-t flag, alias
// names as the first argument of `log` and `timer start`, and the arguments of a few subcommands.
func setupCompletion(root *cobra.Command) {
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		if c.Flags().Lookup("project") != nil {
			_ = c.RegisterFlagCompletionFunc("project", completeProjects)
		}
		if c.Flags().Lookup("task") != nil {
			_ = c.RegisterFlagCompletionFunc("task", completeTasks)
		}
		switch c.CommandPath() {
		case "moco log", "moco timer start":
			c.ValidArgsFunction = firstArg(completeAliases)
		case "moco alias rm":
			c.ValidArgsFunction = firstArg(completeAliases)
		case "moco pause clear":
			c.ValidArgsFunction = firstArg(completePauses)
		case "moco queue drop":
			c.ValidArgsFunction = firstArg(completeQueue)
		}
		for _, sub := range c.Commands() {
			walk(sub)
		}
	}
	walk(root)
}

type completeFunc = func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective)

// firstArg completes only the first positional argument.
func firstArg(f completeFunc) completeFunc {
	return func(cmd *cobra.Command, args []string, toComplete string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return f(cmd, args, toComplete)
	}
}

func completeAliases(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for name, a := range cfg.Aliases {
		out = append(out, name+"\t"+a.Project+" / "+a.Task)
	}
	sort.Strings(out)
	return out, cobra.ShellCompDirectiveNoFileComp
}

func cachedProjects() []api.Project {
	st, err := store.New().Load()
	if err != nil || st.Projects == nil {
		return nil
	}
	return st.Projects.Items
}

func completeProjects(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	var out []string
	for _, p := range cachedProjects() {
		out = append(out, p.Name+"\t"+p.Customer.Name+" · "+p.Identifier)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completeTasks(cmd *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	query, _ := cmd.Flags().GetString("project")
	if strings.TrimSpace(query) == "" {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	p, err := service.ResolveProject(cachedProjects(), query)
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, t := range service.ActiveTasks(p) {
		out = append(out, t.Name)
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}

func completePauses(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	st, err := store.New().Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return st.Pauses, cobra.ShellCompDirectiveNoFileComp
}

func completeQueue(_ *cobra.Command, _ []string, _ string) ([]string, cobra.ShellCompDirective) {
	st, err := store.New().Load()
	if err != nil {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	var out []string
	for _, it := range st.Queue {
		out = append(out, fmt.Sprintf("%d\t%s", it.ID, it.Summary()))
	}
	return out, cobra.ShellCompDirectiveNoFileComp
}
