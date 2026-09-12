package main

// Unified operator incident inbox.
//
// GET /v1/admin/obs/incidents composes the existing bounded operator reads
// into one deterministic, read-only projection. The handler does not create a
// new incident table or write to deployment/job/node state; the source rows
// remain owned by their existing controllers.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/cursor"
	"github.com/onebox-faas/faas/pkg/state"
)

const obsIncidentStuckAfter = 5 * time.Minute

// obsIncidentNodeLister is implemented by the production stores so the
// inbox can keep the node source query bounded as well as the response. The
// fallback preserves compatibility with narrow test doubles that only expose
// the older fleet-list method.
type obsIncidentNodeLister interface {
	ListComputeNodesPage(context.Context, bool, int) ([]state.ComputeNode, error)
}

func (s *server) obsIncidents(w http.ResponseWriter, r *http.Request, caller state.Account) {
	if allowed, prob := s.adminAllows(caller); !allowed {
		api.WriteProblem(w, prob)
		return
	}
	q, prob := parseObsIncidentQuery(r)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	items, err := s.loadObsIncidents(r, q.since, q.kind, q.severity)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load operator incidents"))
		return
	}
	page, nextCursor, prob := paginateObsIncidents(items, q.cursor, q.limit)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	emitOperatorJobsView(r, s, caller, "", "obs.incidents")
	writeJSON(w, http.StatusOK, api.ObsIncidentListResponse{
		GeneratedAt:  time.Now().UTC(),
		Items:        page,
		Limit:        q.limit,
		Since:        q.since,
		SinceClamped: q.sinceClamped,
		Type:         q.kind,
		Severity:     q.severity,
		NextCursor:   nextCursor,
	})
}

type obsIncidentQuery struct {
	since        time.Time
	sinceClamped bool
	kind         string
	severity     string
	cursor       string
	limit        int
}

