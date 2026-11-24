package neon

import (
	"context"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

var _ managedpostgres.IdleTailReporter = (*Provider)(nil)

const (
	// neonPlanSuspendTimeoutSeconds is what an endpoint's
	// suspend_timeout_seconds of 0 means on Neon's paid plans.
	neonPlanSuspendTimeoutSeconds = 300
	// restartGap separates an idle suspend from the suspend half of a
	// restart: a config apply just before it, or a start just after it.
	restartGap         = time.Minute
	operationsPageSize = 1000
)

// IdleTail counts the window's idle suspends of the project's one read-write
// compute. Neon suspends a compute after its timeout passes with no
// activity, so each idle suspend held timeout × minimum CU. A suspend next to
// a config apply or a restart is not idle. Settings are read now, not as they
// were in the window; this is telemetry, so that is accepted. A project with
// more than one read-write compute (a restore's branch) is unsupported: its
// usage is the project's, and the restore's class and timeout differ.
func (p *Provider) IdleTail(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow) (managedpostgres.IdleTail, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil || window.From.IsZero() || !window.To.After(window.From) {
		return managedpostgres.IdleTail{}, managedpostgres.ErrInvalid
	}
	if ref.branchID != "" {
		return managedpostgres.IdleTail{}, managedpostgres.ErrUnsupported
	}
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return managedpostgres.IdleTail{}, err
	}
	var compute endpoint
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type != "read_write" {
			continue
		}
		if compute.ID != "" || candidate.ProjectID != ref.projectID || candidate.ID == "" {
			return managedpostgres.IdleTail{}, managedpostgres.ErrUnsupported
		}
		compute = candidate
	}
	timeout := compute.SuspendTimeoutSecond
	if timeout == 0 {
		timeout = neonPlanSuspendTimeoutSeconds
	}
	// No compute, or an always-on one, has no idle tail.
	if compute.ID == "" || timeout < 0 || compute.MinimumCU <= 0 {
		return managedpostgres.IdleTail{}, nil
	}
	ops, err := p.operationsSince(ctx, ref.projectID, window.From.Add(-restartGap))
	if err != nil {
		return managedpostgres.IdleTail{}, err
	}
	var tail managedpostgres.IdleTail
	for i, op := range ops {
		if op.endpointID != compute.ID || op.action != "suspend_compute" || op.status != "finished" ||
			op.at.Before(window.From) || !op.at.Before(window.To) || restarted(ops, i) {
			continue
		}
		tail.Suspends++
	}
	tail.ComputeUnitSeconds = int64(math.Round(float64(tail.Suspends*timeout) * compute.MinimumCU))
	return tail, nil
}

// restarted reports whether the suspend at ops[i] is half of a restart. ops
// is newest first.
func restarted(ops []timedOperation, i int) bool {
	suspend := ops[i]
	for _, op := range ops[:i] {
		if op.endpointID == suspend.endpointID && op.action == "start_compute" && op.at.Sub(suspend.at) <= restartGap {
			return true
		}
	}
	for _, op := range ops[i+1:] {
		if suspend.at.Sub(op.at) > restartGap {
			break
		}
		if op.endpointID == suspend.endpointID && op.action == "apply_config" {
			return true
		}
	}
	return false
}

type timedOperation struct {
	at                         time.Time
	endpointID, action, status string
}

// operationsSince returns the project's operations created at or after since,
// newest first. Neon lists operations newest first without documenting it,
// so a list out of that order is unavailable rather than silently miscounted.
// Neon returns a cursor on the last page too.
func (p *Provider) operationsSince(ctx context.Context, projectID string, since time.Time) ([]timedOperation, error) {
	pageSize := operationsPageSize
	if p.idleTailPageSize > 0 {
		pageSize = p.idleTailPageSize
	}
	var ops []timedOperation
	seenCursors := map[string]bool{}
	cursor := ""
	for range maximumOperationPages {
		query := url.Values{"limit": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response operationsResponse
		if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/operations", query, nil, &response, http.StatusOK); err != nil {
			return nil, err
		}
		if response.Operations == nil && cursor == "" {
			return nil, managedpostgres.ErrUnavailable
		}
		for _, op := range response.Operations {
			created, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
			if err != nil || (len(ops) > 0 && created.After(ops[len(ops)-1].at)) {
				return nil, managedpostgres.ErrUnavailable
			}
			if created.Before(since) {
				return ops, nil
			}
			ops = append(ops, timedOperation{at: created, endpointID: op.EndpointID, action: op.Action, status: op.Status})
		}
		// A short page is the last one, whatever its cursor says.
		if len(response.Operations) < pageSize || response.Pagination.Cursor == "" {
			return ops, nil
		}
		cursor = response.Pagination.Cursor
		if seenCursors[cursor] {
			return nil, managedpostgres.ErrUnavailable
		}
		seenCursors[cursor] = true
	}
	return nil, managedpostgres.ErrUnavailable
}
