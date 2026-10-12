package wizard

import (
	"context"
	"time"

	"github.com/k1tesurfen/moco-cli/internal/api"
	"github.com/k1tesurfen/moco-cli/internal/service"
	"github.com/k1tesurfen/moco-cli/internal/timeutil"
)

// NewEnv collects recents and aliases for the wizard. Aliases that no longer resolve are
// skipped and returned as warnings.
func NewEnv(svc *service.Service, projects []api.Project) (Env, []error, error) {
	recent, err := svc.RecentPicks(projects, 5)
	if err != nil {
		return Env{}, nil, err
	}
	var warnings []error
	aliases := map[string]service.Pick{}
	for name, al := range svc.Cfg.Aliases {
		p, t, err := service.ResolveAlias(projects, name, al)
		if err != nil {
			warnings = append(warnings, err)
			continue
		}
		aliases[name] = service.Pick{Project: p, Task: t}
	}
	return Env{
		Projects:        projects,
		Recent:          recent,
		Aliases:         aliases,
		LastTask:        svc.LastTask,
		Round:           svc.Round,
		RoundingMinutes: svc.Cfg.RoundingMinutes,
	}, warnings, nil
}

// Edit asks for every field of cur (current values preselected) and returns what changed.
func Edit(ctx context.Context, cur api.Activity, env Env) (service.ActivityChange, error) {
	env.AskAll = true
	env.Title = "Save changes?"
	d, _ := time.ParseInLocation(timeutil.DateLayout, cur.Date, time.Local)
	a := Activity{Date: d, Seconds: cur.Seconds, Description: cur.Description}
	for _, p := range env.Projects {
		if p.ID == cur.Project.ID {
			p := p
			a.Project = &p
			for _, t := range p.Tasks {
				if t.ID == cur.Task.ID {
					t := t
					a.Task = &t
				}
			}
		}
	}
	if err := Run(ctx, &a, env); err != nil {
		return service.ActivityChange{}, err
	}
	var ch service.ActivityChange
	if a.Project.ID != cur.Project.ID {
		ch.Project = a.Project
	}
	if a.Task.ID != cur.Task.ID || ch.Project != nil {
		ch.Task = a.Task
	}
	if a.Seconds != cur.Seconds {
		ch.Seconds = &a.Seconds
	}
	if a.Description != cur.Description {
		ch.Description = &a.Description
	}
	return ch, nil
}
