package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// fakeHelper answers like MocoNotifier and hands received commands to the test.
func fakeHelper(t *testing.T) (socket string, cmds chan map[string]any, reply func(any)) {
	t.Helper()
	dir, err := os.MkdirTemp("", "mn") // short path: Unix socket paths are limited to 104 bytes
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	socket = filepath.Join(dir, "n.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	cmds = make(chan map[string]any, 8)
	conns := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		conns <- conn
		sc := bufio.NewScanner(conn)
		for sc.Scan() {
			var m map[string]any
			json.Unmarshal(sc.Bytes(), &m)
			switch m["type"] {
			case "ping":
				json.NewEncoder(conn).Encode(map[string]string{"type": "pong", "version": "1", "authorization": "authorized"})
			case "notify":
				if m["title"] == "" {
					json.NewEncoder(conn).Encode(map[string]string{"type": "error", "id": m["id"].(string), "message": "notify needs id and title"})
				} else {
					json.NewEncoder(conn).Encode(map[string]string{"type": "delivered", "id": m["id"].(string)})
				}
			}
			cmds <- m
		}
	}()
	reply = func(v any) {
		conn := <-conns
		conns <- conn
		json.NewEncoder(conn).Encode(v)
	}
	return socket, cmds, reply
}

func TestRoundTrip(t *testing.T) {
	socket, cmds, reply := fakeHelper(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := Dial(ctx, socket, "")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	st, err := c.Ping(ctx)
	if err != nil || st.Authorization != "authorized" || st.Version != "1" {
		t.Fatalf("ping: %+v %v", st, err)
	}
	<-cmds

	n := Notification{ID: "start-2026-10-08", Category: "start", Title: "Did you start working?", Body: "…",
		Actions: []Action{{ID: "yes", Title: "Yes, 08:00"}, {ID: "other", Title: "Other time…", Input: true, Placeholder: "HH:MM"}}}
	if err := c.Notify(ctx, n); err != nil {
		t.Fatal(err)
	}
	got := <-cmds
	actions := got["actions"].([]any)
	if got["id"] != n.ID || got["category"] != "start" || len(actions) != 2 || actions[1].(map[string]any)["input"] != true {
		t.Errorf("notify sent %v", got)
	}

	if err := c.Notify(ctx, Notification{ID: "bad"}); err == nil {
		t.Error("want the helper's error")
	}
	<-cmds

	reply(map[string]any{"type": "response", "id": n.ID, "action": "other", "text": "8:15"})
	reply(map[string]any{"type": "response", "id": n.ID, "dismissed": true})
	if r := <-c.Responses; r != (Response{ID: n.ID, Action: "other", Text: "8:15"}) {
		t.Errorf("response = %+v", r)
	}
	if r := <-c.Responses; !r.Dismissed {
		t.Errorf("response = %+v", r)
	}

	if err := c.Remove(n.ID); err != nil {
		t.Fatal(err)
	}
	if got := <-cmds; got["type"] != "remove" || got["ids"].([]any)[0] != n.ID {
		t.Errorf("remove sent %v", got)
	}
}

func TestDialWithoutHelper(t *testing.T) {
	ctx := context.Background()
	if _, err := Dial(ctx, filepath.Join(t.TempDir(), "none.sock"), ""); err == nil {
		t.Error("want error without helper")
	}
	if _, err := Dial(ctx, filepath.Join(t.TempDir(), "none.sock"), "/nonexistent/MocoNotifier.app"); err == nil {
		t.Error("want error for a missing app")
	}
}
