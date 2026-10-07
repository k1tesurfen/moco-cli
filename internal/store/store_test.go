package store

import (
	"testing"
	"time"
)

func TestUpdateAndLoad(t *testing.T) {
	s := NewAt(t.TempDir())
	st, err := s.Load()
	if err != nil || st.Me != nil {
		t.Fatalf("empty load: %+v %v", st, err)
	}
	now := time.Now()
	err = s.Update(func(st *State) error {
		st.Me = &Me{Subdomain: "x", UserID: 42}
		st.Projects = &ProjectCache{FetchedAt: now}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err = s.Load()
	if err != nil {
		t.Fatal(err)
	}
	if st.Me.UserID != 42 || !st.Projects.Fresh(now.Add(time.Hour), 24*time.Hour) || st.Projects.Fresh(now.Add(25*time.Hour), 24*time.Hour) {
		t.Errorf("state = %+v", st)
	}
}
