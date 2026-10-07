package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"text/template"
	"time"

	"github.com/spf13/cobra"

	"github.com/k1tesurfen/moco-cli/internal/daemon"
	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

const agentLabel = "de.artismedia.moco.daemon"

func agentPlist() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "LaunchAgents", agentLabel+".plist")
}

func daemonLog() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Library", "Logs", "moco", "daemon.log")
}

func guiDomain() string   { return "gui/" + strconv.Itoa(os.Getuid()) }
func agentTarget() string { return guiDomain() + "/" + agentLabel }

func launchctl(args ...string) (string, error) {
	out, err := exec.Command("/bin/launchctl", args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("launchctl %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

// agentPID returns the pid of the running agent (0 if loaded but not running); loaded is false
// if launchd doesn't know the agent.
func agentPID() (pid int, loaded bool) {
	out, err := exec.Command("/bin/launchctl", "print", agentTarget()).Output()
	if err != nil {
		return 0, false
	}
	if m := regexp.MustCompile(`(?m)^\s*pid = (\d+)`).FindSubmatch(out); m != nil {
		pid, _ = strconv.Atoi(string(m[1]))
	}
	return pid, true
}

var plistTemplate = template.Must(template.New("plist").Parse(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>{{.Label}}</string>
	<key>ProgramArguments</key>
	<array>
		<string>{{.Program}}</string>
		<string>daemon</string>
		<string>run</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
	<key>KeepAlive</key>
	<true/>
	<key>ThrottleInterval</key>
	<integer>30</integer>
	<key>StandardOutPath</key>
	<string>{{.Log}}</string>
	<key>StandardErrorPath</key>
	<string>{{.Log}}</string>
</dict>
</plist>
`))

func daemonCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "daemon",
		Short: "Background reminders (LaunchAgent): install, start, stop, status, logs, test",
	}
	cmd.AddCommand(daemonRunCmd(), daemonInstallCmd(), daemonUninstallCmd(), daemonStartCmd(),
		daemonStopCmd(), daemonStatusCmd(), daemonLogsCmd(), daemonTestCmd())
	return cmd
}

func daemonRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "run",
		Short:  "Run the daemon in the foreground (this is what the LaunchAgent starts)",
		Args:   cobra.NoArgs,
		Hidden: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx, stop := signal.NotifyContext(cmd.Context(), syscall.SIGTERM, syscall.SIGINT)
			defer stop()
			dm := &daemon.Daemon{
				NewService: newServiceNoSync,
				Store:      store.New(),
				Socket:     notifierSocket(),
				App:        notify.DefaultApp(),
				Now:        time.Now,
				Log:        log.New(os.Stderr, "", log.LstdFlags),
			}
			return dm.Run(ctx)
		},
	}
}

func daemonInstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "install",
		Short: "Install and start the LaunchAgent (runs at login)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			exe, err := os.Executable()
			if err != nil {
				return err
			}
			if exe, err = filepath.EvalSymlinks(exe); err != nil {
				return err
			}
			if strings.Contains(exe, "go-build") {
				return errors.New("run install from the installed binary (`make install`), not via `go run`")
			}
			if _, err := os.Stat(notify.DefaultApp()); err != nil {
				return fmt.Errorf("%s is missing — run `make install` first", notify.DefaultApp())
			}
			if err := os.MkdirAll(filepath.Dir(daemonLog()), 0o755); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(agentPlist()), 0o755); err != nil {
				return err
			}
			var buf bytes.Buffer
			if err := plistTemplate.Execute(&buf, map[string]string{"Label": agentLabel, "Program": exe, "Log": daemonLog()}); err != nil {
				return err
			}
			if err := os.WriteFile(agentPlist(), buf.Bytes(), 0o644); err != nil {
				return err
			}
			if _, loaded := agentPID(); loaded {
				launchctl("bootout", agentTarget())
			}
			if _, err := launchctl("bootstrap", guiDomain(), agentPlist()); err != nil {
				return err
			}
			fmt.Printf("Installed %s\n  runs %s daemon run\n  log  %s\n", agentPlist(), exe, daemonLog())
			return waitRunning()
		},
	}
}

func waitRunning() error {
	for i := 0; i < 20; i++ {
		if pid, _ := agentPID(); pid > 0 {
			fmt.Printf("Daemon running (pid %d).\n", pid)
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("the daemon did not start — see `moco daemon logs`")
}

func daemonUninstallCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "uninstall",
		Short: "Stop and remove the LaunchAgent",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, loaded := agentPID(); loaded {
				if _, err := launchctl("bootout", agentTarget()); err != nil {
					return err
				}
			}
			if err := os.Remove(agentPlist()); err != nil && !errors.Is(err, os.ErrNotExist) {
				return err
			}
			fmt.Println("LaunchAgent removed; no more reminders.")
			return nil
		},
	}
}

func daemonStartCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "start",
		Short: "Start (or restart) the installed daemon",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(agentPlist()); err != nil {
				return errors.New("not installed — run `moco daemon install`")
			}
			if _, loaded := agentPID(); loaded {
				if _, err := launchctl("kickstart", "-k", agentTarget()); err != nil {
					return err
				}
			} else if _, err := launchctl("bootstrap", guiDomain(), agentPlist()); err != nil {
				return err
			}
			return waitRunning()
		},
	}
}

func daemonStopCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "stop",
		Short: "Stop the daemon until the next login (or `moco daemon start`)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, loaded := agentPID(); !loaded {
				fmt.Println("Not running.")
				return nil
			}
			if _, err := launchctl("bootout", agentTarget()); err != nil {
				return err
			}
			fmt.Println("Daemon stopped. It starts again at the next login or with `moco daemon start`.")
			return nil
		},
	}
}

func daemonStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show whether the daemon runs and today's reminders",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			pid, loaded := agentPID()
			_, installErr := os.Stat(agentPlist())
			switch {
			case pid > 0:
				fmt.Printf("Daemon running (pid %d).\n", pid)
			case loaded:
				fmt.Println(alarmOut.Render("Daemon loaded but not running — see `moco daemon logs`."))
			case installErr == nil:
				fmt.Println("Daemon installed but stopped — `moco daemon start`.")
			default:
				fmt.Println("Daemon not installed — `moco daemon install`.")
			}

			ctx, cancel := context.WithTimeout(cmd.Context(), 3*time.Second)
			defer cancel()
			if c, err := notify.Dial(ctx, notifierSocket(), ""); err != nil {
				fmt.Println("Notifier: not running (the daemon starts it).")
			} else {
				if st, err := c.Ping(ctx); err == nil {
					fmt.Printf("Notifier: running · notifications %s\n", st.Authorization)
				}
				c.Close()
			}

			st, err := store.New().Load()
			if err != nil {
				return err
			}
			now := time.Now()
			today := now.Format("2006-01-02")
			if st.Paused(today) {
				fmt.Println("Today is paused (day off).")
			}
			if d := st.Daemon; d != nil && d.Date == today {
				fmt.Println()
				for _, ev := range daemon.Events {
					e := d.Events[string(ev)]
					state := "at " + e.Next.Format("15:04")
					switch {
					case e.Shown:
						state = "on screen, unanswered"
						if !e.Done {
							state += " · asked again at " + e.Next.Format("15:04")
						}
					case e.Done && e.Fired > 0:
						state = "done"
					case e.Done:
						state = "skipped"
					case e.Fired > 0:
						state = "asked again at " + e.Next.Format("15:04")
					}
					fmt.Printf("  %-14s %s\n", ev, state)
				}
			}
			return nil
		},
	}
}

func daemonLogsCmd() *cobra.Command {
	var follow bool
	var lines int
	cmd := &cobra.Command{
		Use:   "logs",
		Short: "Show the daemon log",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if _, err := os.Stat(daemonLog()); err != nil {
				return fmt.Errorf("no log yet at %s", daemonLog())
			}
			a := []string{"-n", strconv.Itoa(lines)}
			if follow {
				a = append(a, "-f")
			}
			c := exec.CommandContext(cmd.Context(), "/usr/bin/tail", append(a, daemonLog())...)
			c.Stdout, c.Stderr = os.Stdout, os.Stderr
			return c.Run()
		},
	}
	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing new lines")
	cmd.Flags().IntVarP(&lines, "lines", "n", 40, "number of lines")
	return cmd
}

func daemonTestCmd() *cobra.Command {
	var names []string
	for _, ev := range daemon.Events {
		names = append(names, string(ev))
	}
	return &cobra.Command{
		Use:       "test <" + strings.Join(names, "|") + ">",
		Short:     "Show a reminder now (dry run: answers are described, nothing is saved)",
		Args:      cobra.ExactArgs(1),
		ValidArgs: names,
		RunE: func(cmd *cobra.Command, args []string) error {
			ev := args[0]
			if !strings.Contains(" "+strings.Join(names, " ")+" ", " "+ev+" ") {
				return fmt.Errorf("unknown reminder %q — one of %s", ev, strings.Join(names, ", "))
			}
			data, err := os.ReadFile(daemon.PIDFile())
			if err != nil {
				return errors.New("the daemon is not running — `moco daemon start`")
			}
			pid, _ := strconv.Atoi(strings.TrimSpace(string(data)))
			if pid <= 0 || syscall.Kill(pid, 0) != nil {
				return errors.New("the daemon is not running — `moco daemon start`")
			}
			if err := store.New().Update(func(st *store.State) error { st.DaemonTest = ev; return nil }); err != nil {
				return err
			}
			if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
				return err
			}
			fmt.Printf("Asked the daemon to show %q. Answers are a dry run — nothing is saved.\n", ev)
			return nil
		},
	}
}
