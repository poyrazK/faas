package operatorobs

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/promql"
	"github.com/onebox-faas/faas/pkg/state"
)

var (
	ErrInvalidReference = errors.New("invalid incident reference")
	ErrUnavailable      = errors.New("incident source unavailable")
	tracePattern        = regexp.MustCompile(`^[0-9a-f]{32}$`)
	alertIDPattern      = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// Store is the evidence reader's narrow dependency. Exact-resource history is
// projected in SQL using subject, resource-reference, target, and deployment indexes.
type Store interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
	AppByID(context.Context, string) (state.App, error)
	JobRunGetByID(context.Context, string) (state.JobRun, error)
	ComputeNodeByID(context.Context, string) (state.ComputeNode, error)
	OperatorIncidentEvidence(context.Context, string, time.Time, int) ([]api.ObsIncidentEvidenceEntry, error)
}

// ValidateReference accepts only the inbox's four typed identifiers.
func ValidateReference(kind, id string) error {
	switch kind {
	case "deployment", "job_run", "compute_node":
		parsed, err := uuid.Parse(id)
		if err == nil && parsed != uuid.Nil && parsed.String() == id {
			return nil
		}
	case "platform_alert":
		if alertIDPattern.MatchString(id) {
			return nil
		}
	}
	return ErrInvalidReference
}

// Evidence resolves resources independently of the current inbox page. The
// bounded history is exact-resource evidence, not an inferred account timeline.
func Evidence(ctx context.Context, store Store, client *promql.Client, kind, id string, limit int, now time.Time) (api.ObsIncidentEvidenceResponse, error) {
	out := api.ObsIncidentEvidenceResponse{GeneratedAt: now.UTC(), Since: now.UTC().Add(-time.Duration(api.ObsIncidentWindowDefaultHours) * time.Hour),
		Limit: limit, History: []api.ObsIncidentEvidenceEntry{}, UnavailableSources: []string{}}
	if err := ValidateReference(kind, id); err != nil {
		return out, err
	}
	if limit < 1 || limit > api.ObsIncidentEvidenceLimitMax {
		return out, ErrInvalidReference
	}
	if kind == "platform_alert" {
		if retained, handled, err := retainedAlertEvidence(ctx, store, id, out); handled {
			return retained, err
		}
		live, err := alertEvidence(ctx, client, id, out)
		if _, ok := store.(state.OperatorAlertHistoryReader); ok && err == nil {
			live.Incident.Status = "unknown"
			live.UnavailableSources = append(live.UnavailableSources, "platform_alert_history")
		}
		return live, err
	}
	item, code, err := resourceIncident(ctx, store, kind, id, now)
	if err != nil {
		return out, err
	}
	Enrich(&item)
	out.Incident, out.ErrorCode = item, code
	rows, err := store.OperatorIncidentEvidence(ctx, id, out.Since, limit+1)
	if err != nil {
		return out, fmt.Errorf("load incident history: %w", err)
	}
	out.Truncated = len(rows) > limit
	if out.Truncated {
		rows = rows[:limit]
	}
	for _, row := range rows {
		if tracePattern.MatchString(row.TraceID) {
			row.TracePath = "/v1/admin/obs/traces/" + row.TraceID
		} else {
			row.TraceID = ""
		}
		out.History = append(out.History, row)
	}
	return out, nil
}

func alertEvidence(ctx context.Context, client *promql.Client, id string, out api.ObsIncidentEvidenceResponse) (api.ObsIncidentEvidenceResponse, error) {
	items, unavailable := FiringAlerts(ctx, client)
	for _, source := range unavailable {
		if source == "prometheus" {
			return out, ErrUnavailable
		}
	}
	for _, item := range items {
		if item.ID == "platform_alert/"+id {
			out.Incident = item
			out.UnavailableSources = unavailable
			return out, nil
		}
	}
	return out, state.ErrNotFound
}

func resourceIncident(ctx context.Context, store Store, kind, id string, now time.Time) (api.ObsIncident, string, error) {
	item := api.ObsIncident{ID: kind + "/" + id, Type: kind, ResourceID: id, Severity: "info"}
	switch kind {
	case "deployment":
		return deploymentIncident(ctx, store, id, item)
	case "job_run":
		run, err := store.JobRunGetByID(ctx, id)
		if err != nil {
			return item, "", err
		}
		item.AccountID, item.Status, item.ObservedAt = run.AccountID, run.AggregateStatus, run.CreatedAt.UTC()
		if run.FinishedAt != nil {
			item.ObservedAt = run.FinishedAt.UTC()
		}
		switch run.AggregateStatus {
		case "failed", "dead_letter":
			item.Severity = "error"
		case "queued", "running":
			item.Severity = "warning"
			if !run.CreatedAt.After(now.Add(-api.ObsIncidentStuckAfter)) {
				item.Severity = "error"
			}
		}
	case "compute_node":
		return nodeIncident(ctx, store, id, item, now)
	}
	item.Summary = strings.ReplaceAll(kind, "_", " ") + " " + item.Status
	return item, "", nil
}

func deploymentIncident(ctx context.Context, store Store, id string, item api.ObsIncident) (api.ObsIncident, string, error) {
	dep, err := store.DeploymentByID(ctx, id)
	if err != nil {
		return item, "", err
	}
	if dep.DeletedAt != nil {
		return item, "", state.ErrNotFound
	}
	app, err := store.AppByID(ctx, dep.AppID)
	if err != nil {
		return item, "", err
	}
	if app.Status == state.AppDeleted {
		return item, "", state.ErrNotFound
	}
	item.AppID, item.AccountID, item.ResourceName = app.ID, app.AccountID, app.Slug
	item.Status, item.ObservedAt = string(dep.Status), dep.CreatedAt.UTC()
	item.Summary = "deployment " + item.Status
	switch dep.Status {
	case state.DeployFailed:
		item.Severity = "error"
		return item, dep.ErrorCode, nil
	case state.DeployPending, state.DeployBuilding, state.DeployImaging, state.DeploySnapshotting:
		item.Severity = "warning"
	}
	return item, "", nil
}

func nodeIncident(ctx context.Context, store Store, id string, item api.ObsIncident, now time.Time) (api.ObsIncident, string, error) {
	node, err := store.ComputeNodeByID(ctx, id)
	if err != nil {
		return item, "", err
	}
	item.ResourceName, item.Status, item.ObservedAt = node.Name, string(node.Lifecycle), node.LastHeartbeatAt.UTC()
	item.NodeID = node.ID
	if item.ObservedAt.IsZero() {
		item.ObservedAt = node.CreatedAt.UTC()
	}
	if node.DrainInitiatedAt != nil {
		item.ObservedAt = node.DrainInitiatedAt.UTC()
	}
	if !node.Active {
		item.Severity = "error"
	} else if node.Lifecycle != state.NodeLifecycleActive || (!node.LastHeartbeatAt.IsZero() && node.LastHeartbeatAt.Before(now.Add(-state.DefaultHeartbeatStaleness))) {
		item.Severity = "warning"
	}
	if !node.LastHeartbeatAt.IsZero() && node.LastHeartbeatAt.Before(now.Add(-state.DefaultHeartbeatStaleness)) {
		item.Status = "stale"
	}
	item.Summary = "compute node " + item.Status
	return item, "", nil
}
