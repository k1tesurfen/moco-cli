package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

const (
	tickEvery     = 30 * time.Second
	recheckEvery  = 5 * time.Minute // how often shown questions are checked against MOCO
	syncEvery     = 5 * time.Minute // offline queue
	notifyTimeout = 10 * time.Second
)

// Daemon shows the reminders and carries out the answers.
type Daemon struct {
	NewService func(ctx context.Context) (*service.Service, error)
	Store      *store.Store
	Socket     string // MocoNotifier socket
	App        string // MocoNotifier.app, launched if not running
	Now        func() time.Time
	Log        *log.Logger
	LoadConfig func() (config.Config, error) // config.Load if nil

	svc         *service.Service
	client      *notify.Client
	lastRecheck time.Time
	lastSync    time.Time
	lastErr     map[string]string
}

// PIDFile is where the running daemon writes its pid (used by `moco daemon test`).
func PIDFile() string { return filepath.Join(store.Dir(), "daemon.pid") }

// Run loops until ctx ends. Errors (MOCO or the helper unreachable, not logged in) are logged
// and retried; they never end the daemon.
func (dm *Daemon) Run(ctx context.Context) error {
	if err := os.MkdirAll(store.Dir(), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(PIDFile(), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		return err
	}
	defer os.Remove(PIDFile())
	usr1 := make(chan os.Signal, 1)
	signal.Notify(usr1, syscall.SIGUSR1)
	defer signal.Stop(usr1)

	dm.Log.Printf("daemon started (pid %d)", os.Getpid())
	ticker := time.NewTicker(tickEvery)
	defer ticker.Stop()
	dm.tick(ctx)
	for {
		var responses <-chan notify.Response
		if dm.client != nil {
			responses = dm.client.Responses
		}
		select {
		case <-ctx.Done():
			dm.Log.Printf("daemon stopped")
			if dm.client != nil {
				dm.client.Close()
			}
			return nil
		case <-ticker.C:
			dm.tick(ctx)
		case <-usr1:
			dm.tick(ctx) // picks up a `moco daemon test` request
		case r, ok := <-responses:
			if !ok {
				dm.Log.Printf("notifier connection closed")
				dm.client = nil
				continue
			}
			dm.answer(ctx, r)
		}
	}
}

// logErr logs an error once until it changes or clears, so an outage doesn't flood the log.
func (dm *Daemon) logErr(what string, err error) {
	if dm.lastErr == nil {
		dm.lastErr = map[string]string{}
	}
	if err == nil {
		if dm.lastErr[what] != "" {
			dm.Log.Printf("%s: ok again", what)
		}
		delete(dm.lastErr, what)
		return
	}
	if msg := err.Error(); msg != dm.lastErr[what] {
		dm.Log.Printf("%s: %s", what, msg)
		dm.lastErr[what] = msg
	}
}

func (dm *Daemon) ready(ctx context.Context) bool {
	load := dm.LoadConfig
	if load == nil {
		load = config.Load
	}
	cfg, err := load()
	dm.logErr("config", err)
	if err != nil {
		return false
	}
	if dm.svc == nil || dm.svc.Cfg.Subdomain != cfg.Subdomain {
		svc, err := dm.NewService(ctx)
		dm.logErr("service", err)
		if err != nil {
			return false
		}
		dm.svc = svc
	}
	dm.svc.Cfg = cfg // pick up schedule changes without a restart
	if dm.client == nil {
		dctx, cancel := context.WithTimeout(ctx, notifyTimeout)
		c, err := notify.Dial(dctx, dm.Socket, dm.App)
		cancel()
		dm.logErr("notifier", err)
		if err != nil {
			return false
		}
		dm.client = c
		dm.Log.Printf("connected to the notifier")
	}
	return true
}

// today loads (or starts) the reminder state of the current day.
func (dm *Daemon) today(st store.State, now time.Time) *store.DaemonDay {
	ds := timeutil.Date(now)
	d := st.Daemon
	if d == nil || d.Date != ds {
		d = NewDay(dm.svc.Cfg, now, st.Paused(ds))
	}
	if st.Paused(ds) {
		DayOff(d) // `moco pause today` during the day
	}
	return d
}

func (dm *Daemon) save(d *store.DaemonDay) {
	if err := dm.Store.Update(func(st *store.State) error { st.Daemon = d; return nil }); err != nil {
		dm.Log.Printf("save state: %v", err)
	}
}

func (dm *Daemon) tick(ctx context.Context) {
	if !dm.ready(ctx) {
		return
	}
	now := dm.Now()
	st, err := dm.Store.Load()
	if err != nil {
		dm.Log.Printf("load state: %v", err)
		return
	}
	if st.DaemonTest != "" {
		dm.test(ctx, Event(st.DaemonTest), now)
	}
	if prev := st.Daemon; prev != nil && prev.Date == timeutil.Date(now) && st.Paused(prev.Date) {
		// Paused during the day: take open questions off the screen.
		for _, ev := range Events {
			if prev.Events[string(ev)].Shown {
				dm.client.Remove(noteID(prev.Date, ev))
			}
		}
	}
	d := dm.today(st, now)

	var day *service.Day
	facts := func() *service.Day {
		if day == nil {
			got, err := dm.svc.Day(ctx, now)
			dm.logErr("MOCO", err)
			if err != nil {
				return nil
			}
			day = &got
		}
		return day
	}

	if dueAny(d, now) {
		if ev, ok := Pick(d, dm.svc.Cfg, now, facts()); ok {
			n := message(ev, dm.svc.Cfg, now, day, noteID(d.Date, ev))
			if err := dm.notify(ctx, n); err == nil {
				Shown(d, dm.svc.Cfg, ev, now)
				dm.Log.Printf("asked %s", ev)
			}
		}
	}

	if now.Sub(dm.lastRecheck) >= recheckEvery && anyShown(d) {
		dm.lastRecheck = now
		if f := facts(); f != nil {
			for _, ev := range Events {
				st := d.Events[string(ev)]
				if relevant, _ := Relevant(ev, dm.svc.Cfg, f); st.Shown && !relevant {
					// Answered elsewhere (CLI, MOCO web): take the question off the screen.
					dm.client.Remove(noteID(d.Date, ev))
					Answered(d, ev)
					dm.Log.Printf("%s settled elsewhere, notification removed", ev)
				}
			}
		}
	}
	dm.save(d)

	if now.Sub(dm.lastSync) >= syncEvery {
		dm.lastSync = now
		if pending, _ := st.QueueCounts(); pending > 0 {
			res, err := dm.svc.SyncQueue(ctx, false)
			if err != nil {
				dm.Log.Printf("queue sync: %v", err)
			}
			for _, it := range res.Sent {
				dm.Log.Printf("synced queued #%d: %s", it.ID, it.Summary())
			}
			for _, it := range res.Rejected {
				dm.Log.Printf("MOCO rejected queued #%d: %s: %s", it.ID, it.Summary(), it.LastError)
				dm.notify(ctx, notify.Notification{ID: fmt.Sprintf("queue.%d", it.ID), Category: "result", Sound: true,
					Title: "MOCO rejected a queued entry", Body: it.Summary() + " — see `moco queue`"})
			}
		}
	}
}

func dueAny(d *store.DaemonDay, now time.Time) bool {
	for _, st := range d.Events {
		if !st.Done && !st.Next.After(now) {
			return true
		}
	}
	return false
}

func anyShown(d *store.DaemonDay) bool {
	for _, st := range d.Events {
		if st.Shown {
			return true
		}
	}
	return false
}

func (dm *Daemon) notify(ctx context.Context, n notify.Notification) error {
	if dm.client == nil {
		return errors.New("notifier not connected")
	}
	ctx, cancel := context.WithTimeout(ctx, notifyTimeout)
	defer cancel()
	err := dm.client.Notify(ctx, n)
	if err != nil {
		dm.Log.Printf("notify %s: %v", n.ID, err)
		dm.client.Close()
		dm.client = nil // reconnect on the next tick
	}
	return err
}

// test shows a reminder now as a dry run: answers are described, nothing is written.
func (dm *Daemon) test(ctx context.Context, ev Event, now time.Time) {
	dm.Store.Update(func(st *store.State) error { st.DaemonTest = ""; return nil })
	if !valid(ev) {
		dm.Log.Printf("test: unknown event %q", ev)
		return
	}
	var day *service.Day
	if got, err := dm.svc.Day(ctx, now); err == nil {
		day = &got
	}
	n := message(ev, dm.svc.Cfg, now, day, testID(ev, now))
	n.Subtitle = "Test — answers are not saved"
	if dm.notify(ctx, n) == nil {
		dm.Log.Printf("test: asked %s", ev)
	}
}

// answer carries out the user's answer to a notification.
func (dm *Daemon) answer(ctx context.Context, r notify.Response) {
	date, ev, test, ok := parseID(r.ID)
	if !ok {
		return // results and other plain notifications
	}
	if !dm.ready(ctx) {
		dm.Log.Printf("answer %s/%s lost: not ready", r.ID, r.Action)
		return
	}
	now := dm.Now()
	day := now
	if !test {
		var err error
		if day, err = time.ParseInLocation(timeutil.DateLayout, date, now.Location()); err != nil {
			return
		}
	}
	dm.Log.Printf("answer %s: action=%q text=%q dismissed=%v", r.ID, r.Action, r.Text, r.Dismissed)
	pause := func(ds string) error {
		return dm.Store.Update(func(st *store.State) error { st.Pause(timeutil.Date(now), ds); return nil })
	}
	p, err := decide(ev, r, dm.svc.Cfg, day, now, pause)
	if test {
		body := "Nothing was saved."
		switch {
		case err != nil:
			body = "The real answer would fail: " + err.Error()
		case p.what != "":
			body = "Would " + p.what + ". Nothing was saved."
		}
		dm.notify(ctx, result(r.ID, "Test answer: "+r.Action, body))
		return
	}

	st, lerr := dm.Store.Load()
	if lerr != nil {
		dm.Log.Printf("load state: %v", lerr)
		return
	}
	d := dm.today(st, now)
	sameDay := d.Date == date
	if err == nil && p.run != nil {
		var msg string
		msg, err = p.run(ctx, dm.svc)
		var q *service.QueuedError
		switch {
		case errors.As(err, &q):
			err = nil
			msg = fmt.Sprintf("MOCO not reachable — NOT saved yet. Queued #%d: %s. It is sent automatically.", q.Item.ID, q.Item.Summary())
			dm.notify(ctx, result(r.ID, "Queued", msg))
		case err == nil && msg != "":
			dm.notify(ctx, result(r.ID, "MOCO", msg))
		}
	}
	if err != nil {
		dm.Log.Printf("answer %s: %v", r.ID, err)
		dm.notify(ctx, result(r.ID, "Could not save your answer", err.Error()))
		if sameDay {
			// Ask again on the next tick (it is still due); if the error means it's settled
			// already (e.g. "already started"), the relevance check closes it.
			st := d.Events[string(ev)]
			st.Shown, st.Done = false, false
			if st.Next.After(now) {
				st.Next = now
			}
			dm.save(d)
		}
		return
	}
	if sameDay && p.effect != nil {
		p.effect(d)
		dm.save(d)
	}
}
