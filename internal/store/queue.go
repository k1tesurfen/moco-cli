package store

import (
	"fmt"
	"time"
)

// Kinds of queued writes. Presence writes are queued as intents and replayed through the same
// service logic as the CLI, so they are checked against the day's state at sync time.
const (
	KindLog      = "log"      // create an activity
	KindStart    = "start"    // open a presence at From
	KindStop     = "stop"     // close the open presence at To
	KindBreak    = "break"    // split the presence covering From–To
	KindPresence = "presence" // create the closed or open presence From–To (rest of a half-done break)
	KindEdit     = "edit"     // set Seconds and/or Description of activity ActivityID (stopped timer)
)

// QueueItem is one write waiting for MOCO.
type QueueItem struct {
	ID       int64     `json:"id"`
	Kind     string    `json:"kind"`
	Date     string    `json:"date"`
	QueuedAt time.Time `json:"queued_at"`

	// log
	ProjectID   int64  `json:"project_id,omitempty"`
	ProjectName string `json:"project_name,omitempty"`
	TaskID      int64  `json:"task_id,omitempty"`
	TaskName    string `json:"task_name,omitempty"`
	Seconds     int    `json:"seconds,omitempty"` // already rounded
	Description string `json:"description,omitempty"`

	// edit
	ActivityID int64 `json:"activity_id,omitempty"`

	// presences ("HH:MM", already normalized)
	From string `json:"from,omitempty"`
	To   string `json:"to,omitempty"`
	Home *bool  `json:"home,omitempty"`
	// break: the presence being split, if it was known when queued (CoverTo "" = open). Lets the
	// replay finish a break whose first write reached MOCO although its answer got lost.
	CoverID int64  `json:"cover_id,omitempty"`
	CoverTo string `json:"cover_to,omitempty"`

	// sync bookkeeping
	Attempts  int    `json:"attempts,omitempty"`
	LastError string `json:"last_error,omitempty"`
	// Failed means MOCO rejected the item; it is not retried automatically (only by `moco queue sync`).
	Failed bool `json:"failed,omitempty"`
}

// Summary describes the item in one line.
func (q QueueItem) Summary() string {
	switch q.Kind {
	case KindLog:
		m := q.Seconds / 60
		return fmt.Sprintf("log %dh%02d · %s / %s · %s · %q", m/60, m%60, q.ProjectName, q.TaskName, q.Date, q.Description)
	case KindEdit:
		s := fmt.Sprintf("finish timer entry %d · %s / %s · %s", q.ActivityID, q.ProjectName, q.TaskName, q.Date)
		if q.Seconds > 0 {
			m := q.Seconds / 60
			s += fmt.Sprintf(" · %dh%02d", m/60, m%60)
		}
		if q.Description != "" {
			s += fmt.Sprintf(" · %q", q.Description)
		}
		return s
	case KindStart:
		s := fmt.Sprintf("start %s · %s", q.From, q.Date)
		if q.Home != nil {
			if *q.Home {
				s += " · home"
			} else {
				s += " · office"
			}
		}
		return s
	case KindStop:
		return fmt.Sprintf("stop %s · %s", q.To, q.Date)
	case KindBreak:
		return fmt.Sprintf("break %s–%s · %s", q.From, q.To, q.Date)
	case KindPresence:
		to := q.To
		if to == "" {
			to = "…"
		}
		return fmt.Sprintf("presence %s–%s · %s", q.From, to, q.Date)
	}
	return q.Kind + " · " + q.Date
}

// Enqueue appends an item, assigning id and queue time, and returns the stored item.
func (s *State) Enqueue(item QueueItem, now time.Time) QueueItem {
	s.QueueSeq++
	item.ID = s.QueueSeq
	item.QueuedAt = now
	s.Queue = append(s.Queue, item)
	return item
}

// QueueItem returns a pointer to the item with id, or nil.
func (s *State) QueueItem(id int64) *QueueItem {
	for i := range s.Queue {
		if s.Queue[i].ID == id {
			return &s.Queue[i]
		}
	}
	return nil
}

// Dequeue removes the item with id and reports whether it was there.
func (s *State) Dequeue(id int64) bool {
	for i := range s.Queue {
		if s.Queue[i].ID == id {
			s.Queue = append(s.Queue[:i], s.Queue[i+1:]...)
			return true
		}
	}
	return false
}

// QueueCounts returns the number of pending and failed items.
func (s *State) QueueCounts() (pending, failed int) {
	for _, q := range s.Queue {
		if q.Failed {
			failed++
		} else {
			pending++
		}
	}
	return pending, failed
}

// Drop removes a queued item without sending it.
func (s *Store) Drop(id int64) (QueueItem, error) {
	var item QueueItem
	err := s.Update(func(st *State) error {
		q := st.QueueItem(id)
		if q == nil {
			return fmt.Errorf("no queued item #%d", id)
		}
		item = *q
		st.Dequeue(id)
		return nil
	})
	return item, err
}
