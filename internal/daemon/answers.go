package daemon

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// plan is what an answer means.
type plan struct {
	what   string                                                          // e.g. "start at 08:00 (office)"; "" = nothing to report
	run    func(ctx context.Context, svc *service.Service) (string, error) // the write; nil = none
	effect func(d *store.DaemonDay)                                        // reminder state after success
}

// decide turns an answer into a plan. now is the time of the answer, date the reminder's day.
func decide(ev Event, r notify.Response, cfg config.Config, date, now time.Time, pause func(string) error) (plan, error) {
	s := cfg.Schedule
	ds := timeutil.Date(date)
	today := ds == timeutil.Date(now)
	nowClock := func() (string, error) {
		if !today {
			return "", fmt.Errorf("\"now\" only works on the day of the question (%s)", ds)
		}
		return timeutil.Clock(now), nil
	}
	answered := func(d *store.DaemonDay) { Answered(d, ev) }

	if r.Dismissed || (r.Action == "default" && ev != Morning && ev != Afternoon) {
		// Not an answer: the question stays open and is asked again as scheduled.
		return plan{effect: func(d *store.DaemonDay) { d.Events[string(ev)].Shown = false }}, nil
	}

	start := func(from string, home bool) plan {
		return plan{
			what: fmt.Sprintf("start at %s (%s)", from, where(home)),
			run: func(ctx context.Context, svc *service.Service) (string, error) {
				p, err := svc.Start(ctx, date, from, &home)
				return fmt.Sprintf("Started at %s (%s).", p.From, where(p.IsHomeOffice)), err
			},
			effect: answered,
		}
	}
	stop := func(to string) plan {
		return plan{
			what: "finish the day at " + to,
			run: func(ctx context.Context, svc *service.Service) (string, error) {
				p, err := svc.Stop(ctx, date, to)
				return fmt.Sprintf("Finished at %s. Have a nice evening!", p.To), err
			},
			effect: answered,
		}
	}
	brk := func(from, to string) plan {
		return plan{
			what: fmt.Sprintf("record the break %s–%s", from, to),
			run: func(ctx context.Context, svc *service.Service) (string, error) {
				_, err := svc.Break(ctx, date, from, to)
				return fmt.Sprintf("Break %s–%s recorded.", from, to), err
			},
			effect: answered,
		}
	}

	home := cfg.HomeOfficeOn(date)
	switch r.Action {
	case actStartDefault:
		return start(s.Start, home), nil
	case actStartAlt:
		return start(s.Start, !home), nil
	case actStartNow:
		from, err := nowClock()
		return start(from, home), err
	case actStartTime:
		from, h, err := parseStartText(r.Text, home)
		return start(from, h), err
	case actNotYet:
		return plan{effect: func(d *store.DaemonDay) { NotYet(d, cfg, ev, now) }}, nil
	case actDayOff:
		return plan{
			what: "take " + ds + " off (no more reminders)",
			run: func(context.Context, *service.Service) (string, error) {
				return "Day off — no more reminders today.", pause(ds)
			},
			effect: DayOff,
		}, nil
	case actCopy, "default":
		return plan{
			what: "copy `moco log` to the clipboard",
			run: func(ctx context.Context, _ *service.Service) (string, error) {
				cmd := exec.CommandContext(ctx, "/usr/bin/pbcopy")
				cmd.Stdin = strings.NewReader("moco log")
				return "", cmd.Run()
			},
			effect: answered,
		}, nil
	case actSnooze:
		return plan{effect: func(d *store.DaemonDay) { Snooze(d, cfg, ev, now) }}, nil
	case actDone:
		return plan{effect: answered}, nil
	case actBreakDefault:
		return brk(s.BreakFrom, s.BreakTo), nil
	case actBreakOther:
		from, to, err := parseRange(r.Text)
		return brk(from, to), err
	case actEndDefault:
		return stop(s.End), nil
	case actEndNow:
		to, err := nowClock()
		return stop(to), err
	case actEndTimer:
		to, err := nowClock()
		if err != nil {
			return plan{}, err
		}
		desc := strings.TrimSpace(r.Text)
		if desc == "" {
			return plan{}, errors.New("the timer needs a description — stop it with `moco timer stop`")
		}
		p := stop(to)
		p.what = "stop the timer (\"" + desc + "\") and " + p.what
		finish := p.run
		p.run = func(ctx context.Context, svc *service.Service) (string, error) {
			res, err := svc.StopTimer(ctx, desc)
			var q *service.QueuedError
			if err != nil && !errors.As(err, &q) {
				return "", fmt.Errorf("timer: %w (the day is still open)", err)
			}
			msg, err := finish(ctx, svc)
			return fmt.Sprintf("Timer stopped (%s logged). %s", timeutil.FormatSeconds(res.Activity.Seconds), msg), err
		}
		return p, nil
	case actStillWorking:
		return plan{effect: func(d *store.DaemonDay) { StillWorking(d, cfg, now) }}, nil
	}
	return plan{}, fmt.Errorf("unknown answer %q", r.Action)
}

// parseStartText reads "8:15", "0815 home", "8.15 office".
func parseStartText(text string, defaultHome bool) (string, bool, error) {
	f := strings.Fields(strings.ToLower(text))
	if len(f) == 0 || len(f) > 2 {
		return "", defaultHome, fmt.Errorf("expected a time like 08:15 (optionally + home/office), got %q", text)
	}
	from, err := timeutil.NormalizeClock(f[0])
	if err != nil {
		return "", defaultHome, err
	}
	home := defaultHome
	if len(f) == 2 {
		switch f[1] {
		case "home", "h", "homeoffice":
			home = true
		case "office", "o", "büro", "buero":
			home = false
		default:
			return "", defaultHome, fmt.Errorf("location %q: use home or office", f[1])
		}
	}
	return from, home, nil
}

// parseRange reads "12:30-13:15" (also "12.30 – 13.15").
func parseRange(text string) (string, string, error) {
	parts := strings.FieldsFunc(text, func(r rune) bool { return r == '-' || r == '–' || r == ' ' })
	if len(parts) != 2 {
		return "", "", fmt.Errorf("expected a range like 12:30-13:15, got %q", text)
	}
	from, err := timeutil.NormalizeClock(parts[0])
	if err != nil {
		return "", "", err
	}
	to, err := timeutil.NormalizeClock(parts[1])
	if err != nil {
		return "", "", err
	}
	return from, to, nil
}
