# moco-cli — Plan

A macOS CLI + TUI + background daemon for logging work time in [MOCO](https://www.mocoapp.com/)
with a personal API token. The daemon nudges via native macOS notifications; simple yes/no
questions are answered directly in the notification, everything else is done by the user in
their own terminal (the tool never opens terminal windows).

Status: **approved** — Phases 0–7 committed

---

## 1. Decisions (agreed)

| Topic | Decision |
|---|---|
| Language | Go for CLI, TUI, daemon, API client. Swift only for a tiny notification helper app. |
| API access | Own thin, typed Go client for the endpoints we use (no Python wrapper, no codegen). Types derived from `moco-api-reference.json`. |
| Binary name | `moco` only (no `mc` – clashes with Midnight Commander / MinIO client). |
| Install | `make install`: Go binary → `~/.local/bin/moco`, helper → `~/Applications/MocoNotifier.app` (ad-hoc signed). `moco daemon install` writes the LaunchAgent. No Homebrew tap. |
| Token storage | macOS Keychain. Non-secret config in `~/.config/moco/config.toml`. |
| Daemon | Long-running process under a launchd LaunchAgent (`RunAtLoad`, `KeepAlive`). |
| Smart nags | Daemon checks MOCO state before every reminder and skips/adapts if already done. |
| Missed reminders | After sleep/wake only the latest missed reminder fires (if still relevant). Never a burst. |
| Days off | Weekends are skipped. Otherwise manual only: `moco pause …`. No MOCO absence / holiday lookup. |
| Notifications | Only simple questions are answerable in-notification (start, break, end of day). "What did you do" reminders inform + offer **Copy command** / **Snooze** / **Done**. Nothing ever opens a window. |
| Quick logging | Inline (non-fullscreen) fuzzy wizard + one-liner flags + aliases + "fill the gap". |
| Full TUI | Day view, week view, project browser, presence editing, add/edit/delete activities. |
| Durations | Accept `1h30`, `90m`, `1.5`, `1:30`; **always round up** to 15 min (configurable). |
| Billable / tags | Not exposed. MOCO/project decides billing; user only picks project, task, duration, description. |
| Home office | Asked in the start prompt: default location (configurable per weekday) on the primary buttons, explicit home/office variants in the Options menu. |
| Offline | Local queue, synced automatically by the daemon / next command. Output must state **unmistakably** that MOCO was not reachable and the entry is queued. |
| Timer | In v1. `moco timer start [alias]` → project+task; `moco timer stop` → asks for the description. |
| Schedule | Configurable in `config.toml`; defaults are the times below. |
| Language | English for all texts. |

---

## 2. MOCO API usage

Base URL `https://{subdomain}.mocoapp.com/api/v1`, header `Authorization: Token token=<key>`,
`Content-Type: application/json`. Rate limit 120 req / 2 min → client-side limiter + retry on 429
(`Retry-After`). Collections are paginated (`page`, `per_page`, `X-Total` / `Link` headers) → client
follows pages.

| Purpose | Endpoint |
|---|---|
| Verify token, get own user id | `GET /session` |
| My projects + their tasks ("what type of work") | `GET /projects/assigned?active=true` |
| Read presences for a day/week | `GET /users/presences?from=&to=&user_id=<me>` |
| Start / end presence | `POST /users/presences` (`date`, `from`, `to?`, `is_home_office`) and `PATCH /users/presences/{id}` |
| Delete presence | `DELETE /users/presences/{id}` |
| List activities | `GET /activities?from=&to=&user_id=<me>` |
| Create / edit / delete activity | `POST /activities` (`date`, `project_id`, `task_id`, `seconds`, `description`), `PATCH`, `DELETE /activities/{id}` |
| Timer | `PATCH /activities/{id}/start_timer`, `PATCH /activities/{id}/stop_timer` (only for today's activities, one timer per user) |

Break model: a day with a break = two presences (`08:00–13:00`, `14:00–17:00`).

### Phase 0 probe results (2026-10-07, `scripts/probe.sh`)

| Finding | Consequence |
|---|---|
| `GET /session` → `{id, uuid}` only | User id comes from here; display name from the `user` object of own activities. |
| `GET /users`, `GET /users/{id}` → `403` (empty body) | Never called. |
| `GET /activities` without `user_id` returns **all colleagues'** entries | Client always sends `user_id=<me>` for activities. |
| `GET /users/presences` returns only own presences (same total with/without `user_id`) | `user_id` still sent for clarity. |
| `GET /projects/assigned?active=true` → 38 projects, each with `customer{id,name}`, `tasks[]{id,name,active,billable}`, `contract{user_id,active}` | One call fills the project/task cache; no per-project task requests. |
| `GET /projects` works (all 266 company projects) | Not used — assigned projects only. |
| Activity has `seconds`, `worked_seconds`, `hours` (rounded), `timer_started_at` | Use `seconds`; running timer = `timer_started_at != null`. |
| Pagination: `X-Total`, `X-Page`, `X-Per-Page`, `Link rel="next"`; default 100/page | Client follows `Link` next. |
| No rate-limit headers in responses | Client-side limiter (120 / 2 min) + retry on `429`. |
| Errors (`403`, `404`) have an **empty body** | Error messages derived from status code; JSON body parsed only when present (e.g. `422`). |

### Write probe results (2026-10-07, `scripts/probe-write.sh` on empty 2026-10-06, project "Intern")

| Finding | Consequence |
|---|---|
| Presence `POST` with `from` only → open presence (`to: null`), also on past days | `moco start` / `moco break` work for back-filling with `--date`. |
| Presence update is `PATCH` (partial); `DELETE` returns the deleted object | Client uses `PATCH`. |
| **`is_home_office` is per day**: setting it on one presence changes all presences of that day | Location is chosen once per day (start prompt); `moco presence edit --home/--office` changes the whole day and says so. |
| Overlapping ranges and a second open presence → `422 {"from":["range overlaps"]}` | Only one open presence at a time; checked locally first for a clear message. |
| **Malformed time (`"8"`) → `500`** | Client validates `HH:MM` before sending; the offline queue must never retry such a request forever. |
| Validation errors are a top-level field map: `{"task_id":["ist nicht gültig"]}`, `{"base":["…"]}` (messages may be German) | Error parser handles field maps, `errors`, `message`. |
| Minutes are kept as given (`17:07`) | Presences are not rounded. |
| Activity `billable` follows the project (internal project → `false`) | Confirms: billable not exposed. |
| `start_timer` on a past day → `422 {"base":["Timer can only be started on the current day"]}` | Timer only for today. |
| `presences/touch`: `override` is a **boolean**, not a timestamp; acts on "now" | `touch` is not used; start/stop use explicit `POST`/`PATCH`. |

### Timer probe results (2026-10-08, on today with explicit OK, project "Intern", cleaned up)

| Finding | Consequence |
|---|---|
| **Creating a 0-second activity for today starts its timer by itself**; an explicit `start_timer` then answers `422 Timer is already running` | `timer start` only calls `start_timer` if the created activity isn't running yet. |
| **A starting timer opens a presence** (at the current minute) if the day has none open; stopping the timer leaves it open | `timer start` prints the open presence; the daemon's 08:00 check sees it as "started". |
| While running, `seconds`/`worked_seconds` stay unchanged (0); `timer_started_at` is set | Running time = `seconds + now − timer_started_at` (`service.TimerSeconds`). |
| `stop_timer` writes the tracked time (whole minutes) into `seconds` | Rounded up afterwards with a `PATCH`, minimum one rounding step. |

---

## 3. Daily reminder flow (defaults)

Workdays Mon–Fri. All times configurable. Every reminder first checks MOCO; "skip" means no
notification at all.

| Time | Notification | Skip if | Buttons (primary / Options menu) | Effect |
|---|---|---|---|---|
| 08:00 | "Did you start working?" | presence exists today, or day paused | **Yes, 08:00** · **Yes, now** / *08:00 (home)*, *08:00 (office)*, *Other time…* (reply field `HH:MM`), *Not yet*, *Day off* | Creates open presence (`from`, no `to`) with location. *Not yet* / no reaction → re-ask once at **09:00**. *Day off* → pauses today. |
| 12:30 | "Morning: 4h30 present, 2h00 logged — 2h30 missing" | logged ≥ present for the morning | **Copy `moco log`** · **Snooze 30m** / *Done* | Copy puts `moco log` on the clipboard. |
| 14:00 | "Did you take your break 13:00–14:00?" | presence already split | **Yes, 13–14** · **Different…** (reply field `12:30-13:15`) / *No break yet* | Sets `to=13:00` on the open presence and creates a new open presence from `14:00`. *No break yet* → re-ask in 30 min (once). |
| 16:30 | "Afternoon: … missing" | logged ≥ present | same as 12:30 | |
| 17:00 | "Finished for today?" | no open presence | **Yes, 17:00** · **Yes, now** / *Still working* | Closes open presence. *Still working* → re-ask every 30 min, until 20:00 at the latest. If a timer is running, the text says so and stopping the day also offers stopping the timer. |

"Present" / "logged" are computed for the day so far: activities have no time of day in MOCO, so
logged time cannot be split into half-days. The gap is `present until now − logged today`; at 12:30
that is the morning, at 16:30 the whole day.
No reaction to a notification counts as "not answered"; it is never treated as yes.

---

## 4. CLI commands

```
moco                         # opens the full TUI (same as `moco ui`)
moco login                   # subdomain + token → verify via /session → Keychain
moco logout
moco status                  # today: presences, logged vs present, gap, running timer, queue size

# presences
moco start [HH:MM] [--home|--office] [-d D]  # default: now, default location
moco break [HH:MM-HH:MM] [-d D]              # default: configured break window
moco stop  [HH:MM] [-d D]                    # close open presence (default: now)
moco presence list [--week] [-d D]
moco presence edit <id> [--from] [--to] [--home|--office]
moco presence delete <id> [--yes]
# -d accepts YYYY-MM-DD, today, yesterday, -N, weekday; past days need explicit times
# times accept 8, 830, 8:30, 8.30, 08:30

# activities
moco log [alias] [duration] [description] [-p project] [-t task] [-d date]
                                          # missing parts asked via inline wizard
moco log --gap                            # duration = present − logged of the day
moco list [-d D] [--week] [--from D --to D]
moco edit <id> [--duration] [-m description] [-p] [-t] [-d]   # wizard without flags
moco delete <id> [--yes]
# edit/delete refuse activities of colleagues (the token can read them)

# timer
moco timer start [alias]                  # wizard for project+task if no alias
moco timer stop                           # asks description, rounds up, saves
moco timer status

# projects & aliases
moco projects [--tasks]                   # assigned projects (cached), fuzzy filter arg
moco alias add <name> [-p project -t task]   # wizard if omitted
moco alias list | rm <name>

# days off
moco pause today | <date> | until <date> | list | clear <date>

# offline queue
moco queue [list|sync|drop <n>]

# daemon
moco daemon install | uninstall | start | stop | status | logs
moco daemon test <event>                  # fire a reminder now, for testing
moco config [edit|path|get|set]
```

Global flags: `--json` for machine-readable output on list/status commands, `--debug`.

### Inline wizard (`moco log`)
Built with `charmbracelet/huh`, inline (no alternate screen):
1. **Project** – fuzzy search, recently used first (aliases listed too).
2. **Task** – fuzzy search over the project's active tasks, last used for this project preselected.
3. **Duration** – field starts **empty**; the day's unlogged time is shown as hint + placeholder
   ("4h00 not logged yet · Enter takes it"); Enter on the empty field uses it. In edit mode the hint
   is the current duration and Enter keeps it. Live rounding preview ("1h07 → 1h15").
4. **Description** – free text, required.
5. **Date** – default today (only shown with `-d` or an option toggle).
6. Summary + confirm → POST. On failure, the entry goes to the queue with a loud warning.

### Styling
Wizard and TUI use only the 16 ANSI colours (blue titles/focus bar, magenta selector/cursor,
green selection/confirm, grey hints, cyan project/task, red errors), so they follow the user's
terminal colour scheme. Plain command output (`status`, `list`) gets the same palette in Phase 8.

### Full TUI (`moco ui`)
Built with `bubbletea` + `lipgloss`, fullscreen:
- **Day view** – presences and activities of the selected day, totals, gap of the day;
  `a` add, `e` edit, `d` delete, `←/→` previous/next day.
- **Week view** – Mon–Fri totals, present vs logged, gaps highlighted; `enter` jumps to the day.
- **Projects** – assigned projects → tasks → my hours on them for a selectable period.
- **Presence editing** – adjust from/to, split/merge for breaks, toggle home office.
- Status bar: running timer, queued entries, offline indicator.

---

## 5. Architecture

```
moco-cli/
├── cmd/moco/main.go
├── internal/
│   ├── api/          # HTTP client, auth, rate limiter, pagination, typed models, errors
│   ├── config/       # config.toml load/save, defaults
│   ├── secrets/      # Keychain access (go-keychain or `security` CLI)
│   ├── store/        # local state: cache (projects, me), recents, aliases, pauses, queue, daemon state
│   ├── timeutil/     # duration parsing, rounding, gap math, workday logic
│   ├── service/      # domain logic: start/break/stop, gap calculation, logging, timer, queue sync
│   ├── cli/          # cobra commands
│   ├── wizard/       # huh-based inline flows
│   ├── tui/          # bubbletea app
│   ├── daemon/       # scheduler, wake detection, reminder rules, IPC with notifier
│   └── notify/       # Go side of the notifier protocol
├── notifier/         # Swift package → MocoNotifier.app
├── Makefile
└── PLAN.md
```

### Notification helper (`MocoNotifier.app`)
- Swift, `UNUserNotificationCenter`, categories with `UNNotificationAction` and
  `UNTextInputNotificationAction` (for "Other time…" / "Different…").
- Needs to be an `.app` bundle with its own bundle id (`de.artismedia.moco-notifier` or similar),
  ad-hoc signed; the user grants notification permission once on first launch.
- Launched by the daemon as a background agent app (`LSUIElement`, no Dock icon) and kept running.
- IPC: Unix domain socket at `~/.local/state/moco/notifier.sock` (helper listens, several clients:
  the daemon plus short-lived CLI checks; replies go to the asking client, user answers to all),
  newline-delimited JSON, every message has a `type`:
  daemon → helper: `ping`, `notify {id,category,title,subtitle?,body,actions[{id,title,input?,placeholder?,button?}],sound?}`,
  `remove {ids}`, `quit`;
  helper → daemon: `pong {version,authorization}`, `delivered {id}`, `error {id?,message}`,
  `response {id,action,text?}` (`action:"default"` = notification clicked) or `response {id,dismissed:true}`.
  Answers given while no client is connected are kept (up to 100) and sent to the next one.
- Each distinct action set becomes its own notification category (stable FNV id); the helper waits
  for the category registration before posting, so buttons are never missing.
- The Go side (`internal/notify`) launches the helper with `open -g -a … --args --socket …` if
  nothing listens. Hidden debug command: `moco notifier status|test|quit`.
- macOS shows new apps' notifications as **banners** (buttons on hover, disappear after a few
  seconds). For questions that wait, set "MOCO Reminders" to **Alerts** in System Settings →
  Notifications.
- The helper contains no business logic.

### Daemon
- Started by LaunchAgent `de.artismedia.moco.daemon` (`moco daemon run`); logs to
  `~/Library/Logs/moco/daemon.log`.
- Ticker every 30 s plus wake detection (wall-clock jump) → evaluates the reminder table against
  local daemon state (fired/answered/snoozed per event per day) and MOCO state.
- Executes notification answers via `internal/service` (same code paths as the CLI).
- Syncs the offline queue every few minutes when reachable.

### Local files
- `~/.config/moco/config.toml` – subdomain, schedule, rounding, default location per weekday, aliases.
- `~/.local/state/moco/state.json` – recents, pauses, queue, daemon event state, project cache (TTL 1 day,
  `moco projects --refresh`).

### Example config
```toml
subdomain = "artismedia"
rounding_minutes = 15
rounding = "up"

[schedule]
workdays = ["mon", "tue", "wed", "thu", "fri"]
start = "08:00"
start_reask = "09:00"
morning_log = "12:30"
break_from = "13:00"
break_to = "14:00"
break_check = "14:00"
afternoon_log = "16:30"
end = "17:00"
end_reask_every = "30m"
end_reask_until = "20:00"
snooze = "30m"

[location]           # default for the primary start buttons
default = "office"
mon = "home"
fri = "home"

[aliases]
review = { project = "ACME Website", task = "Project management" }
```

---

## 6. Implementation phases

0. **Probe** – `moco probe` (or a small script) that calls each read endpoint with the personal
   token and reports status codes and response shapes. Run by you; adjust the plan with the results.
1. **Foundation** – Go module, config, Keychain, API client (session, projects/assigned, presences,
   activities), `moco login`, `moco status`, `moco projects`.
2. **Presences** – `moco start`, `moco break`, `moco stop`, `moco presence list/edit/delete`.
3. **Activities** – duration parsing/rounding, `moco log` wizard + flags, aliases, recents, gap, `moco list`,
   `moco edit`, `moco delete`.
4. **Offline queue** – queue on network/5xx errors, `moco queue` commands, loud warnings.
5. **Timer** – `moco timer start/stop/status`.
6. **Notifier app** – Swift helper, socket protocol, `make install`.
7. **Daemon** – scheduler, reminder rules, wake catch-up, pauses, LaunchAgent install, `moco daemon test`.
8. **TUI** – day view, week view, project browser, presence editing.
9. **Polish** – `--json`, README, shell completions.

Testing: unit tests for duration parsing, rounding, gap math, reminder rule evaluation
(with a fake clock and a fake MOCO state); API client tested against `httptest` fixtures shaped
after the OpenAPI spec. No automated tests ever write to the real MOCO account.

---

## 7. Assumptions to confirm on review

- Timer implementation (done in Phase 5): `moco timer start` creates an activity for today with
  `seconds=0` and a placeholder description (MOCO starts the timer itself); `moco timer stop` asks for
  the description, calls `stop_timer`, rounds up (at least one step) and `PATCH`es. Starting a timer
  while one is running stops the old one first (asking its description). `moco timer cancel` discards
  a timer and its activity.
- A timer running across the break is left alone; the 14:00 break prompt only mentions it.
- `moco stop` with a running timer asks whether to stop the timer too.
- Activities with no presence on that day are allowed (MOCO allows it); `moco status` warns.
- No colleagues/multi-user features, no impersonation, no reports beyond own hours.
- Supported macOS: target is **macOS 26 (Tahoe, Darwin 25)**, the version installed now (26.7).
  The Swift helper is built with the installed SDK (macOS 27 SDK, Swift 6.4) and a deployment target
  of macOS 26, so it also runs after the planned update to macOS 27 (Darwin 26). Only stable
  `UserNotifications` APIs are used. If the update breaks anything (e.g. notification permissions,
  LaunchAgent behaviour), it gets fixed after the update.

---

## 8. Progress log

**2026-10-07/08**
- Phases 0–2 done and committed (`48562fa`, `c377caa`).
- Phase 3 (activities: `log`, `list`, `edit`, `delete`, `alias`, wizard) verified against MOCO
  with one-liners on the sandbox day, committed (`Phase 3: …`).
- User tested the wizard → feedback applied: empty duration field with hint, ANSI-colour theme.
  Wizard re-test by the user passed (2026-10-08).
- **Sandbox for write tests: Mon 2026-10-05** (verified empty 2026-10-08), project
  "Intern – nicht verrechenbar" only, always cleaned up, date always given explicitly.
  2026-10-06 was the sandbox until the user entered real data there — never touch it again.
- **Open for Phase 5:** does an activity's `seconds` include a running timer segment? (`status`
  currently assumes not.)

