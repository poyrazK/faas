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
// activity, so each idle suspend held timeout × minimum CU; the tail is the
// part of those idle stretches inside the window. It prices the window with
// the compute's current settings, so a window with a config apply on the
// compute since one timeout before it began is unsupported, as is a held
// (disabled) compute and a project with a second read-write compute (a
// restore's branch: its usage is the project's, and its class and timeout
// differ). A settings change Neon applies to a suspended compute without an
// apply_config operation is not seen, and that window is priced with the new
// settings. It sends at most maxRequests requests and reports how many it
// sent, also on error.
func (p *Provider) IdleTail(ctx context.Context, providerResourceID string, window managedpostgres.UsageWindow, maxRequests int) (managedpostgres.IdleTail, []managedpostgres.ComputeEvent, error) {
	ref, err := parseResourceRef(providerResourceID)
	if err != nil || window.From.IsZero() || !window.To.After(window.From) || maxRequests < 2 {
		return managedpostgres.IdleTail{}, nil, managedpostgres.ErrInvalid
	}
	if ref.branchID != "" {
		return managedpostgres.IdleTail{}, nil, managedpostgres.ErrUnsupported
	}
	tail := managedpostgres.IdleTail{Requests: 1}
	var endpoints endpointsResponse
	if err := p.doJSON(ctx, http.MethodGet, "/projects/"+url.PathEscape(ref.projectID)+"/endpoints", nil, nil, &endpoints, http.StatusOK); err != nil {
		return tail, nil, err
	}
	var compute endpoint
	for _, candidate := range endpoints.Endpoints {
		if candidate.Type != "read_write" {
			continue
		}
		if compute.ID != "" || candidate.ProjectID != ref.projectID || candidate.ID == "" {
			return tail, nil, managedpostgres.ErrUnsupported
		}
		compute = candidate
	}
	timeout := compute.SuspendTimeoutSecond
	if timeout == 0 {
		timeout = neonPlanSuspendTimeoutSeconds
	}
	// No compute, or an always-on one, has no idle tail. A held compute was
	// suspended by the platform, not for idleness.
	if compute.ID == "" || timeout < 0 || compute.MinimumCU <= 0 {
		return tail, nil, nil
	}
	if endpointDisabled(compute) {
		return tail, nil, managedpostgres.ErrUnsupported
	}
	idle := time.Duration(timeout) * time.Second
	// An idle suspend's idle stretch can start before the window, and one
	// that ends after it can start inside it: read back one timeout.
	ops, pages, err := p.operationsSince(ctx, ref.projectID, window.From.Add(-idle), maxRequests-1)
	tail.Requests += pages
	if err != nil {
		return tail, nil, err
	}
	events := computeEvents(ops, compute.ID, window)
	var idleSeconds float64
	for i, op := range ops {
		if op.endpointID != compute.ID {
			continue
		}
		if op.action == "apply_config" {
			return tail, events, managedpostgres.ErrUnsupported
		}
		if op.action != "suspend_compute" || op.status != "finished" || !idleSuspend(ops, i, idle) {
			continue
		}
		if !op.at.Before(window.From) && op.at.Before(window.To) {
			tail.Suspends++
		}
		// Count only the part of [suspend - timeout, suspend] inside the
		// window, so the tail and the window's compute cover the same time.
		start, end := op.at.Add(-idle), op.at
		if start.Before(window.From) {
			start = window.From
		}
		if end.After(window.To) {
			end = window.To
		}
		if end.After(start) {
			idleSeconds += end.Sub(start).Seconds()
		}
	}
	tail.ComputeUnitSeconds = int64(math.Round(idleSeconds * compute.MinimumCU))
	return tail, events, nil
}

// computeEvents are the compute's finished starts and suspends inside the
// window (ADR-20261125231222).
func computeEvents(ops []timedOperation, endpointID string, window managedpostgres.UsageWindow) []managedpostgres.ComputeEvent {
	var events []managedpostgres.ComputeEvent
	for _, op := range ops {
		if op.endpointID != endpointID || op.status != "finished" || op.id == "" ||
			op.at.Before(window.From) || !op.at.Before(window.To) {
			continue
		}
		switch op.action {
		case "start_compute":
			events = append(events, managedpostgres.ComputeEvent{OperationID: op.id, Kind: managedpostgres.ComputeEventStart, At: op.at})
		case "suspend_compute":
			events = append(events, managedpostgres.ComputeEvent{OperationID: op.id, Kind: managedpostgres.ComputeEventSuspend, At: op.at})
		}
	}
	return events
}

// idleSuspend reports whether the suspend at ops[i] ended an idle stretch:
// the compute had run for at least its timeout since it last started, and it
// did not start again within restartGap (a restart). A suspend the platform
// forces soon after a wake (a storage size probe, a hold) is not idle. ops is
// newest first and reaches back one timeout before the window.
func idleSuspend(ops []timedOperation, i int, idle time.Duration) bool {
	suspend := ops[i]
	for j := i - 1; j >= 0 && ops[j].at.Sub(suspend.at) <= restartGap; j-- {
		if ops[j].endpointID == suspend.endpointID && ops[j].action == "start_compute" {
			return false
		}
	}
	for _, op := range ops[i+1:] {
		if suspend.at.Sub(op.at) >= idle {
			break
		}
		if op.endpointID == suspend.endpointID && op.action == "start_compute" {
			return false
		}
	}
	return true
}

type timedOperation struct {
	at                             time.Time
	id, endpointID, action, status string
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
			ops = append(ops, timedOperation{at: created, id: op.ID, endpointID: op.EndpointID, action: op.Action, status: op.Status})
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
