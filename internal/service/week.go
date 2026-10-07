package service

import (
	"context"
	"sort"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// Monday returns the Monday of the week containing t (at midnight).
func Monday(t time.Time) time.Time {
	y, m, d := t.Date()
	day := time.Date(y, m, d, 0, 0, 0, 0, t.Location())
	return day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
}

// Days loads every day from from to to (inclusive) with two requests.
func (s *Service) Days(ctx context.Context, from, to time.Time) ([]Day, error) {
	presences, err := s.API.Presences(ctx, s.UserID, timeutil.Date(from), timeutil.Date(to))
	if err != nil {
		return nil, err
	}
	activities, err := s.API.Activities(ctx, s.UserID, timeutil.Date(from), timeutil.Date(to))
	if err != nil {
		return nil, err
	}
	ps := map[string][]api.Presence{}
	for _, p := range presences {
		ps[p.Date] = append(ps[p.Date], p)
	}
	as := map[string][]api.Activity{}
	for _, a := range activities {
		as[a.Date] = append(as[a.Date], a)
	}
	var days []Day
	for d := from; !d.After(to); d = d.AddDate(0, 0, 1) {
		ds := timeutil.Date(d)
		acts := as[ds]
		sort.SliceStable(acts, func(i, j int) bool { return acts[i].CreatedAt.Before(acts[j].CreatedAt) })
		day, err := buildDay(ds, ps[ds], acts, s.Now())
		if err != nil {
			return nil, err
		}
		days = append(days, day)
	}
	return days, nil
}

// Hours sums tracked seconds (including running timers) per project and per task.
type Hours struct {
	Total     int
	ByProject map[int64]int
	ByTask    map[int64]map[int64]int // project id → task id → seconds
}

// SumHours aggregates activities into Hours.
func SumHours(acts []api.Activity, now time.Time) Hours {
	h := Hours{ByProject: map[int64]int{}, ByTask: map[int64]map[int64]int{}}
	for _, a := range acts {
		sec := TimerSeconds(a, now)
		h.Total += sec
		h.ByProject[a.Project.ID] += sec
		if h.ByTask[a.Project.ID] == nil {
			h.ByTask[a.Project.ID] = map[int64]int{}
		}
		h.ByTask[a.Project.ID][a.Task.ID] += sec
	}
	return h
}