**2026-10-08 — Phase 4 (offline queue)**
- Writes that fail with a network error or 5xx (`log`, `start`, `stop`, `break`) go to the queue in
  `state.json` and print a red "MOCO NOT REACHABLE — NOT SAVED IN MOCO YET" block (exit 0: the entry
  is safe). Edit/delete are not queued; offline they fail with "Nothing was changed in MOCO".
- Presence writes are queued as **intents** and replayed through the same service logic, so they
  are checked against the day's state at sync time. Every replay first checks whether MOCO already
  has the result (the request may have arrived although the answer got lost): log → identical
  activity created after queueing; start/presence → same `from`; stop → closed at the same `to`;
  break → both halves present, or the recorded split presence already shortened (then only the
  rest is created).
- Every command (via `newService`) first syncs pending items in order and stops at the first
  unreachable error; a non-blocking `sync.lock` keeps CLI and daemon from sending an item twice.
- MOCO rejects (4xx / failed local check) → item stays as **FAILED**, never retried automatically,
  warned about on every command; `moco queue sync` retries it, `moco queue drop <#>` removes it.
  5xx on the write itself (reads before it worked) → FAILED after 3 tries, so a request MOCO
  answers with 500 is never retried forever.
- `moco status` shows queue counts (`queue_pending`/`queue_failed` in `--json`).
- Verified against MOCO on the sandbox day with an unreachable proxy (`HTTPS_PROXY=http://127.0.0.1:9`):
  queued start/break/stop/log, auto-sync on the next command, duplicate start skipped, impossible
  stop kept as FAILED, dropped; sandbox cleaned up.

