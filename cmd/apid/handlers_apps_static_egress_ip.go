// handlers_apps_static_egress_ip.go — apid handlers for ADR-119
// (static outbound IP per app, Scale-only, BYOIP, single-node v1).
//
// Routes (registered in cmd/apid/server.go::handler with the standard
// customer-scoped auth chain — see the mount block):
//
//	GET    /v1/apps/{slug}/static-egress-ip  → getAppStaticEgressIP
//	PUT    /v1/apps/{slug}/static-egress-ip  → setAppStaticEgressIP
//	DELETE /v1/apps/{slug}/static-egress-ip  → clearAppStaticEgressIP
//
// Why a dedicated endpoint trio (instead of folding static_egress_ip
// into the existing PATCH /v1/apps/{slug}):
//
//   - The flag is a *plan-gated* feature, not a per-app toggle. Mixing
//     it into the customer PATCH surface would force a 402 / 403 / 400
//     problem in the middle of an otherwise 200 surface, and the SDK
//     auto-generated clients would have to special-case the field.
//   - The PUT body is a typed request (ip + set), not a Set-bit
//     optional-pointer. This matches the canonical "PUT body for a
//     resource state" pattern (e.g. the per-app scaling policy PATCH
//     at handlers_apps_scaling.go).
//   - The cross-app duplicate-IP check is a unique-index violation
//     (SQLSTATE 23505) that the pgstore surfaces as ErrConflict. The
//     handler maps that to 403 plan_static_egress_ip_quota so the
//     dashboard can render the "another app on this account already
//     pins that IP" copy without round-tripping the conflict log.
//
// Feature flag: api.StaticEgressIPEnabled() (env FAAS_STATIC_EGRESS_IP_ENABLED).
// The flag is checked inside each handler so a misconfigured rollout
// surfaces as 402 (not 404) and the route table can be left wired
// during the dark-launch window — same posture as
// api.TenantSurfacesEnabled() at handlers_tenant_surfaces.go:45-48.

package main

