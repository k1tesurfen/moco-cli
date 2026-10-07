// Package service holds the domain logic shared by CLI, TUI and daemon.
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

const projectCacheTTL = 24 * time.Hour

// Service combines config, API client and local state for one logged-in user.
type Service struct {
	Cfg    config.Config
	API    *api.Client
	Store  *store.Store
	UserID int64
	Now    func() time.Time
}

// Projects returns the assigned projects, from the cache unless it is stale or refresh is set.
func (s *Service) Projects(ctx context.Context, refresh bool) ([]api.Project, error) {
	st, err := s.Store.Load()
	if err != nil {
		return nil, err
	}
	if !refresh && st.Projects.Fresh(s.Now(), projectCacheTTL) {
		return st.Projects.Items, nil
	}
	projects, err := s.API.AssignedProjects(ctx)
	if err != nil {
		if st.Projects != nil && api.IsUnreachable(err) {
			return st.Projects.Items, nil // stale cache beats nothing when offline
		}
		return nil, err
	}
	sort.Slice(projects, func(i, j int) bool {
		return strings.ToLower(projects[i].Name) < strings.ToLower(projects[j].Name)
	})
	err = s.Store.Update(func(st *store.State) error {
		st.Projects = &store.ProjectCache{FetchedAt: s.Now(), Items: projects}
		return nil
	})
	return projects, err
}

// Day is the state of one day in MOCO.
type Day struct {
	Date           string
	Presences      []api.Presence
	Activities     []api.Activity
	PresentSeconds int // open presences count until now (today) or not at all (past days)
	LoggedSeconds  int
	OpenPresence   *api.Presence
	RunningTimer   *api.Activity
}

// Gap is present minus logged time (positive = still to log).
func (d Day) Gap() int { return d.PresentSeconds - d.LoggedSeconds }

// Day loads presences and activities of the given date.
func (s *Service) Day(ctx context.Context, date time.Time) (Day, error) {
	ds := timeutil.Date(date)
	presences, err := s.API.Presences(ctx, s.UserID, ds, ds)
	if err != nil {
		return Day{}, err
	}
	activities, err := s.API.Activities(ctx, s.UserID, ds, ds)
	if err != nil {
		return Day{}, err
	}
	return buildDay(ds, presences, activities, s.Now())
}

func buildDay(date string, presences []api.Presence, activities []api.Activity, now time.Time) (Day, error) {
	d := Day{Date: date, Presences: presences, Activities: activities}
	sort.Slice(d.Presences, func(i, j int) bool { return d.Presences[i].From < d.Presences[j].From })
	day, err := time.ParseInLocation(timeutil.DateLayout, date, now.Location())
	if err != nil {
		return d, err
	}
	isToday := timeutil.Date(now) == date
	for i := range d.Presences {
		p := &d.Presences[i]
		from, err := timeutil.ParseClock(day, p.From)
		if err != nil {
			return d, fmt.Errorf("presence %d: %w", p.ID, err)
		}
		if p.To == "" {
			d.OpenPresence = p
			if isToday && now.After(from) {
				d.PresentSeconds += int(now.Sub(from).Seconds())
			}
			continue
		}
		to, err := timeutil.ParseClock(day, p.To)
		if err != nil {
			return d, fmt.Errorf("presence %d: %w", p.ID, err)
		}
		d.PresentSeconds += int(to.Sub(from).Seconds())
	}
	for i := range d.Activities {
		a := &d.Activities[i]
		d.LoggedSeconds += a.Seconds
		if a.TimerRunning() {
			// Assumes `seconds` excludes the running segment; verify in Phase 5 (timer).
			d.RunningTimer = a
			d.LoggedSeconds += int(now.Sub(*a.TimerStartedAt).Seconds())
		}
	}
	return d, nil
}