**2026-10-08 — Phase 5 (timer)**
- `moco timer start [alias] [description…]` (`-p/-t`, wizard otherwise), `timer stop [description…]`
  (asks with a prompt, prefilled if the entry already has one), `timer status`, `timer cancel`.
- A running timer is found over the last 7 days (catches a forgotten one). Durations in `list`/
  `status` include the running segment; the placeholder shows as "(no description yet)".
- `moco stop` (today) asks whether to stop a running timer too; without a terminal it only warns.
- Timer start/stop are not queued (they must be live). If `stop_timer` works but saving the rounded
  time/description doesn't reach MOCO, that update is queued (`edit` queue item).
- If `start_timer` fails, the freshly created empty activity is deleted again.
- Verified on today with OK: start, status, stop (5 min → 0h15), start with description, cancel;
  probe activity and the auto-opened presence deleted, today empty as before.
- Not tested live: the `moco stop` timer question (needs a terminal) — for the user to try.

**2026-10-08 — Phase 6 (notifier app)**
- `notifier/` Swift package (Swift 6.4, language mode 5, deployment target macOS 26) →
  `MocoNotifier.app` (`de.artismedia.moco-notifier`, display name "MOCO Reminders", `LSUIElement`,
  ad-hoc signed). `make build|install|uninstall|test|clean`.
