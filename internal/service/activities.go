package service

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/config"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// FindProjects returns the projects matching query: an exact identifier or name match wins,
// otherwise every project whose name, customer or identifier contains all words of the query.
func FindProjects(projects []api.Project, query string) []api.Project {
	q := strings.TrimSpace(query)
	for _, p := range projects {
		if strings.EqualFold(p.Identifier, q) || strings.EqualFold(p.Name, q) {
			return []api.Project{p}
		}
	}
	words := strings.Fields(strings.ToLower(q))
	var out []api.Project
	for _, p := range projects {
		hay := strings.ToLower(p.Name + " " + p.Customer.Name + " " + p.Identifier)
		if containsAll(hay, words) {
			out = append(out, p)
		}
	}
	return out
}

// FindTasks returns the project's active tasks matching query (exact name match wins).
func FindTasks(p api.Project, query string) []api.Task {
	q := strings.TrimSpace(query)
	active := ActiveTasks(p)
	for _, t := range active {
		if strings.EqualFold(t.Name, q) {
			return []api.Task{t}
		}
	}
	words := strings.Fields(strings.ToLower(q))
	var out []api.Task
	for _, t := range active {
		if containsAll(strings.ToLower(t.Name), words) {
			out = append(out, t)
		}
	}
	return out
}

// ActiveTasks returns the active tasks of a project.
func ActiveTasks(p api.Project) []api.Task {
	var out []api.Task
	for _, t := range p.Tasks {
		if t.Active {
			out = append(out, t)
		}
	}
	return out
}

func containsAll(hay string, words []string) bool {
	for _, w := range words {
		if !strings.Contains(hay, w) {
			return false
		}
	}
	return true
}

// ResolveProject finds exactly one project for query.
func ResolveProject(projects []api.Project, query string) (api.Project, error) {
	found := FindProjects(projects, query)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return api.Project{}, fmt.Errorf("no assigned project matches %q (try `moco projects --refresh`)", query)
	}
	var names []string
	for i, p := range found {
		if i == 8 {
			names = append(names, fmt.Sprintf("… and %d more", len(found)-8))
			break
		}
		names = append(names, p.Identifier+" "+p.Name)
	}
	return api.Project{}, fmt.Errorf("%q matches %d projects:\n  %s", query, len(found), strings.Join(names, "\n  "))
}

// ResolveTask finds exactly one active task of p for query.
func ResolveTask(p api.Project, query string) (api.Task, error) {
	found := FindTasks(p, query)
	switch len(found) {
	case 1:
		return found[0], nil
	case 0:
		return api.Task{}, fmt.Errorf("project %q has no active task matching %q", p.Name, query)
	}
	var names []string
	for _, t := range found {
		names = append(names, t.Name)
	}
	return api.Task{}, fmt.Errorf("%q matches several tasks of %q: %s", query, p.Name, strings.Join(names, ", "))
}

// ResolveAlias returns the project and task an alias points to.
func ResolveAlias(projects []api.Project, name string, a config.Alias) (api.Project, api.Task, error) {
	p, err := ResolveProject(projects, a.Project)
	if err != nil {
		return p, api.Task{}, fmt.Errorf("alias %q: %w", name, err)
	}
	t, err := ResolveTask(p, a.Task)
	if err != nil {
		return p, t, fmt.Errorf("alias %q: %w", name, err)
	}
	return p, t, nil
}

// Pick is a project/task pair.
type Pick struct {
	Project api.Project
	Task    api.Task
}

// RecentPicks returns up to n recently used pairs that are still assigned and active.
func (s *Service) RecentPicks(projects []api.Project, n int) ([]Pick, error) {
	st, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	byID := map[int64]api.Project{}
	for _, p := range projects {
		byID[p.ID] = p
	}
	var out []Pick
	for _, r := range st.Recent {
		p, ok := byID[r.ProjectID]
		if !ok {
			continue
		}
		for _, t := range ActiveTasks(p) {
			if t.ID == r.TaskID {
				out = append(out, Pick{p, t})
				break
			}
		}
		if len(out) == n {
			break
		}
	}
	return out, nil
}

// LastTask returns the most recently used task id for a project (0 if none).
func (s *Service) LastTask(projectID int64) int64 {
	st, err := s.Store.Load()
	if err != nil {
		return 0
	}
	for _, r := range st.Recent {
		if r.ProjectID == projectID {
			return r.TaskID
		}
	}
	return 0
}

// Round applies the configured rounding (always up) to seconds.
func (s *Service) Round(sec int) int { return timeutil.RoundUp(sec, s.Cfg.RoundingMinutes) }

