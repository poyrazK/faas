// apid side of route resource peaks (ADR-20261121170000): vmmd sends each
// app instance's per-minute cgroup usage on SpansWriter, gateways send each
// instance's per-minute route activity on RequestTelemetry. Both are kept
// only for apps whose account the route_resource_peaks flag releases.

package receivers

import (
	"context"
	"errors"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	apidpb "github.com/onebox-faas/faas/api/proto/onebox/faas/apid/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/routeresources"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// RouteResources stores both halves of route resource peaks. It is a
// RequestTelemetryOption, and GuestSpansIngester.WithRouteResources takes
// it for the vmmd half.
type RouteResources struct {
	store    state.RouteResourceStore
	released func(platformflags.Subject) bool
	now      func() time.Time
}

// NewRouteResources keeps minutes for the subjects released approves.
func NewRouteResources(store state.RouteResourceStore, released func(platformflags.Subject) bool) *RouteResources {
	if store == nil || released == nil {
		return nil
	}
	return &RouteResources{store: store, released: released, now: time.Now}
}

func (rr *RouteResources) applyRequestTelemetry(r *requestTelemetryReceiver) { r.routeResources = rr }

// releasedFor decides the flag by account and plan only. The gateway, which
// counts the route half, knows no organization: an org-aware decision here
// could keep one half and drop the other (code review 2026-11-22).
func (rr *RouteResources) releasedFor(subject platformflags.Subject) bool {
	subject.OrgID = ""
	return rr.released(subject)
}

// routeResourceMinuteOK accepts minutes a little in the future (clock
// skew) and up to the retention back.
func (rr *RouteResources) minuteOK(unix int64) (time.Time, bool) {
	if unix%60 != 0 {
		return time.Time{}, false
	}
	at := time.Unix(unix, 0).UTC()
	now := rr.now()
	return at, !at.After(now.Add(5*time.Minute)) && at.After(now.Add(-api.RouteResourceRetention))
}

// AddInstanceRouteMinutes stores one gateway batch of route minutes.
func (r *requestTelemetryReceiver) AddInstanceRouteMinutes(ctx context.Context, req *apidpb.AddInstanceRouteMinutesRequest) (*apidpb.AddInstanceRouteMinutesResponse, error) {
	rr := r.routeResources
	if rr == nil {
		return nil, status.Error(codes.Unimplemented, "this apid keeps no route resource minutes")
	}
	if len(req.GetRows()) > api.RouteResourceBatchRowsMax {
		return nil, status.Errorf(codes.InvalidArgument, "a batch carries at most %d rows, got %d", api.RouteResourceBatchRowsMax, len(req.GetRows()))
	}
	out := &apidpb.AddInstanceRouteMinutesResponse{}
	type key struct {
		app             uuid.UUID
		instance, route string
		method          string
		minute          int64
	}
	merged := map[key]*state.InstanceRouteMinuteRow{}
	var order []key
	for _, p := range req.GetRows() {
		row, ok := rr.routeRowFromProto(p)
		if !ok {
			out.Invalid++
			continue
		}
		plan, err := r.plans.forApp(ctx, row.AccountID, row.AppID)
		if err != nil {
			// A failed lookup is not "flag off": the gateway drops this
			// batch and keeps counting.
			return nil, status.Errorf(codes.Unavailable, "route minutes: plan lookup: %v", err)
		}
		if !rr.releasedFor(plan.Flag) {
			out.Disabled++
			continue
		}
		k := key{row.AppID, row.InstanceID, row.Route, row.Method, row.Minute.Unix()}
		if cur := merged[k]; cur != nil {
			cur.Requests += row.Requests
			cur.BusyMS += row.BusyMS
			cur.MaxLatencyMS = max(cur.MaxLatencyMS, row.MaxLatencyMS)
			continue
		}
		merged[k] = &row
		order = append(order, k)
	}
	rows := make([]state.InstanceRouteMinuteRow, 0, len(order))
	for _, k := range order {
		rows = append(rows, *merged[k])
	}
	if len(rows) == 0 {
		return out, nil
	}
	release, err := r.gate.acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer release()
	if err := rr.store.AddInstanceRouteMinutes(ctx, rows); err != nil {
		return nil, status.Errorf(codes.Unavailable, "store route minutes: %v", err)
	}
	out.Accepted = int64(len(rows))
	return out, nil
}

func (rr *RouteResources) routeRowFromProto(p *apidpb.InstanceRouteMinute) (state.InstanceRouteMinuteRow, bool) {
	account, aerr := uuid.Parse(p.GetAccountId())
	app, perr := uuid.Parse(p.GetAppId())
	minute, mok := rr.minuteOK(p.GetMinuteUnix())
	route, method, instance := p.GetRoute(), p.GetMethod(), p.GetInstanceId()
	if aerr != nil || perr != nil || !mok || account == uuid.Nil || app == uuid.Nil ||
		len(instance) == 0 || len(instance) > 128 || !utf8.ValidString(instance) ||
		len(route) == 0 || len(route) > 256 || !utf8.ValidString(route) || len(method) > 16 ||
		p.GetRequests() < 0 || p.GetBusyMs() < 0 || p.GetMaxLatencyMs() < 0 {
		return state.InstanceRouteMinuteRow{}, false
	}
	return state.InstanceRouteMinuteRow{AccountID: account, RouteMinute: routeresources.RouteMinute{
		AppID: app, InstanceID: instance, Minute: minute, Route: route, Method: method,
		Requests: p.GetRequests(), BusyMS: p.GetBusyMs(), MaxLatencyMS: int(p.GetMaxLatencyMs()),
	}}, true
}

// WithRouteResources makes the ingester take vmmd's resource minutes.
func (g *GuestSpansIngester) WithRouteResources(rr *RouteResources) *GuestSpansIngester {
	if g != nil {
		g.routeResources = rr
	}
	return g
}

// IngestInstanceResourceMinutes implements the SpansWriter RPC.
func (r *spansWriterReceiver) IngestInstanceResourceMinutes(ctx context.Context, req *apidpb.IngestInstanceResourceMinutesRequest) (*apidpb.IngestInstanceResourceMinutesResponse, error) {
	if !r.enabled || r.guest == nil || r.guest.routeResources == nil {
		return &apidpb.IngestInstanceResourceMinutesResponse{Outcome: guestSpansUnavailable}, nil
	}
	return r.guest.ingestResourceMinutes(ctx, req)
}

func (g *GuestSpansIngester) ingestResourceMinutes(ctx context.Context, req *apidpb.IngestInstanceResourceMinutesRequest) (*apidpb.IngestInstanceResourceMinutesResponse, error) {
	rr := g.routeResources
	if len(req.GetMinutes()) > api.RouteResourceBatchRowsMax {
		return nil, status.Errorf(codes.InvalidArgument, "a batch carries at most %d rows, got %d", api.RouteResourceBatchRowsMax, len(req.GetMinutes()))
	}
	node, err := g.peerNode(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "resource minutes: %v", err)
	}
	out := &apidpb.IngestInstanceResourceMinutesResponse{Outcome: guestSpansAccepted}
	disabled := map[string]bool{}
	type key struct {
		app      uuid.UUID
		instance string
		minute   int64
	}
	seen := map[key]bool{}
	var rows []state.InstanceResourceMinuteRow
	for _, p := range req.GetMinutes() {
		row, ok := rr.resourceRowFromProto(p)
		if !ok {
			out.Invalid++
			continue
		}
		if disabled[p.GetAppId()] {
			continue
		}
		plan, err := g.plans.forApp(ctx, row.AccountID, row.AppID)
		if err != nil {
			// A failed lookup is not "flag off": answer unavailable so vmmd
			// drops this batch without backing the app off.
			g.log.Warn("resource minutes: plan lookup failed", "app_id", p.GetAppId(), "err", err)
			return &apidpb.IngestInstanceResourceMinutesResponse{Outcome: guestSpansUnavailable, DisabledAppIds: out.DisabledAppIds}, nil
		}
		if !rr.releasedFor(plan.Flag) {
			disabled[p.GetAppId()] = true
			out.DisabledAppIds = append(out.DisabledAppIds, p.GetAppId())
			continue
		}
		if node != "" {
			own, err := g.binding.owns(ctx, guestBindingKey{node: node, account: p.GetAccountId(), app: p.GetAppId(),
				deployment: p.GetDeploymentId(), instance: p.GetInstanceId()})
			if err != nil {
				//nolint:nilerr // the outcome tells vmmd the batch was not taken; it drops it without backing the app off
				return &apidpb.IngestInstanceResourceMinutesResponse{Outcome: guestSpansUnavailable}, nil
			}
			if !own {
				out.Invalid++
				continue
			}
		}
		// vmmd sends an instance-minute once per batch; a repeat would make
		// the upsert touch a row twice, so it is refused.
		k := key{row.AppID, row.InstanceID, row.Minute.Unix()}
		if seen[k] {
			out.Invalid++
			continue
		}
		seen[k] = true
		rows = append(rows, row)
	}
	if len(rows) > 0 {
		if err := rr.store.AddInstanceResourceMinutes(ctx, rows); err != nil {
			g.log.Warn("resource minutes: store failed", "err", err)
			return &apidpb.IngestInstanceResourceMinutesResponse{Outcome: guestSpansUnavailable, DisabledAppIds: out.DisabledAppIds}, nil
		}
	}
	out.Accepted = int64(len(rows))
	return out, nil
}

