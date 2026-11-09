package agentconnections

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/httpkit"
	"github.com/onebox-faas/faas/pkg/authz"
	"github.com/onebox-faas/faas/pkg/state"
)

// ownConnection returns the account's agent key with this id: one of its
// own keys, or an app agent key of one of its apps.
func ownConnection(ctx context.Context, store state.Store, accountID, id string) (state.APIKey, error) {
	k, err := store.GetAPIKey(ctx, accountID, id)
	if errors.Is(err, state.ErrNotFound) {
		keys, listErr := appAgentKeys(ctx, store, accountID)
		if listErr != nil {
			return state.APIKey{}, listErr
		}
		for _, key := range keys {
			if key.ID == id {
				return key, nil
			}
		}
	}
	if err == nil && Kind(k) == "" {
		err = state.ErrNotFound
	}
	return k, err
}

// Activity is GET /v1/agent-connections/{id}/activity: the write requests of
// one of the caller's agents (ADR-20261107220000 §3). An agent key reads only
// its own.
func Activity(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, acct state.Account) {
		id := r.PathValue("id")
		if key, isAgent := agentKey(r); isAgent && key.ID != id {
			connectionNotFound(w)
			return
		}
		k, err := ownConnection(r.Context(), d.Store, acct.ID, id)
		if errors.Is(err, state.ErrNotFound) {
			connectionNotFound(w)
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load the agent connection"))
			return
		}
		writeActivity(w, r, d.Store, k.ID)
	}
}

// SelfActivity is GET /v1/agent-connections/self/activity: an agent reads its
// own write requests.
func SelfActivity(d Deps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		key, isAgent := agentKey(r)
		if !isAgent {
			api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Agent key required", "only an agent's own key can read its activity here; people use /v1/agent-connections/{id}/activity"))
			return
		}
		writeActivity(w, r, d.Store, key.ID)
	}
}

// OrgActivity is GET /v1/orgs/{slug}/agent-connections/{id}/activity: an
// owner or admin reads an agent acting in the organization.
func OrgActivity(d OrgDeps) httpkit.AccountHandler {
	return func(w http.ResponseWriter, r *http.Request, _ state.Account) {
		orgID, ok := d.activeOrg(w, r, authz.OrgActionRevokeApiKey)
		if !ok {
			return
		}
		k, err := d.Store.GetOrgAPIKey(r.Context(), orgID, r.PathValue("id"))
		if errors.Is(err, state.ErrNotFound) || (err == nil && Kind(k) == "") {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Agent connection not found", "no connected agent with this id acts in this organization"))
			return
		}
		if err != nil {
			api.WriteProblem(w, api.ErrCapacity("could not load the agent connection"))
			return
		}
		writeActivity(w, r, d.Store, k.ID)
	}
}

func connectionNotFound(w http.ResponseWriter) {
	api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Agent connection not found", "no connected agent with this id belongs to this account"))
}

// writeActivity answers one page of the key's activity, newest first.
func writeActivity(w http.ResponseWriter, r *http.Request, store state.Store, keyID string) {
	activity, ok := store.(state.AgentActivityStore)
	if !ok {
		httpkit.WriteJSON(w, http.StatusOK, api.AgentActivityResponse{Activity: []api.AgentActivityEntry{}})
		return
	}
	limit, cursor, p := activityPage(r)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	since := time.Now().Add(-api.AgentActivityRetention)
	rows, err := activity.ListAgentActivity(r.Context(), keyID, since, cursor, limit+1)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load the agent's activity"))
		return
	}
	out := api.AgentActivityResponse{Activity: make([]api.AgentActivityEntry, 0, min(len(rows), limit))}
	for i, row := range rows {
		if i == limit {
			last := rows[limit-1]
			out.NextBefore = last.CreatedAt.UTC().Format(time.RFC3339Nano) + "_" + last.ID
			break
		}
		out.Activity = append(out.Activity, api.AgentActivityEntry{
			ID: row.ID, Method: row.Method, Path: row.Path, Status: row.Status,
			ApprovalID: row.ApprovalID, CreatedAt: row.CreatedAt.UTC().Format(time.RFC3339),
		})
	}
	httpkit.WriteJSON(w, http.StatusOK, out)
}

// activityPage reads ?limit and the ?before cursor.
func activityPage(r *http.Request) (int, state.AgentActivityCursor, *api.Problem) {
	limit := api.DefaultAgentActivityPage
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > api.MaxAgentActivityPage {
			return 0, state.AgentActivityCursor{}, api.NewProblem(http.StatusBadRequest, api.CodeBadRequest, "Invalid limit",
				"limit must be between 1 and "+strconv.Itoa(api.MaxAgentActivityPage)).WithLimit(int64(api.MaxAgentActivityPage), int64(n))
		}
		limit = n
	}
	var cursor state.AgentActivityCursor
	if raw := r.URL.Query().Get("before"); raw != "" {
		at, id, ok := strings.Cut(raw, "_")
		t, err := time.Parse(time.RFC3339Nano, at)
		if !ok || err != nil {
			return 0, cursor, api.NewProblem(http.StatusBadRequest, api.CodeBadRequest, "Invalid cursor", "before must be a next_before value from an earlier page")
		}
		cursor = state.AgentActivityCursor{At: t, ID: id}
	}
	return limit, cursor, nil
}

// activitySweepBatch bounds one delete statement, so a backlog never holds
// a long lock.
const activitySweepBatch = 5000

// SweepActivity deletes activity rows older than api.AgentActivityRetention
// (ADR-20261107220000 §2) and reports how many it removed.
func SweepActivity(ctx context.Context, store state.Store, now time.Time) (int64, error) {
	activity, ok := store.(state.AgentActivityStore)
	if !ok {
		return 0, nil
	}
	cutoff := now.Add(-api.AgentActivityRetention)
	var total int64
	for {
		n, err := activity.DeleteAgentActivityBefore(ctx, cutoff, activitySweepBatch)
		total += n
		if err != nil || n < activitySweepBatch {
			return total, err
		}
	}
}

// RunActivitySweep runs SweepActivity every interval until ctx ends.
func RunActivitySweep(ctx context.Context, store state.Store, log *slog.Logger, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		bounded, cancel := context.WithTimeout(ctx, time.Minute)
		if _, err := SweepActivity(bounded, store, time.Now()); err != nil && ctx.Err() == nil && log != nil {
			log.Warn("agent activity sweep", "err", err)
		}
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
