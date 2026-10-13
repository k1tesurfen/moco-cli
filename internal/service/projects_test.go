package service

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/fakemoco"
)

// newJoinService: on intern; website (billable) and website2 are joinable; a colleague is on website.
func newJoinService(t *testing.T) (*Service, *fakemoco.Server) {
	s, fake := newTestService(t)
	fake.AddProject(intern)
	w := website
	w.Billable = true
	w.Contracts = []api.Contract{{ID: 900, UserID: 1, Active: true, Billable: true}}
	fake.AddOtherProject(w)
	fake.AddOtherProject(website2)
	return s, fake
}

func TestJoinableProjects(t *testing.T) {
	s, _ := newJoinService(t)
	ps, err := s.JoinableProjects(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(ps) != 2 || ps[0].ID != website.ID || ps[1].ID != website2.ID { // by name
		t.Errorf("joinable = %+v", ps)
	}
	if _, err := ResolveJoinable(ps, "internal"); err == nil || !strings.Contains(err.Error(), "no project you could join") {
		t.Errorf("resolve own project: %v", err)
	}
}

func TestJoinProjectCopiesBillableAndRefreshesCache(t *testing.T) {
	s, fake := newJoinService(t)
	ctx := context.Background()
	if ps, _ := s.Projects(ctx, true); len(ps) != 1 {
		t.Fatalf("assigned before = %+v", ps)
	}
	ps, _ := s.JoinableProjects(ctx)
	p, err := ResolveJoinable(ps, "acme")
	if err != nil {
		t.Fatal(err)
	}
	joined, err := s.JoinProject(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	if joined.ID != website.ID || len(ActiveTasks(joined)) != 1 {
		t.Errorf("joined = %+v", joined)
	}
	staff := fake.Staff(website.ID)
	if len(staff) != 2 || staff[1].UserID != fakemoco.UserID || !staff[1].Billable || !staff[1].Active {
		t.Errorf("staff = %+v", staff)
	}
	if cached, _ := s.Projects(ctx, false); len(cached) != 2 { // no refresh needed
		t.Errorf("cache after join = %+v", cached)
	}
	if ps, _ := s.JoinableProjects(ctx); len(ps) != 1 {
		t.Errorf("still joinable: %+v", ps)
	}
}

func TestJoinProjectReactivatesInactiveContract(t *testing.T) {
	s, fake := newTestService(t)
	p := website
	p.Contracts = []api.Contract{{ID: 900, UserID: fakemoco.UserID, Active: false}}
	fake.AddOtherProject(p)
	ctx := context.Background()
	ps, err := s.JoinableProjects(ctx)
	if err != nil || len(ps) != 1 {
		t.Fatalf("joinable = %+v, %v", ps, err)
	}
	if _, err := s.JoinProject(ctx, ps[0]); err != nil {
		t.Fatal(err)
	}
	if staff := fake.Staff(website.ID); len(staff) != 1 || staff[0].ID != 900 || !staff[0].Active {
		t.Errorf("staff = %+v", staff)
	}
}

func TestJoinProjectForbidden(t *testing.T) {
	s, fake := newJoinService(t)
	fake.Fail = func(r *http.Request) int {
		if r.Method == http.MethodPost {
			return http.StatusForbidden
		}
		return 0
	}
	_, err := s.JoinProject(context.Background(), website)
	if err == nil || !strings.Contains(err.Error(), "ask the project lead") {
		t.Errorf("err = %v", err)
	}
}

func TestLeaveProject(t *testing.T) {
	s, fake := newJoinService(t)
	ctx := context.Background()
	if _, err := s.JoinProject(ctx, website); err != nil {
		t.Fatal(err)
	}
	if err := s.LeaveProject(ctx, website); err != nil {
		t.Fatal(err)
	}
	if staff := fake.Staff(website.ID); len(staff) != 1 || staff[0].UserID != 1 {
		t.Errorf("staff = %+v", staff)
	}
	if cached, _ := s.Projects(ctx, false); len(cached) != 1 {
		t.Errorf("cache after leave = %+v", cached)
	}
	if err := s.LeaveProject(ctx, website); err == nil || !strings.Contains(err.Error(), "not on the team") {
		t.Errorf("leave twice: %v", err)
	}

	// With hours booked MOCO keeps the user on the project.
	fake.AddActivity(api.Activity{Date: "2026-10-06", Project: api.Ref{ID: intern.ID}, Task: api.Ref{ID: 11}, Seconds: 900})
	if err := s.LeaveProject(ctx, intern); err == nil || !strings.Contains(err.Error(), "without booked hours") {
		t.Errorf("leave with hours: %v", err)
	}
}
