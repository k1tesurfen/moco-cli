package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/fakemoco"
	"github.com/k1tesurfen/moco-cli/internal/notify"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/store"
)

// helper is a fake MocoNotifier that records commands.
type helper struct {
	mu   sync.Mutex
	cmds []map[string]any
}

func (h *helper) sent() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.cmds...)
}

// last returns the last command of a type, or nil.
func (h *helper) last(typ string) map[string]any {
	cmds := h.sent()
	for i := len(cmds) - 1; i >= 0; i-- {
		if cmds[i]["type"] == typ {
			return cmds[i]
		}
	}
	return nil
}

func startHelper(t *testing.T) (string, *helper) {
	t.Helper()
	dir, err := os.MkdirTemp("", "md")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket := filepath.Join(dir, "n.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	h := &helper{}
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				sc := bufio.NewScanner(conn)
				for sc.Scan() {
					var m map[string]any
					json.Unmarshal(sc.Bytes(), &m)
					h.mu.Lock()
					h.cmds = append(h.cmds, m)
					h.mu.Unlock()
					if m["type"] == "notify" {
						json.NewEncoder(conn).Encode(map[string]any{"type": "delivered", "id": m["id"]})
					}
				}
			}()
		}
	}()
	return socket, h
}

func newDaemon(t *testing.T) (*Daemon, *fakemoco.Server, *helper, *time.Time) {
	t.Helper()
	fake := fakemoco.New(t)
	socket, h := startHelper(t)
	clock := at(thu, "07:00")
	now := func() time.Time { return clock }
	fake.Now = now
	st := store.NewAt(t.TempDir())
	dm := &Daemon{
		Store:      st,
		Socket:     socket,
		Now:        now,
		Log:        log.New(io.Discard, "", 0),
		LoadConfig: func() (config.Config, error) { return config.Default(), nil },
		NewService: func(context.Context) (*service.Service, error) {
			return &service.Service{Cfg: config.Default(), API: fake.Client(), Store: st, UserID: fakemoco.UserID, Now: now}, nil
		},
	}
	return dm, fake, h, &clock
}

