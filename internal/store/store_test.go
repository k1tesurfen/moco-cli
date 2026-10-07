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

func TestAddRecent(t *testing.T) {
	var st State
	now := time.Now()
	st.AddRecent(1, 10, now)
	st.AddRecent(2, 20, now)
	st.AddRecent(1, 10, now)
	if len(st.Recent) != 2 || st.Recent[0].ProjectID != 1 || st.Recent[1].ProjectID != 2 {
		t.Errorf("recent = %+v", st.Recent)
	}
	for i := 0; i < 40; i++ {
		st.AddRecent(int64(100+i), 1, now)
	}
	if len(st.Recent) != maxRecent {
		t.Errorf("len = %d", len(st.Recent))
	}
}
