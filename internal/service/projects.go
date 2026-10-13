package service

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

// JoinableProjects returns the company's active projects the user is not actively on, by name.
// Their tasks become bookable after JoinProject.
func (s *Service) JoinableProjects(ctx context.Context) ([]api.Project, error) {
	all, err := s.API.AllProjects(ctx)
	if err != nil {
		return nil, err
	}
	var out []api.Project
	for _, p := range all {
		if k := s.contract(p); p.Active && (k == nil || !k.Active) {
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// ResolveJoinable finds exactly one of the joinable projects for query.
func ResolveJoinable(joinable []api.Project, query string) (api.Project, error) {
	return resolveProject(joinable, query, fmt.Errorf("no project you could join matches %q (`moco projects` lists the ones you are on)", query))
}

// contract is the user's entry in p.Contracts, if any.
func (s *Service) contract(p api.Project) *api.Contract {
	for i := range p.Contracts {
		if p.Contracts[i].UserID == s.UserID {
			return &p.Contracts[i]
		}
	}
	return nil
}

// JoinProject puts the user on the staff of p (an entry of JoinableProjects) – what "add
// myself to the team" does in the web app. Like every existing contract, the new one is
// billable exactly when the project is; an inactive one is reactivated. It returns the
// project as assigned, with its tasks, and refreshes the project cache.
func (s *Service) JoinProject(ctx context.Context, p api.Project) (api.Project, error) {
	var err error
	if k := s.contract(p); k != nil {
		active := true
		_, err = s.API.UpdateContract(ctx, p.ID, k.ID, api.ContractInput{Active: &active})
	} else {
		billable := p.Billable
		_, err = s.API.CreateContract(ctx, p.ID, api.ContractInput{UserID: s.UserID, Billable: &billable})
	}
	if err != nil {
		if api.IsStatus(err, http.StatusForbidden) {
			return api.Project{}, fmt.Errorf("MOCO doesn't let you join %s — your role can't change project teams, ask the project lead (%w)", p.Name, err)
		}
		return api.Project{}, fmt.Errorf("join %s: %w", p.Name, err)
	}
	projects, err := s.Projects(ctx, true)
	if err != nil {
		return api.Project{}, fmt.Errorf("joined %s, but reloading your projects failed: %w", p.Name, err)
	}
	for _, a := range projects {
		if a.ID == p.ID {
			return a, nil
		}
	}
	return api.Project{}, fmt.Errorf("joined %s, but MOCO doesn't list it among your projects yet — try `moco projects --refresh`", p.Name)
}

// LeaveProject takes the user off the staff of p and refreshes the project cache. MOCO only
// allows it while the user has no hours on the project.
func (s *Service) LeaveProject(ctx context.Context, p api.Project) error {
	staff, err := s.API.Contracts(ctx, p.ID)
	if err != nil {
		return err
	}
	var id int64
	for _, k := range staff {
		if k.UserID == s.UserID {
			id = k.ID
		}
	}
	if id == 0 {
		return fmt.Errorf("you are not on the team of %s", p.Name)
	}
	if err := s.API.DeleteContract(ctx, p.ID, id); err != nil {
		if api.IsStatus(err, http.StatusUnprocessableEntity) {
			return fmt.Errorf("MOCO keeps you on %s — it only removes team members without booked hours (%w)", p.Name, err)
		}
		return fmt.Errorf("leave %s: %w", p.Name, err)
	}
	_, err = s.Projects(ctx, true)
	return err
}
