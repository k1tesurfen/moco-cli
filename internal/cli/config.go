package cli

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/config"
)

func configCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Show or change settings (schedule, rounding, location)",
		Long: `Show or change settings in ` + config.Path() + `.

  moco config                              # all settings with their values
  moco config get schedule.start
  moco config set schedule.start 7:30
  moco config set schedule.workdays mon,tue,wed,thu
  moco config set location.fri home        # empty value: back to location.default
  moco config edit                         # open the file in $EDITOR
  moco config path

The daemon picks up changes within a minute; no restart needed.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			values := cfg.Values()
			if flags.json {
				return printJSON(values)
			}
			fmt.Println(outMuted.Render(config.Path()))
			w := newTable(os.Stdout)
			for _, k := range config.Keys {
				v := values[k]
				if v == "" {
					v = outMuted.Render("(location.default)")
				}
				fmt.Fprintf(w, "%s\t%s\n", outHead.Render(k), v)
			}
			return w.Flush()
		},
	}
	keyArgs := func(_ *cobra.Command, args []string, _ string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		return config.Keys, cobra.ShellCompDirectiveNoFileComp
	}
	cmd.AddCommand(
		&cobra.Command{
			Use:   "path",
			Short: "Print the config file path",
			Args:  cobra.NoArgs,
			Run:   func(cmd *cobra.Command, args []string) { fmt.Println(config.Path()) },
		},
		&cobra.Command{
			Use:               "get <key>",
			Short:             "Print one setting",
			Args:              cobra.ExactArgs(1),
			ValidArgsFunction: keyArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				v, ok := cfg.Values()[args[0]]
				if !ok {
					return fmt.Errorf("unknown key %q — `moco config` lists them", args[0])
				}
				fmt.Println(v)
				return nil
			},
		},
		&cobra.Command{
			Use:               "set <key> <value>",
			Short:             "Change one setting (comments in the file are kept)",
			Args:              cobra.RangeArgs(1, 2),
			ValidArgsFunction: keyArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				value := ""
				if len(args) == 2 {
					value = args[1]
				}
				if err := config.Set(args[0], value); err != nil {
					return err
				}
				cfg, err := config.Load()
				if err != nil {
					return err
				}
				fmt.Printf("%s = %s\n", args[0], cfg.Values()[args[0]])
				return nil
			},
		},
		&cobra.Command{
			Use:   "edit",
			Short: "Open the config file in $VISUAL / $EDITOR (vi if unset) and check it afterwards",
			Args:  cobra.NoArgs,
			RunE: func(cmd *cobra.Command, args []string) error {
				path := config.Path()
				if _, err := os.Stat(path); err != nil {
					return errors.New("no config file yet — run `moco login` first")
				}
				editor := os.Getenv("VISUAL")
				if editor == "" {
					editor = os.Getenv("EDITOR")
				}
				if editor == "" {
					editor = "vi"
				}
				parts := strings.Fields(editor)
				c := exec.Command(parts[0], append(parts[1:], path)...)
				c.Stdin, c.Stdout, c.Stderr = os.Stdin, os.Stdout, os.Stderr
				if err := c.Run(); err != nil {
					return fmt.Errorf("%s: %w", editor, err)
				}
				if _, err := config.Load(); err != nil {
					return fmt.Errorf("%w\nThe file was saved like this — fix it with `moco config edit`", err)
				}
				fmt.Println("Config OK.")
				return nil
			},
		},
	)
	return cmd
}
