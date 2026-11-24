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
	// restartGap separates the suspend half of a restart, whose start
	// follows within seconds, from an idle suspend a client wakes again.
	restartGap         = 5 * time.Second
	operationsPageSize = 1000
)

// IdleTail counts the window's idle suspends of the project's one read-write
// compute. Neon suspends a compute after its timeout passes with no
// activity, so each idle suspend held timeout × minimum CU. It prices the
// window with the compute's current settings, so a window with a config
// apply on the compute from its start until now is unsupported, as is a
// project with a second read-write compute (a restore's branch: its usage is
// the project's, and its class and timeout differ). It sends at most
// maxRequests requests and reports how many it sent, also on error.
func (p *Provider) IdleTail(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow, maxRequests int) (managedpostgres.IdleTail, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil || window.From.IsZero() || !window.To.After(window.From) || maxRequests < 2 {
		return managedpostgres.IdleTail{}, managedpostgres.ErrInvalid
	}
	if ref.branchID != "" {
		return managedpostgres.IdleTail{}, managedpostgres.ErrUnsupported
	}
	tail := managedpostgres.IdleTail{Requests: 1}
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return tail, err
	}
	var compute endpoint
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type != "read_write" {
			continue
		}
		if compute.ID != "" || candidate.ProjectID != ref.projectID || candidate.ID == "" {
			return tail, managedpostgres.ErrUnsupported
		}
		compute = candidate
	}
	timeout := compute.SuspendTimeoutSecond
	if timeout == 0 {
		timeout = neonPlanSuspendTimeoutSeconds
	}
	// No compute, or an always-on one, has no idle tail.
	if compute.ID == "" || timeout < 0 || compute.MinimumCU <= 0 {
		return tail, nil
	}
	ops, pages, err := p.operationsSince(ctx, ref.projectID, window.From, maxRequests-1)
	tail.Requests += pages
	if err != nil {
		return tail, err
	}
	for i, op := range ops {
		if op.endpointID != compute.ID {
			continue
		}
		if op.action == "apply_config" {
			return tail, managedpostgres.ErrUnsupported
		}
		if op.action == "suspend_compute" && op.status == "finished" && op.at.Before(window.To) && !restarted(ops, i) {
			tail.Suspends++
		}
	}
	tail.ComputeUnitSeconds = int64(math.Round(float64(tail.Suspends*timeout) * compute.MinimumCU))
	return tail, nil
}

// restarted reports whether the suspend at ops[i] is half of a restart: the
// compute starts again within restartGap. ops is newest first.
func restarted(ops []timedOperation, i int) bool {
	suspend := ops[i]
	for j := i - 1; j >= 0 && ops[j].at.Sub(suspend.at) <= restartGap; j-- {
		if ops[j].endpointID == suspend.endpointID && ops[j].action == "start_compute" {
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
// newest first, reading at most maxPages pages; a list longer than that is
// unsupported. Neon lists operations newest first without documenting it,
// so a list out of that order is unavailable rather than silently miscounted.
// Neon returns a cursor on the last page too.
func (p *Provider) operationsSince(ctx context.Context, projectID string, since time.Time, maxPages int) ([]timedOperation, int, error) {
	pageSize := operationsPageSize
	if p.idleTailPageSize > 0 {
		pageSize = p.idleTailPageSize
	}
	var ops []timedOperation
	seenCursors := map[string]bool{}
	cursor := ""
	for page := 1; page <= min(maxPages, maximumOperationPages); page++ {
		query := url.Values{"limit": {strconv.Itoa(pageSize)}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		var response operationsResponse
		if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(projectID)+"/operations", query, nil, &response, http.StatusOK); err != nil {
			return nil, page, err
		}
		if response.Operations == nil && cursor == "" {
			return nil, page, managedpostgres.ErrUnavailable
		}
		for _, op := range response.Operations {
			created, err := time.Parse(time.RFC3339Nano, op.CreatedAt)
			if err != nil || (len(ops) > 0 && created.After(ops[len(ops)-1].at)) {
				return nil, page, managedpostgres.ErrUnavailable
			}
			if created.Before(since) {
				return ops, page, nil
			}
			ops = append(ops, timedOperation{at: created, endpointID: op.EndpointID, action: op.Action, status: op.Status})
		}
		// A short page is the last one, whatever its cursor says.
		if len(response.Operations) < pageSize || response.Pagination.Cursor == "" {
			return ops, page, nil
		}
		cursor = response.Pagination.Cursor
		if seenCursors[cursor] {
			return nil, page, managedpostgres.ErrUnavailable
		}
		seenCursors[cursor] = true
	}
	return nil, min(maxPages, maximumOperationPages), managedpostgres.ErrUnsupported
}
