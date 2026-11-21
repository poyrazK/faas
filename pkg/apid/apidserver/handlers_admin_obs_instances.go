package apidserver

// Operator fleet instance list and instance lookup (ADR-20261121161842).
//
//	GET /v1/admin/obs/instances       — resident instances across every node
//	GET /v1/admin/obs/instances/{id}  — one instance in any state

import (
	"context"
	"errors"
	"net/http"
	"sort"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type fleetInstanceFilter struct {
	running                bool
	node, appID, accountID string
	limit                  int
}

func (s *server) obsListInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	filter, prob := parseFleetInstanceFilter(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	out, err := s.fleetInstances(r.Context(), filter)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list compute nodes"))
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, out)
}

func parseFleetInstanceFilter(r *http.Request) (fleetInstanceFilter, *api.Problem) {
	q := r.URL.Query()
	f := fleetInstanceFilter{running: true, node: q.Get("node"), appID: q.Get("app_id"), accountID: q.Get("account_id")}
	switch q.Get("state") {
	case "", "running":
	case "resident":
		f.running = false
	default:
		return f, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad instance filter", "state must be running or resident")
	}
	for _, id := range []string{f.appID, f.accountID} {
		if id != "" {
			if _, err := uuid.Parse(id); err != nil {
				return f, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad instance filter", "app_id and account_id must be UUIDs")
			}
		}
	}
	prob, limit := api.ParseLimit(q.Get("limit"), api.ObsInstanceListDefault, api.ObsInstanceListMax, "instances")
	f.limit = limit
	return f, prob
}

// fleetInstances reads each node's resident instances, keeps the ones the
// filter selects and orders them most recently used first. A node whose read
// fails is reported, not fatal.
func (s *server) fleetInstances(ctx context.Context, f fleetInstanceFilter) (api.ObsInstanceListResponse, error) {
	out := api.ObsInstanceListResponse{Items: []api.ObsInstanceRow{}, Limit: f.limit}
	nodes, err := s.store.ListComputeNodes(ctx, true)
	if err != nil {
		return out, err
	}
	apps := map[string]state.App{}
	nodeNames := map[string]string{}
	var rows []state.Instance
	for _, node := range nodes {
		if f.node != "" && node.Name != f.node {
			continue
		}
		nodeNames[node.ID] = node.Name
		instances, err := s.store.ListResidentInstancesOnNodeID(ctx, node.ID)
		if err != nil {
			out.UnavailableNodes = append(out.UnavailableNodes, node.Name)
			continue
		}
		for _, ins := range instances {
			if f.running && ins.State != string(state.StateRunning) || f.appID != "" && ins.AppID != f.appID {
				continue
			}
			app := s.cachedApp(ctx, apps, ins.AppID)
			if f.accountID != "" && app.AccountID != f.accountID {
				continue
			}
			rows = append(rows, ins)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].LastRequestAt.After(rows[j].LastRequestAt) })
	if len(rows) > f.limit {
		rows, out.Truncated = rows[:f.limit], true
	}
	out.Items = projectObsInstances(rows, appSlugs(apps), appAccounts(apps), nodeNames)
	return out, nil
}

// cachedApp loads an app once per request; an unreadable app yields a zero
// App, so its instances still list without slug or account.
func (s *server) cachedApp(ctx context.Context, cache map[string]state.App, id string) state.App {
	if app, ok := cache[id]; ok {
		return app
	}
	app, err := s.store.AppByID(ctx, id)
	if err != nil {
		app = state.App{}
	}
	cache[id] = app
	return app
}

func (s *server) obsGetInstance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if allowed, prob := s.adminAllows(acct); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	id := r.PathValue("id")
	if _, err := uuid.Parse(id); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad instance id", "expected UUID"))
		return
	}
	ins, err := s.store.InstanceByID(r.Context(), id)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Instance not found", "no instance has this id"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not read the instance"))
		return
	}
	apps := map[string]state.App{}
	s.cachedApp(r.Context(), apps, ins.AppID)
	nodeNames := map[string]string{}
	if node, err := s.store.ComputeNodeByID(r.Context(), ins.NodeID); err == nil {
		nodeNames[node.ID] = node.Name
	}
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, projectObsInstances([]state.Instance{ins}, appSlugs(apps), appAccounts(apps), nodeNames)[0])
}