// peerNode is the compute node a mutual-TLS caller's certificate names, or
// "" when the ingester binds no node or the caller is on a local socket.
func (g *GuestSpansIngester) peerNode(ctx context.Context) (string, error) {
	if g.binding == nil {
		return "", nil
	}
	cn, err := wire.PeerCN(ctx)
	if errors.Is(err, wire.ErrPeerCNUnavailable) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return g.binding.nodes.NodeIDByCN(cn)
}

func (rr *RouteResources) resourceRowFromProto(p *apidpb.InstanceResourceMinute) (state.InstanceResourceMinuteRow, bool) {
	account, aerr := uuid.Parse(p.GetAccountId())
	app, perr := uuid.Parse(p.GetAppId())
	deployment, derr := uuid.Parse(p.GetDeploymentId())
	minute, mok := rr.minuteOK(p.GetMinuteUnix())
	instance := p.GetInstanceId()
	if aerr != nil || perr != nil || derr != nil || !mok || account == uuid.Nil || app == uuid.Nil ||
		len(instance) == 0 || len(instance) > 128 || !utf8.ValidString(instance) ||
		p.GetSamples() < 1 || p.GetMemStartMb() < 0 || p.GetMemPeakMb() < p.GetMemStartMb() ||
		p.GetMemEndMb() < 0 || p.GetMemEndMb() > p.GetMemPeakMb() || p.GetMemLimitMb() < 0 ||
		p.GetCpuMs() < 0 || p.GetCpuPeakMillicores() < 0 || p.GetCpuLimitMillicores() < 0 {
		return state.InstanceResourceMinuteRow{}, false
	}
	return state.InstanceResourceMinuteRow{AccountID: account, DeploymentID: deployment, InstanceMinute: routeresources.InstanceMinute{
		AppID: app, InstanceID: instance, Minute: minute, Samples: int(p.GetSamples()),
		MemStartMB: int(p.GetMemStartMb()), MemPeakMB: int(p.GetMemPeakMb()), MemEndMB: int(p.GetMemEndMb()),
		MemLimitMB: int(p.GetMemLimitMb()), CPUMS: p.GetCpuMs(),
		CPUPeakMillicores: int(p.GetCpuPeakMillicores()), CPULimitMillicores: int(p.GetCpuLimitMillicores()),
	}}, true
}
