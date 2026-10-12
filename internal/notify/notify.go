// Package notify is the Go side of the MocoNotifier protocol: newline-delimited JSON over the
// Unix socket the helper app listens on (see notifier/Sources/MocoNotifier/Protocol.swift).
package notify

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Action is one button of a notification. Input turns it into a text-reply action.
type Action struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Input       bool   `json:"input,omitempty"`
	Placeholder string `json:"placeholder,omitempty"`
	Button      string `json:"button,omitempty"`
	Destructive bool   `json:"destructive,omitempty"`
}

// Notification is shown by the helper. Posting again with the same ID replaces it.
type Notification struct {
	ID       string   `json:"id"`
	Category string   `json:"category"`
	Title    string   `json:"title"`
	Subtitle string   `json:"subtitle,omitempty"`
	Body     string   `json:"body"`
	Actions  []Action `json:"actions,omitempty"`
	Sound    bool     `json:"sound,omitempty"`
}

// Response is what the user did with a notification. Action is an Action.ID, or "default" when
// the notification itself was clicked; Text is the reply of an input action.
type Response struct {
	ID        string
	Action    string
	Text      string
	Dismissed bool
}

// Status is the helper's answer to a ping.
type Status struct {
	Version       string
	Authorization string // authorized, denied, notDetermined, provisional
}

type command struct {
	Type string `json:"type"`
	*Notification
	IDs []string `json:"ids,omitempty"`
}

type event struct {
	Type          string `json:"type"`
	ID            string `json:"id"`
	Action        string `json:"action"`
	Text          string `json:"text"`
	Dismissed     bool   `json:"dismissed"`
	Authorization string `json:"authorization"`
	Version       string `json:"version"`
	Message       string `json:"message"`
}

// DefaultApp is where `make install` puts the helper.
func DefaultApp() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, "Applications", "MocoNotifier.app")
}

// Client is a connection to the helper.
type Client struct {
	conn net.Conn

	wmu sync.Mutex
	enc *json.Encoder

	mu        sync.Mutex
	pongs     chan Status
	delivered map[string]chan error

	// Responses receives the user's answers to all notifications (every client gets them;
	// ignore ids you don't own). It is closed when the connection ends.
	Responses <-chan Response
	responses chan Response
	// Errors receives errors the helper reports that belong to no pending request.
	Errors chan error
}

// Dial connects to the helper at socket. If nothing listens there and app is not empty, the app
// is launched (in the background) and Dial waits for it until ctx ends.
func Dial(ctx context.Context, socket, app string) (*Client, error) {
	conn, err := dial(socket)
	if err == nil {
		return newClient(conn), nil
	}
	if app == "" {
		return nil, err
	}
	if _, serr := os.Stat(app); serr != nil {
		return nil, fmt.Errorf("notifier app not found at %s — run `make install`", app)
	}
	// -g: don't bring it to the front. Arguments only reach a freshly launched app.
	if out, err := exec.CommandContext(ctx, "open", "-g", "-a", app, "--args", "--socket", socket).CombinedOutput(); err != nil {
		return nil, fmt.Errorf("launch %s: %v: %s", app, err, out)
	}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("notifier did not start listening on %s: %w", socket, ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		if conn, err := dial(socket); err == nil {
			return newClient(conn), nil
		}
	}
}

func dial(socket string) (net.Conn, error) {
	return net.DialTimeout("unix", socket, time.Second)
}

func newClient(conn net.Conn) *Client {
	c := &Client{
		conn:      conn,
		enc:       json.NewEncoder(conn),
		pongs:     make(chan Status, 1),
		delivered: map[string]chan error{},
		responses: make(chan Response, 16),
		Errors:    make(chan error, 16),
	}
	c.Responses = c.responses
	go c.read()
	return c
}

func (c *Client) read() {
	defer close(c.responses)
	sc := bufio.NewScanner(c.conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	for sc.Scan() {
		var ev event
		if err := json.Unmarshal(sc.Bytes(), &ev); err != nil {
			c.report(fmt.Errorf("notifier sent invalid JSON: %v", err))
			continue
		}
		switch ev.Type {
		case "pong":
			select {
			case c.pongs <- Status{Version: ev.Version, Authorization: ev.Authorization}:
			default:
			}
		case "delivered", "error":
			var err error
			if ev.Type == "error" {
				err = errors.New("notifier: " + ev.Message)
			}
			c.mu.Lock()
			ch, ok := c.delivered[ev.ID]
			delete(c.delivered, ev.ID)
			c.mu.Unlock()
			if ok {
				ch <- err
			} else if err != nil {
				c.report(err)
			}
		case "response":
			c.responses <- Response{ID: ev.ID, Action: ev.Action, Text: ev.Text, Dismissed: ev.Dismissed}
		}
	}
}

func (c *Client) report(err error) {
	select {
	case c.Errors <- err:
	default:
	}
}

func (c *Client) send(cmd command) error {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	return c.enc.Encode(cmd)
}

// Ping asks the helper for its version and notification permission.
func (c *Client) Ping(ctx context.Context) (Status, error) {
	if err := c.send(command{Type: "ping"}); err != nil {
		return Status{}, err
	}
	select {
	case s := <-c.pongs:
		return s, nil
	case <-ctx.Done():
		return Status{}, fmt.Errorf("no answer from the notifier: %w", ctx.Err())
	}
}

// Notify shows n and waits until the helper has handed it to macOS.
func (c *Client) Notify(ctx context.Context, n Notification) error {
	ch := make(chan error, 1)
	c.mu.Lock()
	c.delivered[n.ID] = ch
	c.mu.Unlock()
	if err := c.send(command{Type: "notify", Notification: &n}); err != nil {
		return err
	}
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.delivered, n.ID)
		c.mu.Unlock()
		return fmt.Errorf("notifier did not confirm %s: %w", n.ID, ctx.Err())
	}
}

// Remove withdraws notifications (e.g. when the question was answered elsewhere).
func (c *Client) Remove(ids ...string) error {
	return c.send(command{Type: "remove", IDs: ids})
}

// Quit stops the helper.
func (c *Client) Quit() error { return c.send(command{Type: "quit"}) }

// Close closes the connection; the helper keeps running.
func (c *Client) Close() error { return c.conn.Close() }
