package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/secrets"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

func loginCmd() *cobra.Command {
	var subdomain string
	cmd := &cobra.Command{
		Use:   "login",
		Short: "Store your MOCO subdomain and personal API token",
		Long: `Asks for your MOCO subdomain and personal API token, verifies them against MOCO
and stores the token in the macOS Keychain. Find the token in MOCO under
Profile → Integrations → Personal API key.

If stdin is not a terminal, the token is read from the first line of stdin.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			in := bufio.NewReader(os.Stdin)
			tty := term.IsTerminal(int(os.Stdin.Fd()))

			if subdomain == "" {
				subdomain = cfg.Subdomain
				if tty {
					prompt := "MOCO subdomain (the part before .mocoapp.com)"
					if subdomain != "" {
						prompt += " [" + subdomain + "]"
					}
					fmt.Print(prompt + ": ")
					line, _ := in.ReadString('\n')
					if line = strings.TrimSpace(line); line != "" {
						subdomain = line
					}
				}
			}
			subdomain = strings.TrimSuffix(strings.TrimSpace(subdomain), ".mocoapp.com")
			if subdomain == "" {
				return fmt.Errorf("no subdomain given (use --subdomain)")
			}

			var token string
			if tty {
				fmt.Print("Personal API token (input hidden): ")
				b, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Println()
				if err != nil {
					return err
				}
				token = string(b)
			} else {
				token, _ = in.ReadString('\n')
			}
			token = strings.TrimSpace(token)
			if token == "" {
				return fmt.Errorf("no token given")
			}

			client := api.New(subdomain, token)
			sess, err := client.Session(cmd.Context())
			if err != nil {
				return fmt.Errorf("could not verify the token: %w", err)
			}
			if err := secrets.SetToken(subdomain, token); err != nil {
				return err
			}
			if err := config.SetSubdomain(subdomain); err != nil {
				return err
			}
			err = store.New().Update(func(s *store.State) error {
				s.Me = &store.Me{Subdomain: subdomain, UserID: sess.ID}
				s.Projects = nil
				return nil
			})
			if err != nil {
				return err
			}
			fmt.Printf("Logged in to %s.mocoapp.com (user id %d). Token stored in the Keychain.\n", subdomain, sess.ID)
			fmt.Printf("Config: %s\n", config.Path())
			return nil
		},
	}
	cmd.Flags().StringVar(&subdomain, "subdomain", "", "MOCO subdomain, e.g. artismedia")
	return cmd
}

func logoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout",
		Short: "Remove the API token from the Keychain",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load()
			if err != nil {
				return err
			}
			if cfg.Subdomain == "" {
				fmt.Println("Not logged in.")
				return nil
			}
			if err := secrets.DeleteToken(cfg.Subdomain); err != nil {
				return err
			}
			err = store.New().Update(func(s *store.State) error {
				s.Me = nil
				s.Projects = nil
				return nil
			})
			if err != nil {
				return err
			}
			fmt.Printf("Logged out of %s.mocoapp.com. Token removed from the Keychain.\n", cfg.Subdomain)
			return nil
		},
	}
}
