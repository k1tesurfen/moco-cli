package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/store"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// TimerPlaceholder is the description of a timer activity started without one. MOCO shows it
// while the timer runs; `moco timer stop` replaces it.
const TimerPlaceholder = "⏱ timer running (moco-cli)"

// ErrNoTimer means no timer is running.
var ErrNoTimer = errors.New("no timer is running — start one with `moco timer start`")

// TimerRunningError means a timer is already running (MOCO allows one per user).
type TimerRunningError struct{ Activity api.Activity }

func (e *TimerRunningError) Error() string {
	return fmt.Sprintf("a timer is already running on %s / %s since %s", e.Activity.Project.Name, e.Activity.Task.Name,
		e.Activity.TimerStartedAt.Local().Format("15:04"))
}

// timerLookback is how far back RunningTimer looks for a forgotten timer.
const timerLookback = 7

// RunningTimer returns the activity with a running timer, or nil.
func (s *Service) RunningTimer(ctx context.Context) (*api.Activity, error) {
	now := s.Now()
	as, err := s.API.Activities(ctx, s.UserID, timeutil.Date(now.AddDate(0, 0, -timerLookback)), timeutil.Date(now))
	if err != nil {
		return nil, err
	}
	for i := range as {
		if as[i].TimerRunning() {
			return &as[i], nil
		}
	}
	return nil, nil
}

// TimerSeconds is the time tracked on an activity so far, including a running timer segment.
// MOCO keeps `seconds` unchanged while the timer runs (probed 2026-10-08).
func TimerSeconds(a api.Activity, now time.Time) int {
	sec := a.Seconds
	if a.TimerRunning() {
		sec += int(now.Sub(*a.TimerStartedAt).Seconds())
	}
	return sec
}

// HasDescription reports whether the activity has a real description (not the timer placeholder).
func HasDescription(a api.Activity) bool {
	d := strings.TrimSpace(a.Description)
	return d != "" && d != TimerPlaceholder
}

// StartTimer creates an activity for today on p/t and starts its timer. description may be
// empty; the placeholder is used until the timer is stopped. If a timer is already running, a
// *TimerRunningError is returned and nothing is changed.
func (s *Service) StartTimer(ctx context.Context, p api.Project, t api.Task, description string) (api.Activity, error) {
	running, err := s.RunningTimer(ctx)
	if err != nil {
		return api.Activity{}, err
	}
	if running != nil {
		return api.Activity{}, &TimerRunningError{*running}
	}
	description = strings.TrimSpace(description)
	if description == "" {
		description = TimerPlaceholder
	}
	zero := 0
	a, err := s.API.CreateActivity(ctx, api.ActivityInput{
		Date: timeutil.Date(s.Now()), ProjectID: p.ID, TaskID: t.ID, Seconds: &zero, Description: description,
	})
	if err != nil {
		return a, err
	}
	if a.TimerRunning() {
		// MOCO starts the timer by itself when a 0-second activity is created for today.
		s.remember(p.ID, t.ID)
		return a, nil
	}
	started, err := s.API.StartTimer(ctx, a.ID)
	if err != nil {
		// Don't leave an empty activity behind.
		if derr := s.API.DeleteActivity(context.WithoutCancel(ctx), a.ID); derr != nil {
			return a, fmt.Errorf("%w (and the empty activity %d could not be removed: %v)", err, a.ID, derr)
		}
		return a, err
	}
	s.remember(p.ID, t.ID)
	return started, nil
}

// TimerResult is a stopped timer.
type TimerResult struct {
	Activity api.Activity // as saved (rounded)
	Tracked  int          // seconds as tracked by MOCO before rounding
}

// StopTimer stops the running timer, rounds the tracked time up (at least one rounding step) and
// sets the description. description may be empty only if the activity already has a real one.
// If saving the rounded time fails because MOCO is unreachable, the update is queued and a
// *QueuedError is returned together with the result (the timer is stopped either way).
func (s *Service) StopTimer(ctx context.Context, description string) (TimerResult, error) {
	var res TimerResult
	running, err := s.RunningTimer(ctx)
	if err != nil {
		return res, err
	}
	if running == nil {
		return res, ErrNoTimer
	}
	description = strings.TrimSpace(description)
	if description == "" && !HasDescription(*running) {
		return res, errors.New("a description is required to stop the timer")
	}

	stopped, err := s.API.StopTimer(ctx, running.ID)
	if err != nil {
		return res, fmt.Errorf("could not stop the timer — check with `moco timer status`: %w", err)
	}
	res.Activity, res.Tracked = stopped, stopped.Seconds

	rounded := s.Round(stopped.Seconds)
	if min := s.Cfg.RoundingMinutes * 60; rounded < min {
		rounded = min
	}
	var in api.ActivityInput
	if rounded != stopped.Seconds {
		in.Seconds = &rounded
	}
	if description != "" && description != stopped.Description {
		in.Description = description
	}
	if in == (api.ActivityInput{}) {
		return res, nil
	}
	a, err := s.API.UpdateActivity(ctx, stopped.ID, in)
	if api.IsUnreachable(err) {
		item := store.QueueItem{Kind: store.KindEdit, Date: stopped.Date, ActivityID: stopped.ID,
			ProjectName: stopped.Project.Name, TaskName: stopped.Task.Name, Description: in.Description}
		if in.Seconds != nil {
			item.Seconds = *in.Seconds
		}
		return res, s.enqueue(item, err)
	}
	if err != nil {
		return res, fmt.Errorf("the timer is stopped (%s tracked), but saving the rounded time and description failed: %w",
			timeutil.FormatSeconds(stopped.Seconds), err)
	}
	res.Activity = a
	return res, nil
}

// CancelTimer stops the running timer and deletes its activity.
func (s *Service) CancelTimer(ctx context.Context) (api.Activity, error) {
	running, err := s.RunningTimer(ctx)
	if err != nil {
		return api.Activity{}, err
	}
	if running == nil {
		return api.Activity{}, ErrNoTimer
	}
	if _, err := s.API.StopTimer(ctx, running.ID); err != nil {
		return *running, fmt.Errorf("could not stop the timer — check with `moco timer status`: %w", err)
	}
	if err := s.API.DeleteActivity(ctx, running.ID); err != nil {
		return *running, fmt.Errorf("the timer is stopped, but activity %d could not be deleted: %w", running.ID, err)
	}
	return *running, nil
}