func parseObsIncidentQuery(r *http.Request) (obsIncidentQuery, *api.Problem) {
	now := time.Now().UTC()
	q := obsIncidentQuery{
		since:    now.Add(-time.Duration(api.ObsIncidentWindowDefaultHours) * time.Hour),
		kind:     strings.TrimSpace(r.URL.Query().Get("type")),
		severity: strings.TrimSpace(r.URL.Query().Get("severity")),
		cursor:   strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if raw := r.URL.Query().Get("since"); raw != "" {
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid since", "since must be RFC 3339 (e.g. 2026-09-12T00:00:00Z)")
		}
		floor := now.Add(-time.Duration(api.ObsAdminWindowMaxHours) * time.Hour)
		q.since = t.UTC()
		if q.since.Before(floor) {
			q.since = floor
			q.sinceClamped = true
		}
	}
	if q.kind != "" {
		switch q.kind {
		case "deployment", "job_run", "compute_node", "platform_alert":
		default:
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid type", "type must be deployment, job_run, compute_node, or platform_alert")
		}
	}
	if q.severity != "" {
		switch q.severity {
		case "warning", "error", "critical":
		default:
			return obsIncidentQuery{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid severity", "severity must be warning, error, or critical")
		}
	}
	limitProb, limit := api.ParseLimit(r.URL.Query().Get("limit"), api.ObsIncidentLimitDefault, api.ObsIncidentLimitMax, "incidents")
	if limitProb != nil {
		return obsIncidentQuery{}, limitProb
	}
	q.limit = limit
	return q, nil
}

func (s *server) loadObsIncidents(r *http.Request, since time.Time, kind, severity string) ([]api.ObsIncident, error) {
	now := time.Now().UTC()
	items := make([]api.ObsIncident, 0)
	want := func(source string) bool { return kind == "" || kind == source }
	appendItem := func(item api.ObsIncident) {
		if item.ObservedAt.IsZero() || item.ObservedAt.Before(since) {
			return
		}
		if severity != "" && item.Severity != severity {
			return
		}
		items = append(items, item)
	}

	if want("deployment") {
		rows, err := s.store.ListDeploymentsForOperator(r.Context(), state.OperatorDeploymentFilter{
			Statuses: append([]state.DeploymentStatus(nil), operatorDeploymentIncidentStatuses...),
			Limit:    api.ObsIncidentLimitMax,
		})
		if err != nil {
			return nil, err
		}
		for _, deployment := range rows {
			app, err := s.store.AppByID(r.Context(), deployment.AppID)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if app.AccountID == "" || app.Status == state.AppDeleted {
				continue
			}
			observedAt := deployment.CreatedAt.UTC()
			if observedAt.IsZero() {
				observedAt = now
			}
			sev := "warning"
			summary := "deployment " + string(deployment.Status)
			if deployment.Status == state.DeployFailed {
				sev = "error"
				summary = "deployment failed"
				if deployment.ErrorCode != "" {
					summary += " (" + deployment.ErrorCode + ")"
				}
			}
			appendItem(api.ObsIncident{
				ID:           "deployment/" + deployment.ID,
				Type:         "deployment",
				Severity:     sev,
				Status:       string(deployment.Status),
				Summary:      summary,
				AccountID:    app.AccountID,
				AppID:        app.ID,
				ResourceID:   deployment.ID,
				ResourceName: app.Slug,
				ObservedAt:   observedAt,
				DedupeKey:    "deployment:" + deployment.ID,
				ActionPath:   "/v1/admin/ops/deployments/" + deployment.ID,
				AuditPath:    "/v1/admin/obs/audit-log/search?target_account_id=" + app.AccountID,
				RunbookURL:   "/docs/runbooks/FaasDeployFailed.md",
			})
		}
	}

	if want("job_run") {
		runs, err := s.store.JobRunListActive(r.Context(), "", api.ObsIncidentLimitMax, 0)
		if err != nil {
			return nil, err
		}
		for _, run := range runs {
			job, err := s.store.JobGetByID(r.Context(), run.JobID)
			if errors.Is(err, state.ErrNotFound) {
				continue
			}
			if err != nil {
				return nil, err
			}
			if job.AccountID != run.AccountID {
				continue
			}
			observedAt := run.CreatedAt.UTC()
			if observedAt.IsZero() {
				observedAt = now
			}
			stuck := !observedAt.After(now.Add(-obsIncidentStuckAfter))
			sev := "warning"
			summary := "job run " + run.AggregateStatus
			if stuck {
				sev = "error"
				summary = "job run appears stuck"
			}
			appendItem(api.ObsIncident{
				ID:           "job_run/" + run.ID,
				Type:         "job_run",
				Severity:     sev,
				Status:       run.AggregateStatus,
				Summary:      summary,
				AccountID:    run.AccountID,
				ResourceID:   run.ID,
				ResourceName: job.Name,
				ObservedAt:   observedAt,
				DedupeKey:    "job_run:" + run.ID,
				ActionPath:   "/v1/admin/ops/jobs/runs/" + run.ID,
				AuditPath:    "/v1/admin/obs/audit-log/search?target_account_id=" + run.AccountID,
				RunbookURL:   "/docs/ops/gregalectl-operator-quickstart.md#job-run-incidents",
			})
		}
	}

	if want("compute_node") {
		var nodes []state.ComputeNode
		var err error
		if paged, ok := s.store.(obsIncidentNodeLister); ok {
			nodes, err = paged.ListComputeNodesPage(r.Context(), true, api.ObsIncidentLimitMax)
		} else {
			nodes, err = s.store.ListComputeNodes(r.Context(), true)
			if len(nodes) > api.ObsIncidentLimitMax {
				nodes = nodes[:api.ObsIncidentLimitMax]
			}
		}
		if err != nil {
			return nil, err
		}
		for _, node := range nodes {
			lifecycle := string(node.Lifecycle)
			if lifecycle == "" {
				if node.Active {
					lifecycle = string(state.NodeLifecycleActive)
				} else {
					lifecycle = string(state.NodeLifecycleUnavailable)
				}
			}
			stale := node.LastHeartbeatAt.IsZero() || now.Sub(node.LastHeartbeatAt) > state.DefaultHeartbeatStaleness
			if node.Active && lifecycle == string(state.NodeLifecycleActive) && !stale {
				continue
			}
			observedAt := node.LastHeartbeatAt.UTC()
			if !node.Active {
				observedAt = now
			} else if observedAt.IsZero() {
				observedAt = node.CreatedAt.UTC()
			}
			if observedAt.IsZero() {
				observedAt = now
			}
			sev := "warning"
			summary := "compute node " + lifecycle
			status := lifecycle
			if stale {
				status = "stale"
				summary = "compute node heartbeat stale"
			}
			if !node.Active {
				sev = "error"
			}
			appendItem(api.ObsIncident{
				ID:           "compute_node/" + node.ID,
				Type:         "compute_node",
				Severity:     sev,
				Status:       status,
				Summary:      summary,
				ResourceID:   node.ID,
				ResourceName: node.Name,
				ObservedAt:   observedAt,
				DedupeKey:    "compute_node:" + node.ID,
				ActionPath:   "/v1/admin/obs/nodes/" + node.Name + "/detail",
				AuditPath:    "/v1/admin/obs/audit-log/search?kind_prefix=operator.action.",
				RunbookURL:   "/docs/runbooks/FaasComputeNodeStuckInactive.md",
			})
		}
	}

	if want("platform_alert") {
		if count := s.alertsFiring(r.Context()); count > 0 {
			appendItem(api.ObsIncident{
				ID:           "platform_alert/prometheus",
				Type:         "platform_alert",
				Severity:     "critical",
				Status:       "firing",
				Summary:      fmt.Sprintf("%d Prometheus alerts firing", count),
				ResourceID:   "prometheus",
				ResourceName: "platform alerts",
				ObservedAt:   now,
				DedupeKey:    "platform_alerts:firing",
				ActionPath:   "/v1/admin/obs/health",
				RunbookURL:   "/docs/ops/alert-cookbook.md",
			})
		}
	}

	sort.Slice(items, func(i, j int) bool {
		if items[i].ObservedAt.Equal(items[j].ObservedAt) {
			return items[i].ID > items[j].ID
		}
		return items[i].ObservedAt.After(items[j].ObservedAt)
	})
	return items, nil
}

func paginateObsIncidents(items []api.ObsIncident, rawCursor string, limit int) ([]api.ObsIncident, string, *api.Problem) {
	start := 0
	if rawCursor != "" {
		key, err := cursor.Decode(rawCursor)
		if err != nil {
			return nil, "", api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid cursor", "cursor must be an opaque value returned as next_cursor")
		}
		for start < len(items) {
			item := items[start]
			if item.ObservedAt.Before(key.CreatedAt) || (item.ObservedAt.Equal(key.CreatedAt) && item.ID < key.ID) {
				break
			}
			start++
		}
	}
	if start >= len(items) {
		return []api.ObsIncident{}, "", nil
	}
	remaining := items[start:]
	next := ""
	if len(remaining) > limit {
		page := remaining[:limit]
		last := page[len(page)-1]
		next = cursor.Encode(cursor.Key{CreatedAt: last.ObservedAt, ID: last.ID})
		return page, next, nil
	}
	return remaining, next, nil
}
