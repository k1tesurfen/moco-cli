package store

import (
	"sort"
	"time"
)

// DaemonDay is the reminder state of one day.
type DaemonDay struct {
	Date   string                 `json:"date"`
	Events map[string]*EventState `json:"events"`
}

// EventState tracks one reminder of the day.
type EventState struct {
	Next  time.Time `json:"next"`            // when it is due (again)
	Fired int       `json:"fired,omitempty"` // how often it was shown
	Done  bool      `json:"done,omitempty"`  // nothing more to ask today
	Shown bool      `json:"shown,omitempty"` // a notification is on screen and unanswered
	// Skipped means Done because MOCO already had the answer when it was due (not answered).
	Skipped bool `json:"skipped,omitempty"`
}

// Paused reports whether date is a day off.
func (s *State) Paused(date string) bool {
	for _, d := range s.Pauses {
		if d == date {
			return true
		}
	}
	return false
}

// Pause adds days off; dates before keepFrom are dropped to keep the list short.
func (s *State) Pause(keepFrom string, dates ...string) {
	set := map[string]bool{}
	for _, d := range append(s.Pauses, dates...) {
		if d >= keepFrom {
			set[d] = true
		}
	}
	s.Pauses = s.Pauses[:0]
	for d := range set {
		s.Pauses = append(s.Pauses, d)
	}
	sort.Strings(s.Pauses)
}

// Unpause removes a day off and reports whether it was there.
func (s *State) Unpause(date string) bool {
	for i, d := range s.Pauses {
		if d == date {
			s.Pauses = append(s.Pauses[:i], s.Pauses[i+1:]...)
			return true
		}
	}
	return false
}
