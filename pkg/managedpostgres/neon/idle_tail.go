package neon

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.IdleTailReporter = (*Provider)(nil)

// neonPlanSuspendTimeoutSeconds is what an endpoint's suspend_timeout_seconds
// of 0 means on Neon's paid plans.
const neonPlanSuspendTimeoutSeconds = 300

// IdleTail counts the window's suspends of the project's read-write computes.
// Neon suspends a compute only after its timeout passes with no activity, so
// each suspend held timeout × minimum CU idle. Settings are read now, not as
// they were in the window, and a compute deleted since is not counted; this is
// telemetry, so both are accepted.
func (p *Provider) IdleTail(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow) (managedpostgres.IdleTail, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil || window.From.IsZero() || !window.To.After(window.From) {
		return managedpostgres.IdleTail{}, managedpostgres.ErrInvalid
	}
	// Usage is a project aggregate recorded against the source database; its
	// tail is the project's too.
	if ref.branchID != "" {
		return managedpostgres.IdleTail{}, managedpostgres.ErrUnsupported
	}
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return managedpostgres.IdleTail{}, err
	}
	computes := make(map[string]endpoint, len(endpoints.Endpoints))
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type == "read_write" && candidate.ProjectID == ref.projectID {
			computes[candidate.ID] = candidate
		}
	}
	suspends, err := p.suspendsBetween(ctx, ref.projectID, window.From, window.To)
	if err != nil {
		return managedpostgres.IdleTail{}, err
	}
	var tail managedpostgres.IdleTail
	for _, endpointID := range suspends {
		compute, ok := computes[endpointID]
		timeout := compute.SuspendTimeoutSecond
		if timeout == 0 {
			timeout = neonPlanSuspendTimeoutSeconds
		}
		// An always-on compute does not suspend for idleness.
		if !ok || timeout < 0 || compute.MinimumCU < 0 {
			continue
		}
		tail.Suspends++
		tail.ComputeUnitSeconds += int64(math.Round(float64(timeout) * compute.MinimumCU))
	}
	return tail, nil
}

// suspendsBetween returns the endpoint of each finished suspend_compute
// operation created in [from, to). Neon lists operations newest first and
// does not document it, so a page out of that order is unavailable rather
// than silently miscounted.
func (p *Provider) suspendsBetween(ctx context.Context, projectID string, from, to time.Time) ([]string, error) {
	var suspends []string
	var previous time.Time
	cursor := ""
	for range maximumOperationPages {
		query := url.Values{"limit": {"1000"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response operationsResponse
		if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/operations", query, nil, &response, http.StatusOK); err != nil {
			return nil, err
		}
		if response.Operations == nil {
			return nil, managedpostgres.ErrUnavailable
		}
		for _, op := range response.Operations {
			created, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
			if err != nil || (!previous.IsZero() && created.After(previous)) {
				return nil, managedpostgres.ErrUnavailable
			}
			previous = created
			if created.Before(from) {
				return suspends, nil
			}
			if created.Before(to) && op.Action == "suspend_compute" && op.Status == "finished" {
				suspends = append(suspends, op.EndpointID)
			}
		}
		if len(response.Operations) == 0 || response.Pagination.Cursor == "" || response.Pagination.Cursor == cursor {
			return suspends, nil
		}
		cursor = response.Pagination.Cursor
	}
	return nil, managedpostgres.ErrUnavailable
}
