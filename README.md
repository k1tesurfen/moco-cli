# moco-cli

Log your working time in [MOCO](https://www.mocoapp.com/) from the terminal, on macOS.

- **`moco`** – a fullscreen TUI in the style of lazygit: week, presence, activities, projects.
- **One-liners and an inline wizard** – `moco log dev 1h30 Fixed the login form`.
- **A background daemon** – reminds you through native macOS notifications to start the day,
  take your break, log what you did and finish the day. Simple questions are answered right
  in the notification.
- **Offline queue** – if MOCO isn't reachable, writes are queued and sent later; the output says
  so unmistakably.

It works with a personal MOCO API token and only ever touches your own entries.

## Install

Requirements: macOS 26+, Go 1.27+, Xcode command line tools (Swift 6) for the notification helper.

```sh
make install          # ~/.local/bin/moco, ~/Applications/MocoNotifier.app, zsh completion
moco login            # subdomain + personal API token (MOCO → Profile → Integrations)
moco daemon install   # reminders: LaunchAgent, starts at login
```

`~/.local/bin` must be on your `PATH`. On the first reminder macOS asks whether "MOCO Reminders"
may send notifications. Set its style to **Alerts** in System Settings → Notifications, otherwise
the questions disappear after a few seconds.

`make uninstall` removes binary, helper and completion; `moco daemon uninstall` the LaunchAgent.

## Daily use

```sh
moco                      # the TUI (same as `moco ui`)
moco status               # today: presences, present vs. logged, missing time, timer, queue

moco start                # start working now (open presence), default location of the weekday
moco start 8:15 --home
moco break                # split the presence at the configured break (13:00–14:00)
moco break 12:30-13:15
moco stop                 # finish now;  moco stop 17:30

moco log                  # wizard: project/task (fuzzy), duration, description
moco log dev 1h30 Fixed the login form           # alias + duration + description
moco log 45m Review -p "ACME Website" -t Design
moco log --gap -d yesterday                     # the unlogged time of a day
moco list --week          # moco edit <id> · moco delete <id>

moco timer start dev      # timer on today's activity; moco timer stop asks the description
```

Durations: `1h30`, `90m`, `1:30`, `1.5` – always **rounded up** to 15 minutes (configurable).
Times: `8`, `830`, `8:30`, `08:30`. Dates (`-d`): `today`, `yesterday`, `-2`, `mon`, `2026-10-05`.

### Aliases

```sh
moco alias add dev -p "ACME Website" -t Programming
moco alias list           # moco alias rm dev
```

### TUI

| Key | |
|---|---|
| `1` `2` `3` `4` · `tab` | week · presence · activities · projects |
| `←` `→` · `h` `l` | previous / next day — `H` `L` previous / next week — `t` today |
| `a` · `e` · `d` | log · edit · delete an activity (popup form, fuzzy project/task list) |
| `n` `s` `b` `m` `o` | presence: new · stop · break · merge with next · toggle home office |
| `T` | start / stop a timer |
| `p` `P` · `/` | projects: period · filter |
| `r` · `?` · `q` | reload · help · quit |

Data is loaded a week at a time and cached; skipping through days doesn't hit the API.

## Reminders

| Time | Notification | Answers |
|---|---|---|
| 08:00 | Did you start working? | Yes 08:00 / now, home / office, other time, not yet, day off |
| 12:30 | Morning: what's missing | Copy `moco log`, snooze, done |
| 14:00 | Did you take your break 13:00–14:00? | Yes, different time, not yet |
| 16:30 | Afternoon: what's missing | Copy `moco log`, snooze, done |
| 17:00 | Finished for today? | Yes 17:00 / now, still working (asked again until 20:00) |

Every reminder checks MOCO first and stays quiet if the thing is already done. After sleep only
the latest relevant reminder is shown. Weekends are skipped; days off with `moco pause`:

```sh
moco pause today          # moco pause fri · moco pause until 2026-10-16
moco pause list           # moco pause clear 2026-12-24
moco daemon status        # what's due today;  moco daemon logs
moco daemon test start    # show a reminder now (dry run, nothing is saved)
```

## Configuration

`~/.config/moco/config.toml` (created by `moco login`). The token lives in the Keychain.

```sh
moco config                               # all settings
moco config set schedule.start 7:30
moco config set location.fri home
moco config set schedule.workdays mon,tue,wed,thu
moco config edit                          # $EDITOR, checked afterwards
```

The daemon picks up changes on its own.

## Offline

When MOCO can't be reached, `log`, `start`, `stop`, `break` (and the TUI equivalents) go into a
local queue and print **MOCO NOT REACHABLE — NOT SAVED IN MOCO YET**. The next command or the
daemon sends them; `moco queue` shows them, `moco queue sync` retries, `moco queue drop <#>`
discards. Entries MOCO rejects stay as FAILED until you deal with them.

## Scripting

`--json` works on `status`, `list`, `presence list`, `projects`, `alias list`, `timer status`,
`queue list`, `pause list`, `daemon status` and `config`.

## Shell completion

`make install` installs the zsh completion into Homebrew's `site-functions`. Other shells:
`moco completion bash|fish|zsh --help`. Completion offers aliases, projects (`-p`), the tasks of
that project (`-t`) and config keys, from local data only.

## Development

```sh
make test     # go test ./... — against an in-memory fake MOCO, never the real account
make build    # bin/moco and build/MocoNotifier.app
```

`PLAN.md` holds the design decisions, the probed MOCO API behaviour and the progress log.

| Package | |
|---|---|
| `internal/api` | MOCO client: auth, rate limit, pagination, errors |
| `internal/service` | domain logic shared by CLI, TUI and daemon (presences, activities, timer, queue) |
| `internal/cli` · `internal/tui` · `internal/wizard` | cobra commands · bubbletea TUI · huh wizard |
| `internal/daemon` · `internal/notify` · `notifier/` | reminders · notifier protocol · Swift helper app |
| `internal/config` · `internal/store` · `internal/timeutil` | settings · local state · time math |

## License

MIT
