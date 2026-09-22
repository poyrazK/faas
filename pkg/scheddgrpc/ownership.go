package scheddgrpc

import (
	"context"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// OwnerNodeID is the Phase 2 / Gate A durable shard key this schedd
// owns. The cmd/schedd wiring resolves it at startup from
// cfg.NodeName → compute_nodes.id (or "" for the single-box
// posture where the legacy default-local row carries every app).
//
// Empty OwnerNodeID is the legacy single-box schedd: every app
// is owned by this schedd (the synthetic default-local row seeded
// by migration 00024). The ownership guard short-circuits to "in-
// process" on empty — no store read needed. This preserves
// bit-for-bit behaviour for a single-box install where
// FAAS_NODE_NAME is unset.
//
// Non-empty OwnerNodeID is the multi-box schedd: every gRPC
// handler routes through authorizeApp / authorizeInstance, which
// load the row, compare node_id, and return codes.FailedPrecondition
// on a mismatch. The client (gateway's per-node schedd cache)
// must own-route by apps.node_id before dialling; a stale or
// out-of-band direct dial hits the FailedPrecondition and the
// gateway surfaces 503 to the customer.
type OwnerNodeID string

// AppResolver is the slice of state.Store scheddgrpc needs to
// resolve the app or instance row that an inbound gRPC request
// names. The two helpers (AppByID, InstanceByID) are the
// canonical single-row lookups; the ownership guard reads them
// at the start of every handler that mutates or admits state.
type AppResolver interface {
	AppByID(ctx context.Context, id string) (state.App, error)
	InstanceByID(ctx context.Context, id string) (state.Instance, error)
}

// authorizeApp loads the app row and verifies apps.node_id ==
// s.OwnerNodeID. On mismatch returns a codes.FailedPrecondition
// error with the actual owner node name so a future operator
// inspecting gateway logs can trace the routing mistake without
// re-running the placement chooser.
//
// Empty OwnerNodeID → short-circuits to in-process; the caller
// (legacy single-box schedd) is implicitly the owner of every app
// on the box. Phase 2 / Gate A: this is the only path that
// tolerates the empty string. Any non-empty value triggers the
// resolver read.
func authorizeApp(ctx context.Context, owner OwnerNodeID, resolver AppResolver, appID string) (state.App, error) {
	if owner == "" {
		// Legacy single-box posture: no per-schedd shard. The
		// resolver is never consulted; the handler proceeds.
		return state.App{}, nil
	}
	app, err := resolver.AppByID(ctx, appID)
	if err != nil {
		// Map store-side ErrNotFound to gRPC NotFound; everything
		// else is Internal (the resolver already wraps DB errors
		// with %w + op context, so the operator gets a useful
		// message).
		return state.App{}, status.Errorf(codes.NotFound, "app %s: %v", appID, err)
	}
	if app.NodeID != string(owner) {
		return state.App{}, status.Errorf(codes.FailedPrecondition,
			"app %s is owned by node id=%s (this schedd owns %s)",
			appID, app.NodeID, string(owner))
	}
	return app, nil
}

// authorizeInstance loads the instance row + its parent app and
// verifies the app's node_id == s.OwnerNodeID. Mismatch returns
// codes.FailedPrecondition. NotFound on a missing instance returns
// codes.NotFound (gateway treats it as a drop, not a 503).
//
// Empty OwnerNodeID → short-circuits the same way as authorizeApp.
func authorizeInstance(ctx context.Context, owner OwnerNodeID, resolver AppResolver, instanceID string) (state.Instance, error) {
	if owner == "" {
		return state.Instance{}, nil
	}
	ins, err := resolver.InstanceByID(ctx, instanceID)
	if err != nil {
		return state.Instance{}, status.Errorf(codes.NotFound, "instance %s: %v", instanceID, err)
	}
	app, err := resolver.AppByID(ctx, ins.AppID)
	if err != nil {
		return state.Instance{}, status.Errorf(codes.NotFound, "instance %s parent app %s: %v", instanceID, ins.AppID, err)
	}
	if app.NodeID != string(owner) {
		return state.Instance{}, status.Errorf(codes.FailedPrecondition,
			"instance %s (app %s) is owned by node id=%s (this schedd owns %s)",
			instanceID, ins.AppID, app.NodeID, string(owner))
	}
	return ins, nil
}

// String is a defensive accessor so log lines render the empty
// case as "<legacy single-box>" rather than as a blank, which is
// ambiguous when grepping for owner-related log entries.
func (o OwnerNodeID) String() string {
	if o == "" {
		return "<legacy single-box>"
	}
	return string(o)
}

// relayForeignFailure decides whether an ownership rejection of a vmmd
// failure report can be relayed to the owning schedd (issue #3359). It relays
// only when the rejection is FailedPrecondition, a relay is configured, and
// this schedd's node hosts the instance. A report about an instance on some
// other node is still refused, so the relay cannot be used to destroy
// arbitrary instances. Returns (true, nil) when relayed; otherwise false and
// the error the handler should return.
func (s *Server) relayForeignFailure(ctx context.Context, authErr error, r sched.InstanceFailureReport) (bool, error) {
	if s.failureRelay == nil || s.resolver == nil || status.Code(authErr) != codes.FailedPrecondition {
		return false, authErr
	}
	ins, err := s.resolver.InstanceByID(ctx, r.InstanceID)
	if err != nil || ins.NodeID == "" || ins.NodeID != string(s.owner) {
		return false, authErr
	}
	r.AppID = ins.AppID
	if err := s.failureRelay.RelayInstanceFailure(ctx, r); err != nil {
		s.log.Warn("schedd: relay foreign instance failure", "instance", r.InstanceID, "kind", r.Kind, "err", err)
		return false, status.Errorf(codes.Unavailable, "relay %s report for instance %s: %v", r.Kind, r.InstanceID, err)
	}
	return true, nil
}
