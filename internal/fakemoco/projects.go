package fakemoco

import (
	"encoding/json"
	"net/http"
	"regexp"
	"sort"
	"strconv"

	"github.com/k1tesurfen/moco-cli/internal/api"
)

var contractPath = regexp.MustCompile(`^/api/v1/projects/(\d+)/contracts(?:/(\d+))?$`)

// Staff returns the contracts of a project.
func (s *Server) Staff(projectID int64) []api.Contract {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]api.Contract{}, s.projects[projectID].Contracts...)
}

// handleProjects serves /projects, /projects/assigned and /projects/{id}/contracts[/{id}].
func (s *Server) handleProjects(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case path == "/api/v1/projects" && r.Method == http.MethodGet:
		json.NewEncoder(w).Encode(s.sortedProjects(func(api.Project) bool { return true }))
		return
	case path == "/api/v1/projects/assigned" && r.Method == http.MethodGet:
		out := s.sortedProjects(func(p api.Project) bool { return mine(p) != nil && mine(p).Active })
		for i := range out {
			k := mine(out[i])
			out[i].Contract = &struct {
				UserID int64 `json:"user_id"`
				Active bool  `json:"active"`
			}{k.UserID, k.Active}
			out[i].Contracts = nil
		}
		json.NewEncoder(w).Encode(out)
		return
	}
	m := contractPath.FindStringSubmatch(path)
	if m == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	pid, _ := strconv.ParseInt(m[1], 10, 64)
	p, ok := s.projects[pid]
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	var in struct {
		UserID   int64 `json:"user_id"`
		Active   *bool `json:"active"`
		Billable *bool `json:"billable"`
	}
	if r.Body != nil && r.Method != http.MethodGet && r.Method != http.MethodDelete {
		json.NewDecoder(r.Body).Decode(&in)
	}
	if m[2] == "" {
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(p.Contracts)
		case http.MethodPost:
			for _, k := range p.Contracts {
				if k.UserID == in.UserID {
					jsonError(w, http.StatusUnprocessableEntity, map[string][]string{"user_id": {"has already been taken"}})
					return
				}
			}
			s.nextID++
			k := api.Contract{ID: s.nextID, UserID: in.UserID, Active: in.Active == nil || *in.Active, Billable: in.Billable != nil && *in.Billable}
			p.Contracts = append(p.Contracts, k)
			s.projects[pid] = p
			json.NewEncoder(w).Encode(k)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	cid, _ := strconv.ParseInt(m[2], 10, 64)
	for i, k := range p.Contracts {
		if k.ID != cid {
			continue
		}
		switch r.Method {
		case http.MethodGet:
			json.NewEncoder(w).Encode(k)
		case http.MethodPut:
			if in.Active != nil {
				k.Active = *in.Active
			}
			if in.Billable != nil {
				k.Billable = *in.Billable
			}
			p.Contracts[i] = k
			json.NewEncoder(w).Encode(k)
		case http.MethodDelete:
			for _, a := range s.activities {
				if a.Project.ID == pid && a.User.ID == k.UserID {
					jsonError(w, http.StatusUnprocessableEntity, map[string][]string{"base": {"Contract has already tracked hours"}})
					return
				}
			}
			p.Contracts = append(p.Contracts[:i:i], p.Contracts[i+1:]...)
			s.projects[pid] = p
			json.NewEncoder(w).Encode(k)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}
	w.WriteHeader(http.StatusNotFound)
}

func (s *Server) sortedProjects(keep func(api.Project) bool) []api.Project {
	out := []api.Project{}
	for _, p := range s.projects {
		if keep(p) {
			p.Contracts = append([]api.Contract{}, p.Contracts...)
			out = append(out, p)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func mine(p api.Project) *api.Contract {
	for i := range p.Contracts {
		if p.Contracts[i].UserID == UserID {
			return &p.Contracts[i]
		}
	}
	return nil
}
