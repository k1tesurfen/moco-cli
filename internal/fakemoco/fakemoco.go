// Package fakemoco is an in-memory MOCO API for tests. It mimics the behaviour observed on the
// real API (see PLAN.md, "Write probe results"): one open presence at a time, overlap → 422 field
// map, malformed times → 500, is_home_office shared by all presences of a day.
package fakemoco

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

const UserID = 933695178

// Server is a fake MOCO. Read Presences/Activities only via the accessor methods.
type Server struct {
	URL string // base URL including /api/v1

	mu         sync.Mutex
	nextID     int64
	presences  map[int64]*api.Presence
	activities map[int64]*api.Activity
	Requests   []string // "METHOD /path" log
}

// New starts a fake MOCO that is closed when the test ends.
func New(t *testing.T) *Server {
	t.Helper()
	s := &Server{nextID: 100, presences: map[int64]*api.Presence{}, activities: map[int64]*api.Activity{}}
	srv := httptest.NewServer(http.HandlerFunc(s.handle))
	t.Cleanup(srv.Close)
	s.URL = srv.URL + "/api/v1"
	return s
}

// Client returns an api.Client pointing at the fake.
func (s *Server) Client() *api.Client {
	return api.New("fake", "token", api.WithBaseURL(s.URL))
}

// AddPresence seeds a presence and returns its id.
func (s *Server) AddPresence(p api.Presence) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	p.ID = s.nextID
	s.presences[p.ID] = &p
	return p.ID
}

// Presences returns the presences of a day sorted by from.
func (s *Server) Presences(date string) []api.Presence {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []api.Presence
	for _, p := range s.presences {
		if p.Date == date {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].From < out[j].From })
	return out
}

var (
	hhmm      = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)
	idPath    = regexp.MustCompile(`^/api/v1/(users/presences|activities)/(\d+)$`)
	jsonError = func(w http.ResponseWriter, status int, body any) {
		w.WriteHeader(status)
		json.NewEncoder(w).Encode(body)
	}
)

func (s *Server) handle(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Requests = append(s.Requests, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	path := r.URL.Path

	switch {
	case path == "/api/v1/session" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(api.Session{ID: UserID, UUID: "fake"})
	case path == "/api/v1/users/presences" && r.Method == http.MethodGet:
		s.listPresences(w, r)
	case path == "/api/v1/users/presences" && r.Method == http.MethodPost:
		s.writePresence(w, r, nil)
	case path == "/api/v1/activities" && r.Method == http.MethodGet:
		s.listActivities(w, r)
	default:
		m := idPath.FindStringSubmatch(path)
		if m == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		id, _ := strconv.ParseInt(m[2], 10, 64)
		if m[1] == "users/presences" {
			p, ok := s.presences[id]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			switch r.Method {
			case http.MethodPatch:
				s.writePresence(w, r, p)
			case http.MethodDelete:
				delete(s.presences, id)
				json.NewEncoder(w).Encode(p)
			default:
				w.WriteHeader(http.StatusNotFound)
			}
			return
		}
		a, ok := s.activities[id]
		if !ok || r.Method != http.MethodDelete {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(s.activities, id)
		json.NewEncoder(w).Encode(a)
	}
}

func (s *Server) listPresences(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	out := []api.Presence{}
	for _, p := range s.presences {
		if p.Date >= from && p.Date <= to {
			out = append(out, *p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Date+out[i].From < out[j].Date+out[j].From })
	json.NewEncoder(w).Encode(out)
}

func (s *Server) listActivities(w http.ResponseWriter, r *http.Request) {
	from, to := r.URL.Query().Get("from"), r.URL.Query().Get("to")
	out := []api.Activity{}
	for _, a := range s.activities {
		if a.Date >= from && a.Date <= to {
			out = append(out, *a)
		}
	}
	json.NewEncoder(w).Encode(out)
}

// writePresence handles POST (existing == nil) and PATCH.
func (s *Server) writePresence(w http.ResponseWriter, r *http.Request, existing *api.Presence) {
	var in struct {
		Date         *string `json:"date"`
		From         *string `json:"from"`
		To           *string `json:"to"`
		IsHomeOffice *bool   `json:"is_home_office"`
	}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		jsonError(w, http.StatusUnprocessableEntity, map[string]any{"message": "Invalid payload"})
		return
	}
	p := api.Presence{}
	if existing != nil {
		p = *existing
	}
	if in.Date != nil {
		p.Date = *in.Date
	}
	if in.From != nil {
		p.From = *in.From
	}
	if in.To != nil {
		p.To = *in.To
	}
	for _, v := range []string{p.From, p.To} {
		if v != "" && !hhmm.MatchString(v) {
			w.WriteHeader(http.StatusInternalServerError) // real MOCO does this
			fmt.Fprint(w, "500 Internal Server Error")
			return
		}
	}
	if p.Date == "" || p.From == "" {
		jsonError(w, http.StatusUnprocessableEntity, map[string][]string{"from": {"muss ausgefüllt werden"}})
		return
	}
	if p.To != "" && p.To <= p.From {
		jsonError(w, http.StatusUnprocessableEntity, map[string][]string{"to": {"must be after from"}})
		return
	}
	for _, o := range s.presences {
		if o.Date != p.Date || o.ID == p.ID {
			continue
		}
		if overlaps(p, *o) {
			jsonError(w, http.StatusUnprocessableEntity, map[string][]string{"from": {"range overlaps"}})
			return
		}
	}
	now := time.Now().UTC()
	if existing == nil {
		s.nextID++
		p.ID = s.nextID
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	stored := p
	s.presences[p.ID] = &stored
	// is_home_office is a per-day attribute.
	home := stored.IsHomeOffice
	if in.IsHomeOffice != nil {
		home = *in.IsHomeOffice
	} else if existing == nil {
		home = s.dayHome(p.Date, p.ID)
	}
	for _, o := range s.presences {
		if o.Date == p.Date {
			o.IsHomeOffice = home
		}
	}
	json.NewEncoder(w).Encode(s.presences[p.ID])
}

func (s *Server) dayHome(date string, except int64) bool {
	for _, o := range s.presences {
		if o.Date == date && o.ID != except {
			return o.IsHomeOffice
		}
	}
	return false
}

// overlaps treats an open presence as running until the end of the day.
func overlaps(a, b api.Presence) bool {
	end := func(p api.Presence) string {
		if p.To == "" {
			return "24:00"
		}
		return p.To
	}
	return a.From < end(b) && b.From < end(a)
}

// Dump renders the presences of a day compactly, e.g. "08:00-13:00 14:00-… home".
func (s *Server) Dump(date string) string {
	var parts []string
	home := false
	for _, p := range s.Presences(date) {
		to := p.To
		if to == "" {
			to = "…"
		}
		parts = append(parts, p.From+"-"+to)
		home = p.IsHomeOffice
	}
	if len(parts) > 0 && home {
		parts = append(parts, "home")
	}
	return strings.Join(parts, " ")
}