func TestDaemonDay(t *testing.T) {
	dm, fake, h, clock := newDaemon(t)
	ctx := context.Background()

	dm.tick(ctx)
	if h.last("notify") != nil {
		t.Fatal("nothing should be shown at 07:00")
	}

	*clock = at(thu, "08:00")
	dm.tick(ctx)
	n := h.last("notify")
	if n == nil || n["id"] != "2026-10-08.start" || !strings.HasPrefix(n["title"].(string), "Did you start") {
		t.Fatalf("start question: %v", n)
	}
	dm.tick(ctx) // no repeat within the same minute
	if c := len(h.sent()); c != 1 {
		var types []string
		for _, c := range h.sent() {
			types = append(types, c["type"].(string))
		}
		t.Fatalf("commands: %v", types)
	}

	*clock = at(thu, "08:03")
	dm.answer(ctx, notify.Response{ID: "2026-10-08.start", Action: actStartDefault})
	if got := fake.Dump("2026-10-08"); got != "08:00-…" {
		t.Fatalf("presence: %q", got)
	}
	if r := h.last("notify"); r["id"] != "2026-10-08.start.result" || !strings.Contains(r["body"].(string), "Started at 08:00") {
		t.Errorf("result: %v", r)
	}

	// 09:00: no re-ask, the day is started.
	*clock = at(thu, "09:00")
	before := len(h.sent())
	dm.tick(ctx)
	if len(h.sent()) != before {
		t.Errorf("unexpected command at 09:00: %v", h.sent()[before:])
	}

	// 12:30: 4h30 present, nothing logged.
	*clock = at(thu, "12:30")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.morning_log" || n["title"] != "Morning: 4h30 present, 0h00 logged" {
		t.Fatalf("morning: %v", n)
	}
	dm.answer(ctx, notify.Response{ID: "2026-10-08.morning_log", Action: actSnooze})
	*clock = at(thu, "13:00")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.morning_log" || n["title"] != "Morning: 5h00 present, 0h00 logged" {
		t.Fatalf("snoozed morning: %v", n)
	}
	dm.answer(ctx, notify.Response{ID: "2026-10-08.morning_log", Action: actDone})

	// 14:00: break question, answered with a different time.
	*clock = at(thu, "14:00")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.break" {
		t.Fatalf("break: %v", n)
	}
	dm.answer(ctx, notify.Response{ID: "2026-10-08.break", Action: actBreakOther, Text: "12.30 - 13.15"})
	if got := fake.Dump("2026-10-08"); got != "08:00-12:30 13:15-…" {
		t.Fatalf("after break: %q", got)
	}

	// A dry-run test of the end question writes nothing.
	dm.Store.Update(func(st *store.State) error { st.DaemonTest = "end"; return nil })
	dm.tick(ctx)
	tn := h.last("notify")
	if !strings.HasPrefix(tn["id"].(string), "test.end.") || tn["subtitle"] != "Test — answers are not saved" {
		t.Fatalf("test notification: %v", tn)
	}
	dm.answer(ctx, notify.Response{ID: tn["id"].(string), Action: actEndDefault})
	if r := h.last("notify"); !strings.Contains(r["body"].(string), "Would finish the day at 17:00") {
		t.Errorf("test result: %v", r)
	}
	if got := fake.Dump("2026-10-08"); got != "08:00-12:30 13:15-…" {
		t.Errorf("dry run changed MOCO: %q", got)
	}

	// 16:30: log everything first → the afternoon reminder is skipped.
	fake.AddActivity(api.Activity{Date: "2026-10-08", Seconds: 8 * 3600, Description: "work"})
	*clock = at(thu, "16:30")
	before = len(h.sent())
	dm.tick(ctx)
	if len(h.sent()) != before {
		t.Errorf("afternoon should be skipped: %v", h.sent()[before:])
	}

	// 17:00: end question; "Still working", then the day is finished in the CLI → the
	// shown question is taken off the screen at the next recheck.
	*clock = at(thu, "17:00")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.end" {
		t.Fatalf("end: %v", n)
	}
	dm.answer(ctx, notify.Response{ID: "2026-10-08.end", Action: actStillWorking})
	*clock = at(thu, "17:30")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.end" {
		t.Fatalf("end re-ask: %v", n)
	}
	if _, err := dm.svc.Stop(ctx, thu, "17:40"); err != nil {
		t.Fatal(err)
	}
	*clock = at(thu, "17:45")
	dm.tick(ctx)
	if r := h.last("remove"); r == nil || r["ids"].([]any)[0] != "2026-10-08.end" {
		t.Errorf("end question not removed: %v", r)
	}
}

func TestDaemonDayOffAndBadInput(t *testing.T) {
	dm, fake, h, clock := newDaemon(t)
	ctx := context.Background()
	*clock = at(thu, "08:00")
	dm.tick(ctx)

	// Unparseable time → error shown, question asked again on the next tick.
	dm.answer(ctx, notify.Response{ID: "2026-10-08.start", Action: actStartTime, Text: "half past eight"})
	if r := h.last("notify"); r["title"] != "Could not save your answer" {
		t.Fatalf("error result: %v", r)
	}
	*clock = at(thu, "08:01")
	dm.tick(ctx)
	if n := h.last("notify"); n["id"] != "2026-10-08.start" {
		t.Fatalf("asked again: %v", n)
	}

	dm.answer(ctx, notify.Response{ID: "2026-10-08.start", Action: actDayOff})
	st, _ := dm.Store.Load()
	if !st.Paused("2026-10-08") {
		t.Error("day off must pause the day")
	}
	*clock = at(thu, "17:00")
	before := len(h.sent())
	dm.tick(ctx)
	if len(h.sent()) != before || fake.Dump("2026-10-08") != "" {
		t.Errorf("day off: %v / %q", h.sent()[before:], fake.Dump("2026-10-08"))
	}
}