- `internal/notify` client + protocol tests against a fake helper socket.
- Installed with `make install`; permission granted by the user. `moco notifier test` verified on
  macOS 26: button answer (`yes_0800`) and text reply (`other`, `"09:00"`) arrive in Go.
- **Finding:** with the Alerts style, macOS puts *all* actions into one **Options** menu (next to
  Close) — there are no separate primary buttons. Consequence for Phase 7: the most likely answer
  goes first in the list; the body text states the default ("Yes, 08:00" etc.) so the menu is
  quick to scan. The §3 "primary / Options" split becomes just an ordering.
- Note: an old `alias moco=…python…` in the user's `~/.zshrc` shadowed the binary (user removes it).

**2026-10-08 — Phase 7 (daemon)**
- `internal/daemon`: `rules.go` (pure: schedule, relevance check against MOCO, pick, re-ask
  policy), `messages.go` (texts + action ids), `answers.go` (answer → plan: description, MOCO
  write via `service`, state effect), `daemon.go` (30 s loop, notifier connection, answers).
- **Catch-up:** of all due reminders only the latest *still relevant* one is shown; the others are
  closed as missed (e.g. waking at 13:30 without a presence → only the start question).
- **Relevance:** start = no presence; log reminders = presence and gap ≥ one rounding step;
  break = exactly one presence, open, begun before `break_from`; end = open presence.
  MOCO unreachable → only the start question is asked (its answer is queued), the rest waits.
- Shown questions are rechecked every 5 min; settled elsewhere → notification removed.
- Answers: click on the notification / dismiss = not answered (asked again as scheduled);
  bad input or MOCO error → error notification and the question returns on the next tick;
  queued → "Queued" notification. Late answers for an earlier day still write to that day.
- Offline queue synced every 5 min by the daemon; rejected entries get a notification.
- Start options also offer the other location; "Other time…" accepts `8:15 home`.
- `moco daemon install|uninstall|start|stop|status|logs|test <event>` — LaunchAgent
  `de.artismedia.moco.daemon` (RunAtLoad, KeepAlive), log `~/Library/Logs/moco/daemon.log`,
  pid file for `test` (SIGUSR1). **`test` is a dry run**: answers are described, nothing is written.
- `moco pause today|<date>|until <date>|list|clear <date>` (forward date parsing: weekday = next one).
- Notifier changed to several clients (replies to the asker, answers broadcast) — a CLI check no
  longer disconnects the daemon. `make install` restarts a running daemon.
- Verified live: LaunchAgent installed and running, notifier connected, dry-run start/end tests
  answered by the user (option, click) with result notifications. Daemon left running.

Next: Phase 8 (TUI).