// LogActivity creates an activity, rounding the duration up, and remembers the pair as recent.
// If MOCO is not reachable, the activity is queued and a *QueuedError is returned.
func (s *Service) LogActivity(ctx context.Context, date time.Time, p api.Project, t api.Task, seconds int, description string) (api.Activity, error) {
	description = strings.TrimSpace(description)
	if description == "" {
		return api.Activity{}, fmt.Errorf("a description is required")
	}
	if seconds <= 0 {
		return api.Activity{}, fmt.Errorf("duration must be positive")
	}
	rounded := s.Round(seconds)
	a, err := s.API.CreateActivity(ctx, api.ActivityInput{
		Date: timeutil.Date(date), ProjectID: p.ID, TaskID: t.ID, Seconds: &rounded, Description: description,
	})
	if api.IsUnreachable(err) {
		s.remember(p.ID, t.ID)
		return a, s.enqueue(store.QueueItem{
			Kind: store.KindLog, Date: timeutil.Date(date), ProjectID: p.ID, ProjectName: p.Name,
			TaskID: t.ID, TaskName: t.Name, Seconds: rounded, Description: description,
		}, err)
	}
	if err != nil {
		return a, err
	}
	s.remember(p.ID, t.ID)
	return a, nil
}

func (s *Service) remember(projectID, taskID int64) {
	// Recents are a convenience; a failure here must not fail the logging.
	_ = s.Store.Update(func(st *store.State) error {
		st.AddRecent(projectID, taskID, s.Now())
		return nil
	})
}

// OwnActivity loads an activity and makes sure it belongs to the user. The token can read
// colleagues' activities, so every edit/delete goes through this check.
func (s *Service) OwnActivity(ctx context.Context, id int64) (api.Activity, error) {
	a, err := s.API.Activity(ctx, id)
	if err != nil {
		return a, err
	}
	if a.User.ID != s.UserID {
		return a, fmt.Errorf("activity %d belongs to %s %s, not to you", id, a.User.Firstname, a.User.Lastname)
	}
	return a, nil
}

// ActivityChange is a partial update; nil/zero fields stay unchanged.
type ActivityChange struct {
	Date        *time.Time
	Project     *api.Project
	Task        *api.Task
	Seconds     *int // unrounded; rounding is applied
	Description *string
}

// EditActivity applies a change to one of the user's activities.
func (s *Service) EditActivity(ctx context.Context, id int64, c ActivityChange) (api.Activity, error) {
	if _, err := s.OwnActivity(ctx, id); err != nil {
		return api.Activity{}, err
	}
	var in api.ActivityInput
	if c.Date != nil {
		in.Date = timeutil.Date(*c.Date)
	}
	if c.Project != nil {
		in.ProjectID = c.Project.ID
	}
	if c.Task != nil {
		in.TaskID = c.Task.ID
	}
	if c.Seconds != nil {
		if *c.Seconds <= 0 {
			return api.Activity{}, fmt.Errorf("duration must be positive")
		}
		r := s.Round(*c.Seconds)
		in.Seconds = &r
	}
	if c.Description != nil {
		d := strings.TrimSpace(*c.Description)
		if d == "" {
			return api.Activity{}, fmt.Errorf("a description is required")
		}
		in.Description = d
	}
	if in == (api.ActivityInput{}) {
		return api.Activity{}, fmt.Errorf("nothing to change")
	}
	a, err := s.API.UpdateActivity(ctx, id, in)
	if err == nil {
		s.remember(a.Project.ID, a.Task.ID)
	}
	return a, err
}

// DeleteActivity deletes one of the user's activities.
func (s *Service) DeleteActivity(ctx context.Context, id int64) (api.Activity, error) {
	a, err := s.OwnActivity(ctx, id)
	if err != nil {
		return a, err
	}
	return a, s.API.DeleteActivity(ctx, id)
}

// ActivitiesBetween lists the user's activities in [from, to], sorted by date and creation.
func (s *Service) ActivitiesBetween(ctx context.Context, from, to time.Time) ([]api.Activity, error) {
	as, err := s.API.Activities(ctx, s.UserID, timeutil.Date(from), timeutil.Date(to))
	if err != nil {
		return nil, err
	}
	sort.SliceStable(as, func(i, j int) bool {
		if as[i].Date != as[j].Date {
			return as[i].Date < as[j].Date
		}
		return as[i].CreatedAt.Before(as[j].CreatedAt)
	})
	return as, nil
}
