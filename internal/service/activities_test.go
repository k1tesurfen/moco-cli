package service

import (
	"context"
	"strings"
	"testing"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/fakemoco"
)

var (
	intern = api.Project{ID: 1, Identifier: "P.K0001.22.0110", Name: "Intern – nicht verrechenbar",
		Customer: api.Ref{ID: 9, Name: "artismedia GmbH"},
		Tasks: []api.Task{
			{ID: 11, Name: "Programmierung", Active: true},
			{ID: 12, Name: "Projektmanagement", Active: true},
			{ID: 13, Name: "Projektleitung", Active: true},
			{ID: 14, Name: "Alt", Active: false},
		}}
	website = api.Project{ID: 2, Identifier: "P.20530.24.0707", Name: "Website scheidtmann.green",
		Customer: api.Ref{ID: 8, Name: "Scheidtmann GmbH"},
		Tasks:    []api.Task{{ID: 21, Name: "Kreation", Active: true}}}
	website2 = api.Project{ID: 3, Identifier: "P.1", Name: "Website bwgruen.de",
		Customer: api.Ref{ID: 7, Name: "Förderungsgesellschaft"}}
	projects = []api.Project{intern, website, website2}
)

func TestResolveProject(t *testing.T) {
	for q, want := range map[string]int64{
		"P.K0001.22.0110": 1, "intern": 1, "scheidt": 2, "website scheidt": 2, "Website bwgruen.de": 3, "artismedia": 1,
	} {
		p, err := ResolveProject(projects, q)
		if err != nil || p.ID != want {
			t.Errorf("ResolveProject(%q) = %d, %v; want %d", q, p.ID, err, want)
		}
	}
	if _, err := ResolveProject(projects, "website"); err == nil || !strings.Contains(err.Error(), "matches 2 projects") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := ResolveProject(projects, "nope"); err == nil {
		t.Error("expected no match")
	}
}

func TestResolveTask(t *testing.T) {
	if tk, err := ResolveTask(intern, "prog"); err != nil || tk.ID != 11 {
		t.Errorf("prog: %v %v", tk, err)
	}
	if _, err := ResolveTask(intern, "projekt"); err == nil || !strings.Contains(err.Error(), "several") {
		t.Errorf("ambiguous: %v", err)
	}
	if _, err := ResolveTask(intern, "alt"); err == nil {
		t.Error("inactive task must not match")
	}
}

func TestResolveAlias(t *testing.T) {
	p, tk, err := ResolveAlias(projects, "dev", config.Alias{Project: "intern", Task: "Programmierung"})
	if err != nil || p.ID != 1 || tk.ID != 11 {
		t.Errorf("%v %v %v", p.ID, tk.ID, err)
	}
	if _, _, err := ResolveAlias(projects, "x", config.Alias{Project: "website", Task: "Kreation"}); err == nil || !strings.HasPrefix(err.Error(), `alias "x"`) {
		t.Errorf("err = %v", err)
	}
}

func newActivityService(t *testing.T) (*Service, *fakemoco.Server) {
	s, fake := newTestService(t)
	for _, p := range projects {
		fake.AddProject(p)
	}
	return s, fake
}

func TestLogActivityRoundsUpAndRemembers(t *testing.T) {
	s, fake := newActivityService(t)
	a, err := s.LogActivity(context.Background(), day, intern, intern.Tasks[0], 67*60, "  Fix login  ")
	if err != nil {
		t.Fatal(err)
	}
	if a.Seconds != 75*60 || a.Description != "Fix login" || a.Task.Name != "Programmierung" {
		t.Errorf("activity = %+v", a)
	}
	if got := fake.Activities("2026-10-06"); len(got) != 1 {
		t.Errorf("stored = %+v", got)
	}
	picks, err := s.RecentPicks(projects, 5)
	if err != nil || len(picks) != 1 || picks[0].Task.ID != 11 {
		t.Errorf("recent = %+v, %v", picks, err)
	}
	if s.LastTask(1) != 11 || s.LastTask(2) != 0 {
		t.Error("LastTask wrong")
	}
}

func TestLogActivityValidation(t *testing.T) {
	s, fake := newActivityService(t)
	ctx := context.Background()
	if _, err := s.LogActivity(ctx, day, intern, intern.Tasks[0], 900, "  "); err == nil {
		t.Error("empty description accepted")
	}
	if _, err := s.LogActivity(ctx, day, intern, intern.Tasks[0], 0, "x"); err == nil {
		t.Error("zero duration accepted")
	}
	if len(fake.Requests) != 0 {
		t.Errorf("requests sent: %v", fake.Requests)
	}
}

func TestEditAndDeleteOnlyOwnActivities(t *testing.T) {
	s, fake := newActivityService(t)
	ctx := context.Background()
	mine := fake.AddActivity(api.Activity{Date: "2026-10-06", Project: api.Ref{ID: 1}, Task: api.Ref{ID: 11}, Seconds: 900, Description: "a"})
	theirs := fake.AddActivity(api.Activity{Date: "2026-10-06", Project: api.Ref{ID: 1}, Task: api.Ref{ID: 11}, Seconds: 900,
		User: api.UserRef{ID: 1, Firstname: "Daniel", Lastname: "G"}})

	sec, desc := 20*60, "edited"
	a, err := s.EditActivity(ctx, mine, ActivityChange{Seconds: &sec, Description: &desc})
	if err != nil {
		t.Fatal(err)
	}
	if a.Seconds != 30*60 || a.Description != "edited" {
		t.Errorf("edited = %+v", a)
	}
	if _, err := s.EditActivity(ctx, theirs, ActivityChange{Description: &desc}); err == nil || !strings.Contains(err.Error(), "not to you") {
		t.Errorf("edit theirs: %v", err)
	}
	if _, err := s.DeleteActivity(ctx, theirs); err == nil {
		t.Error("deleted a colleague's activity")
	}
	if _, err := s.DeleteActivity(ctx, mine); err != nil {
		t.Fatal(err)
	}
	if got := fake.Activities("2026-10-06"); len(got) != 1 || got[0].ID != theirs {
		t.Errorf("remaining = %+v", got)
	}
}