import (
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// staticEgressIPNotEnabled returns the dark-launch 402 problem so a
// not-yet-rolled-out cluster signals "feature off" without exposing
// the route shape to the customer (404 would let an attacker probe
// for the endpoint by slug).
func staticEgressIPNotEnabled() *api.Problem {
	return api.ErrStaticEgressIPNotEnabled()
}

// getAppStaticEgressIP reads the per-app static egress IP pin.
// Plan-agnostic — returns the current state (with plan_allowed=false
// for non-Scale plans) so the dashboard can render the upsell
// without a separate plan lookup. Customer-scoped (no admin
// required).
func (s *server) getAppStaticEgressIP(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.StaticEgressIPEnabled() {
		api.WriteProblem(w, staticEgressIPNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	limits, _ := api.LimitsFor(acct.Plan)
	writeJSON(w, http.StatusOK, appStaticEgressIPResponse(app, limits))
}

// setAppStaticEgressIP pins a customer-supplied IPv4 to the app's
// egress traffic (Scale-only). Set=false with empty IP clears the
// pin (mirrors the DELETE wire shape so the same handler covers all
// three verbs in a single body type).
func (s *server) setAppStaticEgressIP(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.StaticEgressIPEnabled() {
		api.WriteProblem(w, staticEgressIPNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.SetAppStaticEgressIPRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.ErrValidation("invalid JSON body"))
		return
	}
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok {
		api.WriteProblem(w, api.ErrPlanStaticEgressIPNotAllowed(acct.Plan))
		return
	}
	if !limits.StaticEgressIPAllowed {
		api.WriteProblem(w, api.ErrPlanStaticEgressIPNotAllowed(acct.Plan))
		return
	}
	var (
		newIP     *netip.Addr
		willClear = !req.Set
		// nodeID (ADR-119 v2) is the IP's owning compute_nodes.id.
		// Stamped onto apps.node_id at the same UPDATE as
		// static_egress_ip; nil with SetNodeID=true clears the pin
		// (used by clearAppStaticEgressIP). See the operator-bundle
		// gate below for the lookup; the v2 migration guarantees
		// every (account_id, ip) tuple carries a non-null node_id.
		nodeID *string
	)
	if req.Set {
		ip, err := netip.ParseAddr(req.IP)
		if err != nil {
			api.WriteProblem(w, api.ErrAppStaticEgressIPInvalid(req.IP, "must be a dotted-quad IPv4 address"))
			return
		}
		if !ip.Is4() {
			api.WriteProblem(w, api.ErrAppStaticEgressIPInvalid(req.IP, "IPv6 is not supported in v1"))
			return
		}
		if err := api.ValidateStaticEgressIP(ip); err != nil {
			api.WriteProblem(w, api.ErrAppStaticEgressIPInvalid(req.IP, err.Error()))
			return
		}
		// ADR-119 redesign: operator-bundle gate. The pinned
		// IP must be in the operator's provisioned set
		// (the vmmd SIGHUP watcher writes the Postgres gate
		// table from /etc/faas/egress/static_egress_ips.toml;
		// this lookup is the read). A missing tuple is the
		// operator-side "this IP is not on the host's AS"
		// surface — 404 Not Found, distinct from the
		// 400 (bad IP) and 403 (plan quota) above. The
		// appendToAllocPool here is the per-VM host IP
		// reservation that drives the host renderer.
		ok, perr := s.store.ProvisionedStaticEgressIPExists(r.Context(), acct.ID, ip)
		if perr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not check operator bundle"))
			return
		}
		if !ok {
			api.WriteProblem(w, api.ErrStaticEgressIPNotProvisioned(req.IP))
			return
		}
		// ADR-119 v2: owner-node resolution. The IP must be
		// provisioned on exactly one compute_nodes.id (the host
		// that has the IP routed on its AS + aliased on
		// br-tenants). Schedd reads app.NodeID as the hard
		// placement constraint (pkg/sched/admission.go::
		// Request.RequiredNodeID); without this stamp the wake
		// could land on a non-owning node and the egress would
		// be source-spoofed at the switch (the v1 BYOIP
		// impossibility ADR-119 fixed). The (account_id, ip)
		// PK lookup is sub-millisecond — same index as the
		// ProvisionedStaticEgressIPExists gate above.
		//
		// A missing node_id on a tuple that DID pass the
		// operator-bundle gate is the operator-side "this IP
		// is in the bundle but not on a node" surface. That
		// row state is unreachable in v2 (migration 00488's
		// NOT NULL on provisioned_static_egress_ips.node_id
		// backfilled every existing tuple to 'default-local'),
		// but defence-in-depth: surface it as 500 so the
		// operator notices the broken bundle before the
		// customer pin lands.
		owningNodeID, nerr := s.store.StaticEgressIPNode(r.Context(), acct.ID, ip)
		if nerr != nil {
			api.WriteProblem(w, api.ErrCapacity("could not resolve owning node"))
			return
		}
		if owningNodeID == "" {
			api.WriteProblem(w, api.ErrCapacity("operator bundle has no node for this IP — fix the bundle and retry"))
			return
		}
		// ADR-119 redesign note: the per-VM host IP allocation
		// (pkg/fcvm.AcquireStaticEgressIP) is owned by vmmd, not
		// apid. The schedd egress_drift subscriber (pkg/sched/
		// egress_drift.go) listens for app_changed and calls
		// Router.UpdateStaticEgressIP gRPC on the vmmd client;
		// vmmd's gRPC handler calls AcquireStaticEgressIP. This
		// keeps the firecracker/jailer layer out of the apid
		// control-plane binary (CLAUDE.md component ownership).
		newIP = &ip
		nodeIDStr := owningNodeID
		nodeID = &nodeIDStr
	}
	updated, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{
		StaticEgressIP:    newIP,
		SetStaticEgressIP: true,
		// ADR-119 v2: stamp apps.node_id at the SAME UPDATE as
		// apps.static_egress_ip so schedd's wake path sees a
		// consistent (IP, owner_node) pair. The schedd
		// egress_drift subscriber (pkg/sched/egress_drift.go)
		// reads both from the pg_notify payload; a separate
		// UPDATE would race the fan-out and could leave the
		// app on the wrong node for one wake. SetNodeID=true
		// + NodeID=*nodeID writes the value; SetNodeID=true +
		// NodeID=nil clears the pin (see clearAppStaticEgressIP
		// below).
		NodeID:    nodeID,
		SetNodeID: true,
	})
	if err != nil {
		// Cross-app unique-index violation on apps_static_egress_ip_key
		// → 403 quota, mirroring the per-account egress-allowlist
		// quota path. Match on both state.ErrConflict (the wrapped
		// sentinel) AND the index name in the error string so a
		// unique violation from any future column doesn't false-
		// positive.
		if errors.Is(err, state.ErrConflict) && strings.Contains(err.Error(), "apps_static_egress_ip_key") {
			api.WriteProblem(w, api.ErrPlanStaticEgressIPQuota(acct.Plan, limits.StaticEgressIPsPerApp, 1, "another app on this account already pins that IP"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not update static egress IP"))
		return
	}
	// pg_notify on app_changed so schedd's egress_drift subscriber
	// (pkg/sched/egress_drift.go) fires UpdateStaticEgressIP gRPC
	// to patch live instances. The drift handler reads the IP
	// from the payload; we send the new state so a concurrent
	// clear+set pair still produces the correct MASQUERADE-sibling
	// rule on every live instance.
	payload := "null"
	if updated.StaticEgressIP != nil {
		payload = fmt.Sprintf("%q", updated.StaticEgressIP.String())
	}
	_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
		`{"kind":"static_egress_ip","app_id":"%s","account_id":"%s","ip":%s,"clear":%t}`,
		app.ID, acct.ID, payload, willClear))
	// Audit — IAM-4 (issue #291) shape: record what the customer
	// altered. The `app.static_egress_ip_set` kind is the distinct
	// taxonomy entry so the audit-log panel can filter the static
	// IP changes independently of the other PATCH surfaces.
	auditPayload := map[string]any{
		"app_id": app.ID,
		"clear":  willClear,
	}
	if updated.StaticEgressIP != nil {
		auditPayload["ip"] = updated.StaticEgressIP.String()
	} else {
		auditPayload["ip"] = nil
	}
	s.audit.Emit(r.Context(), "app.static_egress_ip_set", &acct.ID, auditPayload)
	writeJSON(w, http.StatusOK, appStaticEgressIPResponse(updated, limits))
}

// clearAppStaticEgressIP drops the per-app static egress IP pin.
// Convenience wrapper around setAppStaticEgressIP with Set=false.
// Idempotent — clearing a non-existent pin is a 204.
//
// ADR-119 redesign: also releases the per-VM host IP
// reservation in the alloc.go pool. The Wake path
// (pkg/fcvm/manager.go) only reads the reservation; the
// pair is (acquire on PUT, release on DELETE). The release
// is best-effort — a release failure logs but does not block
// the column clear (the customer's intent is the priority,
// and the reservation will be re-released on the next SIGHUP-
// triggered reconcile).
func (s *server) clearAppStaticEgressIP(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !api.StaticEgressIPEnabled() {
		api.WriteProblem(w, staticEgressIPNotEnabled())
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	// ADR-119 redesign note: the per-VM host IP reservation
	// (pkg/fcvm.ReleaseStaticEgressIP) is owned by vmmd, not
	// apid. The schedd egress_drift subscriber calls vmmd's
	// Router.UpdateStaticEgressIP gRPC with clear=true, and
	// vmmd's handler releases the reservation + rebuilds the
	// host renderer. The customer's intent (apps.static_egress_ip
	// = NULL) is the priority; the reservation release is the
	// vmmd handler's responsibility.
	if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{
		StaticEgressIP:    nil,
		SetStaticEgressIP: true,
		// ADR-119 v2: drop apps.node_id atomically with the
		// static_egress_ip clear. The schedd wake path reads
		// app.NodeID as the hard placement constraint; leaving
		// the column set after a clear would cause the next
		// wake to still pin to the previous owning node (the
		// IP is no longer routed there → source-spoofed egress
		// at the switch). SetNodeID=true + NodeID=nil writes
		// NULL atomically.
		NodeID:    nil,
		SetNodeID: true,
	}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not clear static egress IP"))
		return
	}
	_ = s.notif.Notify(r.Context(), "app_changed", fmt.Sprintf(
		`{"kind":"static_egress_ip","app_id":"%s","account_id":"%s","ip":null,"clear":true}`,
		app.ID, acct.ID))
	s.audit.Emit(r.Context(), "app.static_egress_ip_set", &acct.ID, map[string]any{
		"app_id": app.ID,
		"ip":     nil,
		"clear":  true,
	})
	w.WriteHeader(http.StatusNoContent)
}

// appStaticEgressIPResponse projects a state.App into the wire
// response. The plan_cap / plan_allowed pair lets the dashboard
// render the upsell without round-tripping the plan table.
func appStaticEgressIPResponse(app state.App, limits api.Limits) api.AppStaticEgressIPResponse {
	out := api.AppStaticEgressIPResponse{
		IP:          app.StaticEgressIP,
		PlanCap:     limits.StaticEgressIPsPerApp,
		PlanAllowed: limits.StaticEgressIPAllowed,
	}
	if app.StaticEgressIPSetAt != nil {
		// Defensive copy: the SetAt is a *time.Time on the App
		// value; we don't want the response to alias the store's
		// pointer so a future caller can't mutate it.
		t := *app.StaticEgressIPSetAt
		t = t.UTC()
		out.SetAt = &t
	}
	if out.IP == nil {
		// Force SetAt=nil when IP=nil so the wire shape is
		// self-consistent (mismatched ip+set_at would confuse the
		// dashboard).
		out.SetAt = nil
	}
	return out
}

// validCustomerEgressIP was the local deny-set copy. It now lives
// in pkg/api.ValidateStaticEgressIP (the canonical helper) as the
// single source of truth. The handler calls that directly.
