package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c := New("test", "secret", WithBaseURL(srv.URL+"/api/v1"))
	c.sleep = func(context.Context, time.Duration) error { return nil }
	c.limiter.SetLimit(1e6)
	return c
}

func TestSessionSendsToken(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Token token=secret" {
			t.Errorf("Authorization = %q", got)
		}
		if r.URL.Path != "/api/v1/session" {
			t.Errorf("path = %q", r.URL.Path)
		}
		fmt.Fprint(w, `{"id":933695178,"uuid":"abc"}`)
	})
	s, err := c.Session(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.ID != 933695178 || s.UUID != "abc" {
		t.Errorf("session = %+v", s)
	}
}

func TestActivitiesFollowsPagesAndSendsUserID(t *testing.T) {
	var srvURL string
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("user_id") != "7" || q.Get("from") != "2026-10-01" || q.Get("to") != "2026-10-07" {
			t.Errorf("query = %v", q)
		}
		if q.Get("page") == "2" {
			fmt.Fprint(w, `[{"id":3,"seconds":900,"timer_started_at":"2026-10-07T08:00:00Z"}]`)
			return
		}
		w.Header().Set("Link", fmt.Sprintf(`<%s/api/v1/activities?from=2026-10-01&page=2&to=2026-10-07&user_id=7>; rel="last", <%s/api/v1/activities?from=2026-10-01&page=2&to=2026-10-07&user_id=7>; rel="next"`, srvURL, srvURL))
		fmt.Fprint(w, `[{"id":1,"seconds":3600,"project":{"id":10,"name":"P"},"task":{"id":20,"name":"T"}},{"id":2,"seconds":1800,"timer_started_at":null}]`)
	})
	srvURL = strings.TrimSuffix(c.baseURL, "/api/v1")

	acts, err := c.Activities(context.Background(), 7, "2026-10-01", "2026-10-07")
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 3 {
		t.Fatalf("got %d activities, want 3", len(acts))
	}
	if acts[0].Project.Name != "P" || acts[0].Task.ID != 20 || acts[0].Seconds != 3600 {
		t.Errorf("first = %+v", acts[0])
	}
	if acts[1].TimerRunning() || !acts[2].TimerRunning() {
		t.Errorf("timer flags wrong: %v %v", acts[1].TimerRunning(), acts[2].TimerRunning())
	}
}

func TestRetriesOn429(t *testing.T) {
	calls := 0
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, `{"id":1}`)
	})
	if _, err := c.Session(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3", calls)
	}
}

func TestEmptyBodyError(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})
	_, err := c.Session(context.Background())
	if !IsStatus(err, http.StatusForbidden) {
		t.Fatalf("err = %v", err)
	}
	if IsUnreachable(err) {
		t.Error("403 must not count as unreachable")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("message = %q", err)
	}
}

func TestValidationErrorBody(t *testing.T) {
	for body, want := range map[string]string{
		`{"errors":["Task can't be blank","Date is invalid"]}`: "Task can't be blank; Date is invalid",
		`{"errors":{"seconds":["must be positive"]}}`:          "seconds: [must be positive]",
		`{"message":"Invalid payload"}`:                        "Invalid payload",
	} {
		c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusUnprocessableEntity)
			fmt.Fprint(w, body)
		})
		_, err := c.CreateActivity(context.Background(), ActivityInput{Description: "x"})
		var ae *APIError
		if !errors.As(err, &ae) || ae.Message != want {
			t.Errorf("body %s: err = %v, want message %q", body, err, want)
		}
	}
}

func TestServerErrorIsUnreachable(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	})
	_, err := c.Session(context.Background())
	if !IsUnreachable(err) {
		t.Errorf("502 should be unreachable: %v", err)
	}
}

func TestNetworkErrorIsUnreachable(t *testing.T) {
	c := New("test", "secret", WithBaseURL("http://127.0.0.1:1/api/v1"))
	_, err := c.Session(context.Background())
	if !IsUnreachable(err) {
		t.Errorf("connection refused should be unreachable: %v", err)
	}
}

func TestWriteSendsJSONBody(t *testing.T) {
	c := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPatch || r.URL.Path != "/api/v1/users/presences/5" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q", ct)
		}
		buf, _ := io.ReadAll(r.Body)
		if got := string(buf); got != `{"to":"13:00"}` {
			t.Errorf("body = %s", got)
		}
		fmt.Fprint(w, `{"id":5,"date":"2026-10-07","from":"08:00","to":"13:00"}`)
	})
	p, err := c.UpdatePresence(context.Background(), 5, PresenceInput{To: "13:00"})
	if err != nil {
		t.Fatal(err)
	}
	if p.To != "13:00" {
		t.Errorf("presence = %+v", p)
	}
}
