package cli

import (
	"errors"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/tui"
)

func uiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "ui",
		Short: "Open the full-screen interface (same as `moco` without arguments)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !isTTY() {
				return errors.New("the interface needs a terminal")
			}
			return runUI(cmd)
		},
	}
}

func runUI(cmd *cobra.Command) error {
	svc, err := newService(cmd.Context())
	if err != nil {
		return err
	}
	return tui.Run(cmd.Context(), svc)
}
