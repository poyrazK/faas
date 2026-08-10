package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apislogs"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhookdedupe"
	"github.com/onebox-faas/faas/pkg/wire"
)

// publicAuthBasicSealNamespace is the secretbox namespace tag
// apid stamps onto PATCH mode='basic' ciphertext, mirroring
// `appWebhookSecretSealLabel = "APP_WEBHOOK"` from
// handlers_webhooks.go:44. The partner string lives in
// cmd/gatewayd-internal/public_auth_unsealer.go; a future drift
// surfaces as a fail-closed decryption at gatewayd-internal boot (the
// unsealer rejects any sealed blob whose namespace tag doesn't
// match — see pkg/secretbox.SealBytes SetNamespaces contract).
const publicAuthBasicSealNamespace = "APP_BASIC_AUTH"

// --- apps CRUD --------------------------------------------------------------

// getApp returns one app by slug.
func (s *server) getApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	resp := s.appResponse(app, acct.Plan)
	writeJSON(w, http.StatusOK, s.withParkedDeploymentRef(r.Context(), resp, app))
}

// validateUpdateApp enforces the per-app cold-wake floor rules
// (ux_spec §6.5). Returns nil when the request is fine; otherwise a
// *Problem ready for api.WriteProblem. The gate runs before bounds
// checking because a 403 is the correct response on Free/Hobby
// regardless of the value the customer typed — the feature is
// tier-locked, not value-locked.
//
// Plan tier: only Pro/Scale may set MinInstances > 0 (403).
// Bounds: must be in [0, MaxConcurrency] (422).
//
// ADR-031 (tier-2 of the network roadmap): the egress allowlist is
// the second tier-locked knob. Same gate shape — only Pro/Scale may
// patch it (403 plan_egress_allowlist_not_allowed). Distinct
// failure modes warrant distinct codes so the CLI can branch:
//   - 403 plan_egress_allowlist_not_allowed  → Free/Hobby PATCH
//   - 400 egress_allowlist_too_long          → Pro/Scale but > cap
//   - 400 invalid_egress_allowlist           → a CIDR didn't parse,
//     or v6 (v1 is v4 only)
//
// The plan gate runs first (403 supersedes 400) so a Free account
// PATCHing a 64-entry list sees the plan error, not the size error.
//
// Returns *api.Problem instead of error to mirror cmd/apid/handlers.go
// buildApp, the established helper signature in this package.
func validateUpdateApp(req *api.UpdateAppRequest, acct state.Account, limits api.Limits, app state.App) *api.Problem {
	if req.MinInstances == nil {
		// fall through to the egress allowlist branch
	} else {
		if !acct.Plan.MinInstancesAllowed() {
			return api.ErrPlanMinInstancesNotAllowed(acct.Plan)
		}
		if *req.MinInstances < 0 || *req.MinInstances > limits.MaxConcurrency {
			return api.ErrInvalidMinInstances(*req.MinInstances, limits.MaxConcurrency)
		}
		// ADR-071 §Decision 5: per-plan MaxMinInstances cap
		// (Hobby 1, Pro 3, Scale 10). Tighter than MaxConcurrency
		// to protect the §6.2-2 RAM ceiling from a single API
		// call pinning a large fraction of the box.
		if *req.MinInstances > acct.Plan.MaxMinInstances() {
			return api.ErrMaxMinInstancesExceeded(*req.MinInstances, acct.Plan.MaxMinInstances())
		}
	}
	if req.EgressAllowlist != nil {
		// Plan tier first: a Free/Hobby PATCH must surface 403 even
		// if the request would otherwise be a malformed 400.
		if !acct.Plan.EgressAllowlistAllowed() {
			return api.ErrPlanEgressAllowlistNotAllowed(acct.Plan)
		}
		// Issue #679 / PR-B / ADR-082: per-account additive
		// budget widens the effective cap for THIS account only.
		// 0 (the default) = plan cap alone, preserves pre-PR-B
		// behaviour. The ceiling is enforced at the admin
		// handler (api.ErrAccountEgressAllowlistExtraOutOfRange);
		// the validator is the read side and trusts the
		// stored value.
		maxSize := acct.Plan.EgressAllowlistMaxSize() + acct.EgressAllowlistExtra
		if len(*req.EgressAllowlist) > maxSize {
			return api.ErrEgressAllowlistTooLong(len(*req.EgressAllowlist), maxSize)
		}
		// Per-entry shape: every CIDR must ParsePrefix as either v4
		// or v6 (ADR-032 — the v6 mirror), with a non-zero mask. The
		// Postgres cidr[] TRIGGER `apps_egress_allowlist_cidr`
		// (migration 00033) rejects families outside {4,6} and any
		// /0 at write time — catching it here just gives a more
		// operator-friendly error message naming the bad entry. The
		// `Bits() == 0` reject is shared with the DB trigger so a
		// future /0 rejection in either layer cannot quietly
		// disagree.
		//
		// PR-C (ADR-031+032 follow-up): beyond the bare shape check,
		// this loop now also (a) rewrites the v4-mapped v6 form
		// (RFC 4291 §2.5.5.2 — `::ffff:0:0/96` block) to its v4
		// form so the persisted row never carries a "::ffff:"
		// prefix and the read-back is consistent across plans, and
		// (b) de-duplicates entries AFTER canonicalisation so
		// `1.2.3.0/24` and `::ffff:1.2.3.0/120` collapse to one
		// entry. Insertion order is first-seen-wins — the surviving
		// entries keep the order they were submitted in. When the
		// canonicalised list differs from the input (rewrite OR
		// dedup), `req.EgressAllowlist` is replaced so the
		// downstream conversion at updateApp sees canonical strings
		// — without that replacement, the second parse would
		// silently resurrect the v4-mapped form on the wire.
		canonicalised := make([]netip.Prefix, 0, len(*req.EgressAllowlist))
		seen := make(map[string]struct{}, len(*req.EgressAllowlist))
		// dirty tracks whether ANY entry had its string form
		// changed (v4-mapped rewrite) OR was dropped (dedup). The
		// downstream `len(canonicalised) != len(input)` check
		// alone is not enough — a single-entry input that
		// canonicalises but doesn't dedup has the same length
		// before and after. Without this flag, the rewritten
		// string never replaces the original on req, and the
		// second parse at updateApp silently resurrects the
		// pre-canonical form on the wire.
		dirty := false
		for _, raw := range *req.EgressAllowlist {
			prefix, err := netip.ParsePrefix(raw)
			if err != nil || prefix.Bits() == 0 {
				return api.ErrInvalidEgressAllowlist(raw, errOrZero("parse failed", err))
			}
			// v4-mapped v6 → v4. RFC 4291 §2.5.5.2: ::ffff:0:0/96
			// is the v4-mapped block; any prefix inside that block
			// (bits >= 96) is a v4 prefix translated to v6 form.
			// Rewrite to the canonical v4 form. The DB trigger
			// holds the v4 /0 floor; the handler catches "wider
			// than /8" here so the operator gets a more actionable
			// message than a generic Postgres constraint error.
			if prefix.Addr().Is4In6() && prefix.Bits() >= 96 {
				v4Bits := prefix.Bits() - 96
				if v4Bits < 8 {
					return api.ErrInvalidEgressAllowlist(raw,
						errors.New("v4-mapped prefix maps to v4 /0..7"))
				}
				// Unmap() strips the ::ffff: prefix from the v6
				// representation so the resulting prefix is a true
				// v4 prefix (Is4() returns true, Is4In6() returns
				// false, prefix.String() renders without `::`).
				// Without the Unmap, Masked().Addr() returns the
				// v6 form (e.g. `::ffff:1.2.3.0`) and the rewrite
				// silently produces `::/24` instead of `1.2.3.0/24`.
				prefix = netip.PrefixFrom(prefix.Masked().Addr().Unmap(), v4Bits).Masked()
				dirty = true
			}
			key := prefix.String()
			if _, ok := seen[key]; ok {
				dirty = true // dedup drop counts as a change.
				continue
			}
			seen[key] = struct{}{}
			canonicalised = append(canonicalised, prefix)
		}
		if dirty {
			rewritten := make([]string, len(canonicalised))
			for i, p := range canonicalised {
				rewritten[i] = p.String()
			}
			req.EgressAllowlist = &rewritten
		}
	}
	// Issue #169 / #172: per-app reactive scale-up trigger. Plan
	// gates run first (403 supersedes 422) so a Free account
	// PATCHing an invalid value surfaces the gate error, not the
	// bounds error. RPS is Hobby+ (Free is single-concurrency and
	// can't grow beyond 1); CPU is Pro+ (Hobby's cost band is too
	// tight for "scale on CPU without a min_instances floor").
	if req.AutoscaleTargetRPS != nil {
		if !acct.Plan.ScaleUpTargetRPSAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanScaleUpNotAllowed,
				"Autoscale target RPS is not allowed on this plan",
				"Free tier does not support per-app autoscaling; upgrade to Hobby or higher.")
		}
		// 0 is the explicit-disable form (the Set bit is set, so
		// the column gets overwritten to 0). Negative values are
		// still rejected — 422 invalid_autoscale_target_rps.
		if *req.AutoscaleTargetRPS < 0 {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeInvalidAutoscaleTargetRPS,
				"Invalid autoscale target RPS",
				fmt.Sprintf("autoscale_target_rps must be >= 0 (0 = disable); got %d", *req.AutoscaleTargetRPS))
		}
	}
	if req.AutoscaleTargetCPUPct != nil {
		if !acct.Plan.ScaleUpTargetCPUAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanScaleUpNotAllowed,
				"Autoscale target CPU%% is not allowed on this plan",
				"Autoscale target CPU%% requires Pro or Scale; upgrade to a paid tier.")
		}
		// 0 is the explicit-disable form; values outside [1, 100]
		// are invalid (PG CHECK enforces this as a second-layer
		// defense).
		if *req.AutoscaleTargetCPUPct != 0 && (*req.AutoscaleTargetCPUPct < 1 || *req.AutoscaleTargetCPUPct > 100) {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeInvalidAutoscaleTargetCPU,
				"Invalid autoscale target CPU%%",
				fmt.Sprintf("autoscale_target_cpu_pct must be 0 (disable) or in [1, 100]; got %d", *req.AutoscaleTargetCPUPct))
		}
	}
	// Issue #471 / ADR-047: per-app streaming flag. The plan gate
	// runs after the bounds checks above so a Free customer
	// PATCHing true receives a 403 plan_streaming_not_allowed
	// (the action is forbidden for this account), not a 422
	// bounds error. The plan default is applied at create time
	// (cmd/apid/handlers.go::buildApp), so a Hobby customer
	// PATCHing nil is a no-op (the Set bit is unset in updateApp's
	// UpdateAppParams call below).
	if req.StreamingEnabled != nil && *req.StreamingEnabled {
		if !acct.Plan.StreamingResponseAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanStreamingNotAllowed,
				"Streaming responses are not allowed on this plan",
				"Free tier does not support per-app streaming; upgrade to Hobby or higher.")
		}
	}
	// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge flag.
	// Same plan-gate shape as streaming — Free + true = 403
	// plan_websocket_not_allowed. Free is the abuse-floor tier where
	// a single long-lived WS would pin a wake past wake_idle_timeout,
	// so the gate is fail-closed at create AND PATCH time (no override
	// path). A Hobby/Pro/Scale customer may PATCH true → false to
	// opt out (a synchronous JSON API that does not want long-poll
	// pinning); the false direction needs no gate.
	if req.WebSocketEnabled != nil && *req.WebSocketEnabled {
		if !acct.Plan.WebSocketResponseAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanWebSocketNotAllowed,
				"WebSocket / Upgrade traffic is not allowed on this plan",
				"Free tier does not support per-app WebSocket; upgrade to Hobby or higher.")
		}
	}
	// Issue #470 / ADR-055: per-app two-tier-snapshot flag. Same
	// plan-gate shape as streaming — Free/Hobby + true = 403
	// plan_warm_snapshot_not_allowed. Out-of-range thresholds =
	// 422 invalid_warm_snapshot_min_* (the SQL CHECK rejects the
	// same range, but the apid layer catches it first so the
	// customer sees a clean validation error).
	if req.WarmSnapshotEnabled != nil && *req.WarmSnapshotEnabled {
		if !acct.Plan.WarmSnapshotAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanWarmSnapshotNotAllowed,
				"Warm-tier snapshots are not allowed on this plan",
				"Free and Hobby tiers do not support per-app warm snapshots; upgrade to Pro or higher.")
		}
	}
	// Issue #560: per-app require_authn opt-in. Same plan-gate
	// shape as the warm-snapshot / streaming gates — Free/Hobby +
	// true = 403 plan_require_authn_not_allowed. The default
	// stays false (column default + create-time default), so
	// every existing customer keeps being public-by-default and
	// a Free customer's PATCH-true surfaces as the gate error
	// before any SQL write. Cross-account token scope is
	// enforced by the gatewayd-internal authz branch, not here
	// — the PATCH endpoint only checks plan eligibility, not
	// future request authenticity.
	if req.RequireAuthn != nil && *req.RequireAuthn {
		if !acct.Plan.RequireAuthnAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanRequireAuthnNotAllowed,
				"Per-deployment authentication is not allowed on this plan",
				"Free and Hobby tiers do not support per-app require_authn; upgrade to Pro or higher.")
		}
	}
	if req.WarmSnapshotMinRequests != nil {
		v := *req.WarmSnapshotMinRequests
		if v < 1 || v > 100 {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeInvalidWarmSnapshotMinRequests,
				"Invalid warm_snapshot_min_requests",
				fmt.Sprintf("warm_snapshot_min_requests must be in [1, 100]; got %d", v))
		}
	}
	if req.WarmSnapshotMinMs != nil {
		v := *req.WarmSnapshotMinMs
		if v < 100 || v > 60000 {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeInvalidWarmSnapshotMinMs,
				"Invalid warm_snapshot_min_ms",
				fmt.Sprintf("warm_snapshot_min_ms must be in [100, 60000]; got %d", v))
		}
	}
	// Issue #475: per-app eviction_priority tier. The validation
	// chain runs in this order so the customer sees the most
	// specific error first:
	//
	//  1. Bounds: only 'best_effort' and 'reserved' are legal.
	//     422 validation_failed for anything else (SQL CHECK
	//     catches the same closed set, but the apid layer
	//     intercepts so the customer sees a clean error).
	//  2. Plan gate: Free PATCH 'reserved' returns 403
	//     plan_eviction_priority_reserved_not_allowed. The plan
	//     DOES unlock 'best_effort' (the pre-#475 default), so
	//     PATCH 'best_effort' is always allowed (any plan may
	//     go in either direction once the cap is unlocked).
	//  3. Per-account cap: Hobby 1, Pro 2, Scale 4. 422
	//     plan_eviction_priority_reserved_quota when the cap is
	//     reached. Counts APPS (not instances) — a single
	//     reserved app with 5 concurrent instances counts as 1.
	//     The cap is computed against the post-PATCH state
	//     (existing reserved apps + 1 if the current app is
	//     flipping up, 0 if it's already reserved).
	//
	// The cap run is NOT under an apps-row FOR UPDATE lock here
	// (the existing warm-snapshot / streaming flags don't lock
	// either). The plan caps are advisory: a racing PATCH from
	// the same account could land one extra reserved app. The
	// financial model's per-account RAM cap (47,600 MB) is the
	// hard backstop so a cap race costs nothing on the box.
	if req.EvictionPriority != nil {
		v := *req.EvictionPriority
		if v != string(api.EvictionPriorityBestEffort) && v != string(api.EvictionPriorityReserved) {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeValidation,
				"Invalid eviction_priority",
				fmt.Sprintf("eviction_priority must be 'best_effort' or 'reserved'; got %q", v))
		}
		if v == string(api.EvictionPriorityReserved) {
			if !acct.Plan.EvictionPriorityReservedAllowed() {
				return api.ErrPlanEvictionPriorityReservedNotAllowed(acct.Plan)
			}
			// Per-account cap. Skip the read when the value
			// is already 'reserved' on the current app — the
			// PATCH is a no-op and the cap is unchanged.
			// app is loaded later (loadApp); we read it once
			// here and pass it through to the audit-block.
		}
	}
	// Issue #477 / ADR-079: per-app public_auth (open|bearer|basic).
	// Plan-gated upstream: apid returns 402
	// plan_public_auth_{bearer,basic}_not_allowed when the
	// customer's plan lacks the gate. The bearer path
	// re-uses the require_authn chain (apps:read scope on
	// the app's owning account) so the gate is Hobby+;
	// basic adds a secretbox seal + per-app unseal and is
	// Pro+. 'open' is always allowed (the pre-#477 default).
	// Validation runs FIRST (closed-enum + length bounds)
	// so a Free customer who tries PATCH mode='weird' gets
	// a 422 invalid_public_auth_mode rather than a
	// confusing 402 plan_public_auth_bearer_not_allowed.
	if req.PublicAuth != nil {
		if prob := req.PublicAuth.Validate(); prob != nil {
			return prob
		}
		switch req.PublicAuth.Mode {
		case api.AppPublicAuthModeBearer:
			if !acct.Plan.PublicAuthBearerAllowed() {
				return api.ErrPlanPublicAuthBearerNotAllowed(acct.Plan)
			}
		case api.AppPublicAuthModeBasic:
			if !acct.Plan.PublicAuthBasicAllowed() {
				return api.ErrPlanPublicAuthBasicNotAllowed(acct.Plan)
			}
		}
	}
	// Issue #462 / ADR-058: per-app scaling policy (PR-A persists
	// + Hobby+ tier-up; PR-C wires the engine; PR-D carves out the
	// worker-class branch). The DTO uses value semantics so the
	// wire form allows `{}` (zero-value policy = "scale to zero,
	// the v1 contract"). The Set bit distinguishes "don't touch
	// the jsonb column" (nil pointer) from "explicit zero policy"
	// (non-nil with all-zero fields). The worker-class carve-out
	// runs separately in updateApp after loadApp surfaces the
	// per-app WorkloadClass.
	if req.ScalingPolicy != nil {
		sp := req.ScalingPolicy
		// Strict UnmarshalJSON: a typo on the wire (e.g. `min_instance`
		// vs `min_instances`) must surface as 422 validation_failed
		// rather than silently dropping the field. The unknown-field
		// check runs FIRST so a malformed shape doesn't drown in
		// downstream cooldown / bounds errors.
		if sp.HasUnknownFields() {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeValidation,
				"Invalid scaling policy",
				fmt.Sprintf("unknown field(s): %s", strings.Join(sp.UnknownFields(), ", ")))
		}
		sp.ClearUnknownFields()
		// Worker-class carve-out (PR-D): live here in the validator
		// (rather than at the call site in updateApp) so an
		// unknown-field error surfaces before the workload-class
		// error when the wire body has both. The signal source
		// (`pkg/vmmd/activity.ActivityTracker`) counts in-flight
		// HTTP requests; a worker has none, so the metric is
		// forever 0 and the engine would never admit. PR-D closes
		// the engine-side bypass.
		if sp.Target != nil && sp.Target.Metric == "concurrent_requests" &&
			app.WorkloadClass == state.WorkloadClassWorker {
			return api.ErrScalingTargetIncompatibleWithWorkloadClass("concurrent_requests")
		}
		// Plan gates first (403 supersedes 422): a Free customer
		// patching a valid policy still sees the plan error.
		if sp.MinInstances > 0 && !acct.Plan.MinInstancesAllowed() {
			return api.ErrPlanMinInstancesNotAllowed(acct.Plan)
		}
		if sp.MaxInstances > 0 && !acct.Plan.MaxInstancesAllowed() {
			return api.ErrPlanMaxInstancesNotAllowed(acct.Plan)
		}
		// Bounds on min_instances: still in [0, plan.MaxConcurrency].
		// 0 is the explicit "scale to zero" form below the engine
		// floor (1) — the engine applies the floor at wake time, so
		// the apid gate only rejects the negative / over-cap cases.
		if sp.MinInstances < 0 || sp.MinInstances > limits.MaxConcurrency {
			return api.ErrInvalidMinInstances(sp.MinInstances, limits.MaxConcurrency)
		}
		// ADR-071 §Decision 5: per-plan MaxMinInstances cap
		// (Hobby 1, Pro 3, Scale 10). Tighter than MaxConcurrency
		// to protect the §6.2-2 RAM ceiling from a single API
		// call pinning a large fraction of the box.
		if sp.MinInstances > acct.Plan.MaxMinInstances() {
			return api.ErrMaxMinInstancesExceeded(sp.MinInstances, acct.Plan.MaxMinInstances())
		}
		// Bounds on max_instances: must be in [MinInstances, plan.MaxConcurrency].
		// 0 means "use plan max_concurrency"; the engine reads the
		// resolved value at wake time. The bounds check uses the
		// customer-authored min_instances — if the customer
		// PATCHed min_instances=0, max_instances=0 is also valid
		// (the engine resolves to plan max_concurrency).
		if sp.MaxInstances < 0 {
			return api.ErrInvalidMaxInstances(sp.MaxInstances, sp.MinInstances, limits.MaxConcurrency)
		}
		if sp.MaxInstances > 0 && sp.MaxInstances > limits.MaxConcurrency {
			return api.ErrInvalidMaxInstances(sp.MaxInstances, sp.MinInstances, limits.MaxConcurrency)
		}
		if sp.MaxInstances > 0 && sp.MaxInstances < sp.MinInstances {
			return api.ErrInvalidMaxInstances(sp.MaxInstances, sp.MinInstances, limits.MaxConcurrency)
		}
		// Cooldown floors + ceilings. The plan allows a customer
		// to opt for a tighter cooldown (e.g. 5 s on Hobby) than
		// the engine default, but the floors prevent self-DoS via
		// `cooldown_s: 0`.
		if sp.ScaleOutCooldownS < api.MinScaleOutCooldownS ||
			sp.ScaleOutCooldownS > api.MaxScaleOutCooldownS {
			return api.ErrInvalidCooldown("scale_out_cooldown_s",
				sp.ScaleOutCooldownS, api.MinScaleOutCooldownS, api.MaxScaleOutCooldownS)
		}
		if sp.ScaleInCooldownS < api.MinScaleInCooldownS ||
			sp.ScaleInCooldownS > api.MaxScaleInCooldownS {
			return api.ErrInvalidCooldown("scale_in_cooldown_s",
				sp.ScaleInCooldownS, api.MinScaleInCooldownS, api.MaxScaleInCooldownS)
		}
		// Target metric surface: closed set. Empty Metric is the
		// "engine-derives from autoscale_target_rps" path (legacy
		// compat). The metric surface is the only field that
		// triggers the workload-class gate, but the actual reject
		// runs in updateApp after loadApp — the validator here
		// only checks the value shape.
		if sp.Target != nil {
			switch sp.Target.Metric {
			case "", "rps", "concurrent_requests", "p99_latency_ms":
				// ok
			default:
				return api.NewProblem(http.StatusUnprocessableEntity,
					api.CodeValidation,
					"Invalid scaling policy",
					fmt.Sprintf("target.metric=%q is not in the closed set (rps, concurrent_requests, p99_latency_ms).", sp.Target.Metric))
			}
			if sp.Target.Value < 0 {
				return api.NewProblem(http.StatusUnprocessableEntity,
					api.CodeValidation,
					"Invalid scaling policy",
					fmt.Sprintf("target.value must be >= 0; got %v.", sp.Target.Value))
			}
		}
	}
	// Issue #472 / ADR-054: per-app cosign signature-enforcement flag
	// is NOT settable via the customer PATCH surface — the operator
	// controls it through PATCH /v1/apps/{slug}/security
	// (handlers_security.go) which mounts with the admin+MFA chain.
	// updateApp silently drops a customer-set RequireSigned; the
	// field stays on the wire shape (UpdateAppRequest) for SDK
	// stability but is honoured only on the admin route.
	return nil
}

// errOrZero shapes the error message in api.ErrInvalidEgressAllowlist.
// When ParsePrefix fails err is non-nil; the v4 / zero-bits branches
// fire without one, so we synthesise a stable suffix instead of
// relying on a nil-stringer that prints "<nil>".
func errOrZero(msg string, err error) error {
	if err != nil {
		return err
	}
	return errors.New(msg)
}

// updateApp is the PATCH /v1/apps/{slug} handler. User-tunable:
// RAM, idle_timeout_s, max_concurrency, and min_instances (Pro/Scale
// only — validateUpdateApp gates the feature). Type and runtime are
// immutable. Plan caps re-enforced when RAM or concurrency changes
// (spec §4.2: "validation enforces plan quotas before any work").
func (s *server) updateApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.UpdateAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	ram, mc := app.RAMMB, app.MaxConcurrency
	if req.RAMMB != nil {
		ram = *req.RAMMB
	}
	if req.MaxConcurrency != nil {
		mc = *req.MaxConcurrency
	}
	if prob := api.ValidateAppConfig(limits, ram, mc); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Issue #462 / ADR-058 / PR-D carve-out (worker-class vs
	// `target.metric = concurrent_requests`) lives inside
	// validateUpdateApp below, where it runs AFTER the unknown-
	// fields check so a malformed wire body surfaces the wire-shape
	// error rather than the workload-class error. The signal source
	// is `pkg/vmmd/activity.ActivityTracker` (PR-B) which counts
	// in-flight HTTP requests; a worker has none, so the metric is
	// forever 0 and the engine would never admit.
	if prob := validateUpdateApp(&req, acct, limits, app); prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// Resolve the wire name → UUID server-side before the
	// store call so the column carries the resolved UUID
	// (the column-shape integrity contract — uuid NULL, not
	// text). `strictEmpty=false` because PATCH allows `""` to
	// mean "clear the preference" (an explicit transition,
	// distinct from nil = "don't touch the column"). On any
	// 4xx we bail; on success the local var carries the
	// resolved UUID (or "" for clear).
	overflowUUID, prob := s.resolveOverflowNode(r.Context(), req.OverflowNode, false /*strictEmpty*/)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// SetMinInstances: nil pointer means "don't touch"; non-nil
	// (even pointing at 0) means "explicit set" → scale to zero.
	//
	// ADR-031: EgressAllowlist follows the same convention — nil
	// pointer = "don't touch the column", non-nil = "atomic
	// full-overwrite of the list" (including the empty slice, which
	// clears the allowlist back to chain-default-accept). Validation
	// already proved the list is plan-sized and every CIDR is a
	// valid v4, so the state layer is a straightforward delegate.
	var allowPrefixes *[]netip.Prefix
	if req.EgressAllowlist != nil {
		in := *req.EgressAllowlist
		out := make([]netip.Prefix, len(in))
		for i, s := range in {
			// already validated by validateUpdateApp
			out[i], _ = netip.ParsePrefix(s)
		}
		allowPrefixes = &out
	}
	// Issue #475: per-account reserved-tier cap. The bounds + plan
	// gates ran earlier (above the loadApp call at the top of
	// updateApp), so when control reaches here req.EvictionPriority
	// is either nil, 'best_effort' (always allowed), or 'reserved'
	// (plan unlocked). The cap only triggers when the value is
	// 'reserved' AND the current app is not already reserved — a
	// no-op PATCH on a reserved app must not exhaust the cap.
	//
	// The cap is advisory (no FOR UPDATE lock); a racing PATCH from
	// the same account could land one extra reserved app. The
	// financial model's per-account RAM cap (47,600 MB) is the
	// hard backstop, so a cap race costs nothing on the box.
	if req.EvictionPriority != nil && *req.EvictionPriority == string(api.EvictionPriorityReserved) && app.EvictionPriority != string(api.EvictionPriorityReserved) {
		cap := acct.Plan.ReservedConcurrencyPerAccount()
		if cap > 0 {
			n, err := s.store.CountAppsWithEvictionPriority(r.Context(), acct.ID, string(api.EvictionPriorityReserved))
			if err != nil {
				api.WriteProblem(w, api.ErrInternal(fmt.Sprintf("count reserved apps: %v", err)))
				return
			}
			// n excludes the current app already (CountAppsWithEvictionPriority
			// counts the full set; the current app is reserved if and only
			// if app.EvictionPriority == 'reserved'). The flip-up direction
			// adds 1 to the post-PATCH count.
			if n+1 > cap {
				api.WriteProblem(w, api.ErrPlanEvictionPriorityReservedQuota(acct.Plan, n+1, cap))
				return
			}
		}
	}

	// Issue #477 / ADR-079: seal the basic-auth creds (if
	// the operator PATCHed mode='basic'). The seal happens
	// here, BEFORE the UpdateAppParams construction, so
	// the on-wire UpdateAppParams.PublicAuth.BasicSealed
	// is always ciphertext (the store layer never sees
	// plaintext). For mode='open' / 'bearer', the sealed
	// blob is cleared (nil) so a stale secretbox row from
	// a previous PATCH doesn't reach a fresh request.
	var publicAuthSealed []byte
	if req.PublicAuth != nil && req.PublicAuth.Mode == api.AppPublicAuthModeBasic {
		recipient := setSecretRecipient()
		if recipient == nil {
			api.WriteProblem(w, api.ErrCapacity("host age recipient not loaded — refusing to seal public_auth credentials"))
			return
		}
		// Plaintext shape: "<basic_user>\n<basic_pass>" —
		// newline-delimited so neither field can contain
		// the other. The unsealer at
		// cmd/gatewayd-internal/public_auth_unsealer.go
		// splits on the first newline and treats both
		// halves as required.
		plaintext := []byte(req.PublicAuth.BasicUser + "\n" + req.PublicAuth.BasicPass)
		sealed, err := secretbox.SealBytes(recipient, publicAuthBasicSealNamespace, plaintext, api.AppPublicAuthBasicMaxBytes)
		if err != nil {
			if prob := api.AsProblem(err); prob != nil {
				api.WriteProblem(w, prob)
				return
			}
			api.WriteProblem(w, api.ErrCapacity("could not seal public_auth credentials"))
			return
		}
		publicAuthSealed = sealed
	}
	params := state.UpdateAppParams{
		RAMMB:              req.RAMMB,
		IdleTimeoutS:       req.IdleTimeoutS,
		SetIdleTimeout:     req.IdleTimeoutS != nil,
		MaxConcurrency:     req.MaxConcurrency,
		MinInstances:       req.MinInstances,
		SetMinInstances:    req.MinInstances != nil,
		EgressAllowlist:    allowPrefixes,
		SetEgressAllowlist: req.EgressAllowlist != nil,
		// Issue #169 / #172: autoscale trigger targets. Set bits
		// distinguish "unset" from "explicit zero" (the disable
		// signal). Plain nil-with-Set=false leaves the column
		// untouched. Apid validation already gated the plan and
		// the bounds; the store is a plain column write.
		AutoscaleTargetRPS:       req.AutoscaleTargetRPS,
		SetAutoscaleTargetRPS:    req.AutoscaleTargetRPS != nil,
		AutoscaleTargetCPUPct:    req.AutoscaleTargetCPUPct,
		SetAutoscaleTargetCPUPct: req.AutoscaleTargetCPUPct != nil,
		// Issue #471 / ADR-047: per-app streaming flag. Set bit
		// distinguishes "unset" from "explicit false" (opt out of
		// streaming). Apid validation already gated the plan; the
		// store is a plain column write.
		StreamingEnabled:    req.StreamingEnabled,
		SetStreamingEnabled: req.StreamingEnabled != nil,
		// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge
		// flag. The setter bit distinguishes "unset" from "explicit
		// false" (opt out of websocket). Apid validation already
		// gated the plan; the store is a plain column write.
		WebSocketEnabled:    req.WebSocketEnabled,
		SetWebSocketEnabled: req.WebSocketEnabled != nil,
		// Issue #462 / ADR-058: per-app scaling policy. The
		// setter bit on UpdateAppParams distinguishes "don't
		// touch" (nil pointer) from "explicit zero policy"
		// (non-nil with all-zero fields = "scale to zero"). The
		// store layer writes the jsonb column `apps.scaling_policy`
		// and keeps the legacy `min_instances` column in sync so
		// legacy readers (the reaper + the SDK) see the same
		// floor.
		ScalingPolicy:    policyPtrFromReq(&req),
		SetScalingPolicy: req.ScalingPolicy != nil,
		// Issue #472 / ADR-054: per-app cosign signature-enforcement
		// flag is NOT settable via the customer PATCH surface.
		// Operators control it through PATCH /v1/apps/{slug}/security
		// (handlers_security.go), which mounts with the admin+MFA
		// chain. updateApp SILENTLY DROPS a customer-set
		// RequireSigned — the field is in UpdateAppRequest so the
		// SDK wire shape stays stable, but it's not honoured here.
		// imaged reads the column at buildImageLayer time regardless
		// of how it got set.
		//
		// Issue #470 / ADR-055: per-app warm-snapshot knobs ARE
		// settable via the customer PATCH surface. The plan gate
		// (Free/Hobby + true → 403 plan_warm_snapshot_not_allowed)
		// is enforced inside this handler before the store call so
		// the SQL never sees an illegal value. The min_request /
		// min_ms bounds are enforced at the JSON-decode layer.
		WarmSnapshotEnabled:        req.WarmSnapshotEnabled,
		SetWarmSnapshotEnabled:     req.WarmSnapshotEnabled != nil,
		WarmSnapshotMinRequests:    req.WarmSnapshotMinRequests,
		SetWarmSnapshotMinRequests: req.WarmSnapshotMinRequests != nil,
		WarmSnapshotMinMs:          req.WarmSnapshotMinMs,
		SetWarmSnapshotMinMs:       req.WarmSnapshotMinMs != nil,
		// Issue #475: per-app eviction tier. The Set bit
		// distinguishes "don't touch" (nil pointer) from "explicit
		// best_effort" (opt out of reserved). The plan gate +
		// per-account cap run above the UpdateApp call so by
		// here the value is either 'best_effort' (always allowed)
		// or 'reserved' (plan unlocked, cap not exhausted).
		EvictionPriority:    req.EvictionPriority,
		SetEvictionPriority: req.EvictionPriority != nil,
		// Issue #560: per-app require_authn opt-in. Set bit
		// distinguishes "unset" (don't touch) from "explicit
		// false" (opt out — back to public-by-default). Plan
		// gate above has already rejected Free/Hobby + true
		// with 403 plan_require_authn_not_allowed, so the
		// SQL never sees an illegal value. Free/Hobby
		// customers may PATCH true → false to opt out on a
		// Pro-upgraded app; Hobby customers may opt back out
		// the same way.
		RequireAuthn:    req.RequireAuthn,
		SetRequireAuthn: req.RequireAuthn != nil,
		// Issue #477 / ADR-079: per-app public_auth
		// (open|bearer|basic). Set bit distinguishes "unset"
		// (don't touch) from explicit mode flip. The sealed
		// blob is always ciphertext (the seal ran above for
		// mode='basic'; nil for mode='open'/'bearer' so a
		// stale secretbox row from a previous PATCH doesn't
		// leak creds). The plan gate ran above; the store
		// is a plain column write. The block builds the
		// AppPublicAuthUpdate *only* when req.PublicAuth is
		// non-nil; passing nil in the else case keeps the
		// SQL write a no-op (SetPublicAuth=false) so a
		// partial-PATCH (e.g. only flips RAM_MB) never
		// touches the public_auth column.
		SetPublicAuth: req.PublicAuth != nil,
		// Issue #695 / ADR-080: grand-father clear path. Set
		// whenever the customer made a deliberate choice on
		// require_authn OR public_auth — that's the signal
		// the dashboard banner looks for. A no-touch PATCH
		// (e.g. RAM_MB-only) leaves ClearAuthDefaultFlippedAt
		// false so the stamp stays and the banner keeps
		// re-rendering. New post-flip apps (column NULL on
		// create) are unaffected by the SET.
		ClearAuthDefaultFlippedAt: req.RequireAuthn != nil || req.PublicAuth != nil,
		// Tier A10 / ADR-088: per-app overflow_node preference.
		// `req.OverflowNode != nil` distinguishes "don't touch
		// the column" (nil pointer) from "explicit clear or
		// explicit set" (non-nil pointer; "" means clear,
		// non-empty means the resolved UUID). Resolution to
		// UUID ran above (resolveOverflowNode) so this
		// carries the canonical column value, never the
		// wire-format name.
		OverflowNode:    nilStringPtr(overflowUUID),
		SetOverflowNode: req.OverflowNode != nil,
	}
	if req.PublicAuth != nil {
		// params.PublicAuth is unset when req.PublicAuth is
		// nil — the store reads SetPublicAuth below to skip
		// the column write. When the customer DID send a
		// public_auth block, build the AppPublicAuthUpdate
		// struct (mode + plaintext username/password that
		// the seal step buffered into publicAuthSealed).
		params.PublicAuth = &state.AppPublicAuthUpdate{
			Mode:     req.PublicAuth.Mode,
			Username: req.PublicAuth.BasicUser,
			Password: req.PublicAuth.BasicPass,
			Sealed:   publicAuthSealed,
		}
	}
	updated, err := s.store.UpdateApp(r.Context(), app.ID, params)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update app"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app updated", "app", updated.ID, "slug", updated.Slug, "account", acct.ID)
	// IAM-4 (issue #291): record what the customer actually altered.
	// Mirrors cron.updated: only fields the caller touched (req.X != nil)
	// appear in old/new, so the audit row answers "what AND to what"
	// without re-derivation. EgressAllowlist is rendered in canonical
	// string form (the same form the API surfaces) rather than the
	// raw netip.Prefix so the JSON is stable across Go minor bumps.
	oldApp := map[string]any{}
	newApp := map[string]any{}
	if req.RAMMB != nil {
		oldApp["ram_mb"] = app.RAMMB
		newApp["ram_mb"] = updated.RAMMB
	}
	if req.MaxConcurrency != nil {
		oldApp["max_concurrency"] = app.MaxConcurrency
		newApp["max_concurrency"] = updated.MaxConcurrency
	}
	if req.IdleTimeoutS != nil {
		oldApp["idle_timeout_s"] = app.IdleTimeoutS
		newApp["idle_timeout_s"] = updated.IdleTimeoutS
	}
	if req.MinInstances != nil {
		oldApp["min_instances"] = app.MinInstances
		newApp["min_instances"] = updated.MinInstances
	}
	if req.AutoscaleTargetRPS != nil {
		oldApp["autoscale_target_rps"] = app.AutoscaleTargetRPS
		newApp["autoscale_target_rps"] = updated.AutoscaleTargetRPS
	}
	if req.AutoscaleTargetCPUPct != nil {
		oldApp["autoscale_target_cpu_pct"] = app.AutoscaleTargetCPUPct
		newApp["autoscale_target_cpu_pct"] = updated.AutoscaleTargetCPUPct
	}
	if req.StreamingEnabled != nil {
		// Issue #471 / ADR-047: record what the customer altered on
		// the streaming flag. Same shape as the autoscale entries
		// above — only fields the caller touched appear in the audit.
		oldApp["streaming_enabled"] = app.StreamingEnabled
		newApp["streaming_enabled"] = updated.StreamingEnabled
	}
	if req.WebSocketEnabled != nil {
		// Issue #676 / ADR-080: record what the customer altered on
		// the websocket flag. Same shape as the streaming block
		// above — only fields the caller touched appear in the
		// audit. The plan gate already validated this is a legal
		// direction (Free + true is rejected upstream).
		oldApp["websocket_enabled"] = app.WebSocketEnabled
		newApp["websocket_enabled"] = updated.WebSocketEnabled
	}
	// Note: req.RequireSigned is intentionally NOT audited here —
	// it's silently dropped by updateApp (see above). The audit
	// path for the toggle lives on PATCH /v1/apps/{slug}/security
	// (handlers_security.go) where the admin+MFA chain guarantees
	// the operator-only posture.
	// Issue #470 / ADR-055: warm-snapshot toggles + threshold
	// overrides are recorded alongside the other app.updated
	// entries. The Set bit drives "what did the customer actually
	// change" — a `false` here means the audit row is unchanged
	// from the streaming block above.
	if req.WarmSnapshotEnabled != nil {
		oldApp["warm_snapshot_enabled"] = app.WarmSnapshotEnabled
		newApp["warm_snapshot_enabled"] = updated.WarmSnapshotEnabled
	}
	if req.WarmSnapshotMinRequests != nil {
		oldApp["warm_snapshot_min_requests"] = app.WarmSnapshotMinRequests
		newApp["warm_snapshot_min_requests"] = updated.WarmSnapshotMinRequests
	}
	if req.WarmSnapshotMinMs != nil {
		oldApp["warm_snapshot_min_ms"] = app.WarmSnapshotMinMs
		newApp["warm_snapshot_min_ms"] = updated.WarmSnapshotMinMs
	}
	// Issue #475: per-app eviction tier. Same shape as the
	// warm-snapshot entries above — only fields the caller touched
	// appear in the audit row, so a no-op PATCH (rare, but legal
	// for client-side retry idempotency) doesn't pollute the
	// audit stream.
	if req.EvictionPriority != nil {
		oldApp["eviction_priority"] = app.EvictionPriority
		newApp["eviction_priority"] = updated.EvictionPriority
	}
	// Issue #560: record what the customer altered on the
	// require_authn flag. Same shape as the warm-snapshot
	// entries above — only fields the caller touched appear
	// in the audit. The second-event row
	// (app.authn_disabled) below handles the true → false
	// transition as a single-purpose greppable signal, same
	// shape as app.warm_snapshot_disabled (PR #525 /
	// ADR-074).
	if req.RequireAuthn != nil {
		oldApp["require_authn"] = app.RequireAuthn
		newApp["require_authn"] = updated.RequireAuthn
	}
	// Issue #477 / ADR-079: record the public_auth mode
	// flip. Only the mode (not the credentials) is mirrored
	// to the audit row — `has_basic_creds: bool` would
	// double up the second-event row below, so the
	// structured entry here is mode-only. The plaintext /
	// sealed blob is NEVER recorded (re-redaction invariant).
	if req.PublicAuth != nil {
		oldApp["public_auth"] = app.PublicAuthMode
		newApp["public_auth"] = updated.PublicAuthMode
	}
	if req.EgressAllowlist != nil {
		oldApp["egress_allowlist"] = egressStringList(app.EgressAllowlist)
		newApp["egress_allowlist"] = egressStringList(updated.EgressAllowlist)
	}
	s.audit.Emit(r.Context(), "app.updated", &acct.ID, map[string]any{
		"app_id": updated.ID,
		"slug":   updated.Slug,
		"old":    oldApp,
		"new":    newApp,
	})
	// Issue #470 / PR C / ADR-074: emit a second audit row when the
	// warm-snapshot opt-in flips true → false. The app.updated
	// row already carries the old/new snapshot of warm_snapshot_
	// enabled; this row is a single-purpose, single-keyword-
	// greppable signal so operators can `gregale audit-events
	// --kind-prefix warm_snapshot` and see all three lifecycle
	// kinds (promoted/stale/disabled) in one stream. Not emitted
	// when the field was already false (no-op) or when the
	// operator left it unset (no intent to flip).
	if req.WarmSnapshotEnabled != nil && app.WarmSnapshotEnabled && !updated.WarmSnapshotEnabled {
		s.audit.Emit(r.Context(), "app.warm_snapshot_disabled", &acct.ID, map[string]any{
			"app_id": updated.ID,
			"slug":   updated.Slug,
			"old":    true,
			"new":    false,
		})
	}
	// Issue #475: emit a single-purpose audit row when the
	// per-app eviction tier changes. The app.updated row already
	// carries the old/new snapshot of eviction_priority; this row
	// is a single-purpose, single-keyword-greppable signal so
	// operators can `gregale audit-events --kind-prefix
	// eviction_priority` and see every tier change without parsing
	// the larger app.updated payload. Not emitted when the field
	// was already the same value (no-op PATCH) or when the
	// operator left it unset (no intent to flip).
	if req.EvictionPriority != nil && app.EvictionPriority != updated.EvictionPriority {
		s.audit.Emit(r.Context(), "app.eviction_priority_changed", &acct.ID, map[string]any{
			"app_id": updated.ID,
			"slug":   updated.Slug,
			"old":    app.EvictionPriority,
			"new":    updated.EvictionPriority,
		})
	}
	// Issue #560: emit app.authn_required / app.authn_disabled
	// on require_authn transitions. Same single-purpose
	// single-keyword-greppable shape as
	// app.warm_snapshot_disabled above, so operators can
	// `gregale audit-events --kind-prefix authn` and see the
	// full lifecycle. On flips in either direction. The
	// app.updated row already carries the old/new snapshot
	// of require_authn; this row is a single-purpose
	// signal. Not emitted when the field was already in
	// the target state (no-op transition) or when the
	// operator left it unset (no intent to flip).
	switch {
	case req.RequireAuthn != nil && !app.RequireAuthn && updated.RequireAuthn:
		s.audit.Emit(r.Context(), "app.authn_required", &acct.ID, map[string]any{
			"app_id": updated.ID,
			"slug":   updated.Slug,
			"old":    false,
			"new":    true,
		})
	case req.RequireAuthn != nil && app.RequireAuthn && !updated.RequireAuthn:
		s.audit.Emit(r.Context(), "app.authn_disabled", &acct.ID, map[string]any{
			"app_id": updated.ID,
			"slug":   updated.Slug,
			"old":    true,
			"new":    false,
		})
	}
	// Issue #477 / ADR-079: emit app.public_auth_changed on mode
	// transitions. Same single-purpose, single-keyword-greppable
	// shape as app.eviction_priority_changed above so operators
	// can `gregale audit-events --kind-prefix public_auth` and
	// see every mode flip without parsing the larger app.updated
	// payload. NOT emitted when the field was already in the
	// target state (no-op transition) or when the operator left
	// it unset (no intent to flip).
	//
	// Redaction posture (load-bearing — see ADR-079 §Decision
	// "re-redaction invariant"): the payload carries mode only
	// (open|bearer|basic) and a `has_basic_creds` bool flag.
	// Plaintext username / password / sealed blob are NEVER
	// recorded anywhere on the audit stream — neither this row
	// nor any future contributor adding logging in the
	// gatewayd-internal-side path. has_basic_creds answers "did the
	// customer rotate credentials on this PATCH?" without
	// revealing the value.
	if req.PublicAuth != nil && app.PublicAuthMode != updated.PublicAuthMode {
		s.audit.Emit(r.Context(), "app.public_auth_changed", &acct.ID, map[string]any{
			"app_id":          updated.ID,
			"slug":            updated.Slug,
			"old":             app.PublicAuthMode,
			"new":             updated.PublicAuthMode,
			"has_basic_creds": req.PublicAuth.Mode == api.AppPublicAuthModeBasic,
		})
	}
	resp := s.appResponse(updated, acct.Plan)
	writeJSON(w, http.StatusOK, s.withParkedDeploymentRef(r.Context(), resp, updated))
}

// deleteApp marks the app as deleted (soft delete; PG snapshot GC runs on the
// next successful deploy per spec §9).
func (s *server) deleteApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	// Move 2: GC pending invocations for this app BEFORE the row goes
	// away. Without this, a delayed_task can fire after deleteApp and
	// the drain is forced to log a permanent-wake error on a row the
	// customer has already given up on. CancelInvocation is a no-op on
	// terminal rows (returns state.ErrNotFound) so dispatching /
	// completed rows are untouched.
	pending, err := s.store.ListInvocationsForApp(r.Context(), app.ID,
		state.InvocationPending, state.InvocationDispatching)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list-inv"))
		return
	}
	for _, inv := range pending {
		if err := s.store.CancelInvocation(r.Context(), inv.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			// Don't fail the delete on a per-row cancel error; the
			// drain will surface the row as failed and the customer
			// sees it in the meter. Logging at warn so it's
			// observable.
			s.log.Warn("deleteApp: cancel invocation",
				"inv", inv.ID, "app", app.ID, "err", err)
		}
	}
	if err := s.store.DeleteApp(r.Context(), app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete app"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"deleted","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app deleted", "app", app.ID, "slug", app.Slug, "account", acct.ID)
	// IAM-4 (issue #291): record the soft delete. ADR-035 lists
	// `account.deletion_scheduled` / `account.deletion_restored`
	// for account-level churn; this is the per-app counterpart
	// (spec §9: row goes to AppDeleted, snapshot GC follows on
	// the next successful deploy). data carries the slug so the
	// audit row is searchable even after the row soft-deletes.
	s.audit.Emit(r.Context(), "app.deleted", &acct.ID, map[string]any{
		"app_id": app.ID,
		"slug":   app.Slug,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- deployments -----------------------------------------------------------

// getDeployment returns one deployment by id.
func (s *server) getDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(d))
}

// updateDeploymentMinInstances (issue #557 closure / ADR-074) is the
// PATCH /v1/deployments/{id} handler. The only mutable field on a
// deployment post-create is the cold-wake floor (min_instances);
// image / digest / overrides / sidecars stay immutable (a new
// deployment is the canonical way to change them).
//
// Validation:
//   - 404 on a missing deployment or a deployment that belongs to a
//     different account (IDOR-safe).
//   - 403 on a Free account — Free plans cannot set the per-deployment
//     min_instances floor. Plan tier first (same as the per-app gate
//     at validateUpdateApp) so a Free customer sees the plan error
//     rather than the value error. Pre-#557 / ADR-072 this branch
//     was missing: Free plans masked the bug accidentally because
//     `MaxMinInstances == 0` made `v > planMax` always true, but the
//     wrong error code (422 ErrMaxMinInstancesExceeded, "value") was
//     returned instead of the 403 "plan" code. Any future plan with
//     `MaxMinInstances > 0` and `MinInstancesAllowed = false` would
//     have opened a real bypass.
//   - 422 on a negative value or a value > plan.MaxMinInstances().
//     The plan cap applies to the EFFECTIVE floor per instance
//     (max(app, deployment)), not separately per axis — keeps the
//     Scale ceiling at 10 even when a customer pins both axes.
//   - 400 on a malformed body.
//
// Audit: emits a deployment.min_instances_changed row via the
// existing auditor (kind list frozen by ADR-074 §Decision 6).
func (s *server) updateDeploymentMinInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	var req api.UpdateDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.MinInstances == nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"min_instances required",
			"min_instances must be present (use 0 to inherit from the parent app)"))
		return
	}
	// Plan tier gate (issue #557 / ADR-072). Must run BEFORE the
	// bounds check so a Free account PATCHing min_instances=1 sees
	// the 403 plan_min_instances_not_allowed rather than the 422
	// max_min_instances_exceeded (the value is legal; the plan is
	// locked). Mirrors the per-app gate at validateUpdateApp:81-87.
	if !acct.Plan.MinInstancesAllowed() {
		api.WriteProblem(w, api.ErrPlanMinInstancesNotAllowed(acct.Plan))
		return
	}
	v := *req.MinInstances
	if v < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusUnprocessableEntity, api.CodeInvalidMinInstances,
			"min_instances must be >= 0",
			fmt.Sprintf("min_instances = %d; must be >= 0.", v)))
		return
	}
	planMax := planMaxFor(acct)
	if v > planMax {
		api.WriteProblem(w, api.ErrMaxMinInstancesExceeded(v, planMax))
		return
	}
	prev := d.MinInstances
	updated, err := s.store.UpdateDeploymentMinInstances(r.Context(), id, v)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "update failed", err.Error()))
		return
	}
	// Audit emit (issue #557 / ADR-072 §Decision 6). The kind
	// name is the same one the doc comment promised at the top of
	// this function; pre-#557 the emit was a doc-only contract
	// and no row was ever written. Operators correlate the bill
	// change with the PATCH by greppping for the kind. Best-effort:
	// a failure is logged but does not roll back the store write.
	if s.audit != nil {
		s.audit.Emit(r.Context(), "deployment.min_instances_changed", &acct.ID, map[string]any{
			"app":           app.ID,
			"deployment":    updated.ID,
			"min_instances": v,
			"prev":          prev,
		})
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(updated))
}

// updateDeploymentTraffic is the handler for
// PATCH /v1/deployments/{id}/traffic (issue #556 PR-A). The route
// is dedicated — NOT a sibling field on the existing
// /v1/deployments/{id} PATCH — because the request shape differs:
// TrafficPercent is mandatory (a no-field PATCH is meaningless on
// this route) and the pre-existing PATCH enforces min_instances
// presence. Splitting the routes keeps each handler's contract
// crisp and lets the gateway-side pg_notify payload differ without
// touching the min_instances path.
//
// Gate order (mirrors updateDeploymentMinInstances):
//  1. Resolve deployment + IDOR guard (deployment must belong to
//     this account).
//  2. Decode body; require traffic_percent in body.
//  3. Range check [0, 100] — 422 invalid_traffic_percent with
//     WithLimit(100, observed) so the CLI renders the cap.
//     Range-before-plan is intentional: a malformed value is loud
//     regardless of plan, and the plan gate only fires on a legal
//     value (so the operator sees the 403 "plan locked" not a 422
//     "value illegal").
//  4. Plan tier gate (Pro+ only) — 403 plan_traffic_split_not_allowed
//     for Hobby/Free.
//  5. Call store.UpdateDeploymentTraffic (atomic, with FOR UPDATE
//     lock on live rows + Σ = 100 invariant check via
//     RedistributeTraffic largest-remainder — issue #556 PR-C).
//  6. Audit emit deployment.traffic_percent_changed with
//     {app, deployment, traffic_percent, prev} payload.
//  7. pg_notify `deployment_changed` with kind="traffic" so the
//     PR-B gateway refresh subscriber (cmd/gatewayd-internal/
//     backend.go:298) reloads weights within ~1s. Pre-PR-C the
//     handler emitted no notify and the refresh was dead code on
//     this path.
//
// Audit: best-effort — a failure is logged but does not roll
// back the store write. Same contract as
// deployment.min_instances_changed above.
func (s *server) updateDeploymentTraffic(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	var req api.UpdateDeploymentTrafficRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// Body presence rule: this route's contract is "must carry
	// traffic_percent". A 0 is a legal value (operator wants the
	// row to receive no traffic — used during rollback) and is
	// distinct from "field omitted". Reject the omit case at 400
	// before the plan gate so a malformed body is loud.
	if req.TrafficPercent < 0 || req.TrafficPercent > 100 {
		// Range check first (no plan context needed). 422 with the
		// WithLimit(100, observed) shape so the CLI renders the
		// cap. Distinct from the plan gate's 403 below — a 200/403
		// split keeps the operator UX consistent (legal value, plan
		// locked → 403; illegal value → 422).
		api.WriteProblem(w, api.ErrInvalidTrafficPercent(req.TrafficPercent))
		return
	}
	// Plan tier gate (issue #556). Pro + Scale only. Hobby is
	// locked: the canary-rollout audience is more expensive
	// (RAM-billable per-running-second for two deployments) than
	// Hobby's "near-Free with a floor" value-prop.
	if !acct.Plan.TrafficSplitAllowed() {
		api.WriteProblem(w, api.ErrPlanTrafficSplitNotAllowed(acct.Plan))
		return
	}
	prev := d.TrafficPercent
	updated, err := s.store.UpdateDeploymentTraffic(r.Context(), id, req.TrafficPercent)
	if err != nil {
		switch {
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such deployment")
		case errors.Is(err, state.ErrInvalidTrafficPercent):
			// Store-layer backstop tripped (e.g. target row was
			// not 'live' at the moment of stamp). Translate to
			// the canonical 422 shape.
			api.WriteProblem(w, api.ErrInvalidTrafficPercent(req.TrafficPercent))
		case errors.Is(err, state.ErrTrafficPercentSumInvalid):
			// Defensive 409 — unreachable in the live-siblings case
			// (largest-remainder redistribution is Σ=100 by
			// construction), but reachable when the caller asks
			// for target=0 on a sole live row (legitimate Σ=0 —
			// pinned by TestPg_UpdateDeploymentTraffic_SoleLiveRow).
			api.WriteProblem(w, api.ErrTrafficPercentSumInvalid(0))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeInternal, "update failed", err.Error()))
		}
		return
	}
	if s.audit != nil {
		s.audit.Emit(r.Context(), "deployment.traffic_percent_changed", &acct.ID, map[string]any{
			"app":             app.ID,
			"deployment":      updated.ID,
			"traffic_percent": req.TrafficPercent,
			"prev":            prev,
		})
	}
	// Notify the gateway so PR-B's refresh subscriber reloads
	// weights within ~1s. Pre-PR-C (issue #556 PR-A) the handler
	// emitted no notify and `faas traffic set` was silently a no-op
	// for the running gateway until an unrelated `deployment_changed`
	// fired (new deploy, supersede, rollback). The kind="traffic"
	// discriminant lets the subscriber (and any future audit
	// pipeline) distinguish a weight change from a status change;
	// the existing subscriber ignores the field, so this is purely
	// additive. Failure is logged-and-continued — same contract as
	// the audit emit above.
	if s.notif != nil {
		if err := s.notif.Notify(r.Context(), db.NotifyDeploymentChanged,
			fmt.Sprintf(`{"kind":"traffic","app_id":"%s","deployment_id":"%s","traffic_percent":%d}`,
				app.ID, updated.ID, req.TrafficPercent)); err != nil {
			s.log.Warn("apid: notify deployment_changed (traffic) failed", "err", err)
		}
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(updated))
}

// planMaxFor resolves the per-plan MaxMinInstances cap for the
// account. Falls back to 0 for unknown plans (fail-closed — Free
// plan cannot configure any floor).
func planMaxFor(acct state.Account) int {
	return acct.Plan.MaxMinInstances()
}

// rollbackApp re-primes the most recent superseded deployment per spec §9.
// Implemented as a synchronous status swap; imaged/schedd react via
// pg_notify and re-prime on their side. The previous "live" deployment is
// marked superseded; the rolled-back one moves from superseded → live.
func (s *server) rollbackApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	current, err := s.store.LatestDeployment(r.Context(), app.ID)
	if err != nil {
		s.notFound(w, "no deployments")
		return
	}
	target, err := s.store.LatestSupersededDeployment(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrNoRollbackTarget())
		return
	}
	if err := s.store.MarkDeploymentSuperseded(r.Context(), current.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not supersede current"))
		return
	}
	if err := s.store.MarkDeploymentLive(r.Context(), target.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not activate rollback target"))
		return
	}
	// Re-read target so the response carries post-promotion status=Live,
	// not the pre-promotion Superseded we snapshotted into the local
	// struct above. Listeners downstream branch on this field — fix
	// surfaced by PR #117 review (finding F3).
	fresh, err := s.store.DeploymentByID(r.Context(), target.ID)
	if err == nil {
		target = fresh
	}
	// F-03: rollback emit now carries status="live" (the freshly-restored
	// deployment is live) and a deployment_id for listeners that switch on
	// the field. imaged's handleDeployment ignores this emit (the rollback
	// target already has a prepared ext4 + snap from the prior supersede),
	// but the symmetry lets future listeners branch on status without
	// a decode change.
	_ = s.notif.Notify(r.Context(), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"rollback","status":"live","app_id":"%s","deployment_id":"%s","from":"%s","to":"%s"}`,
			app.ID, target.ID, current.ID, target.ID))
	// F-03: emit the supersede transition for the deployment being
	// retired. Prior code did not announce the supersede at all, so imaged's
	// (F5) cleanupDeploymentFiles(p.To, true /* keepSnap */) branch never
	// fired. status="superseded" makes the transition observable.
	_ = s.notif.Notify(r.Context(), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"superseded","status":"superseded","app_id":"%s","deployment_id":"%s","to":"%s"}`,
			app.ID, current.ID, current.ID))
	s.log.Info("app rolled back", "app", app.ID, "from", current.ID, "to", target.ID, "account", acct.ID)
	// IAM-4 (issue #291): record the rollback so an operator can
	// answer "when did this app get rolled back, and to which
	// deployment?" without joining the gdpr ledger. data.from
	// is the deployment_id just superseded; data.to is the
	// deployment_id promoted to live. The pg_notify emit above
	// (lines 460+) carries the same ids for the live-system
	// listener; the audit row is the read-only counterpart.
	s.audit.Emit(r.Context(), "app.rolled_back", &acct.ID, map[string]any{
		"app_id": app.ID,
		"from":   current.ID,
		"to":     target.ID,
	})
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(target))
}

// parkApp marks the app evicted_cold; schedd reacts and tears down live
// instances.
func (s *server) parkApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st := state.AppEvictedCold
	if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not park app"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"parked","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app parked", "app", app.ID, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// wakeApp unparks an evicted_cold app.
func (s *server) wakeApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	st := state.AppActive
	if _, err := s.store.UpdateApp(r.Context(), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not wake app"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"woken","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app woken", "app", app.ID, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// renameApp swaps an app's slug atomically (issue #63). Body is
// {"new_slug": "<slug>"}; the handler validates the new slug with the
// same validSlug regex CreateApp uses, then delegates to
// Store.RenameApp. The unique-slug constraint (Postgres) and MemStore's
// in-memory scan both surface collisions as state.ErrConflict; the
// handler maps that to 409 CodeAppRenameFailed so the CLI can render
// an actionable error.
//
// Validates oldSlug ownership via loadApp (returns 404 on unknown app,
// 403 on cross-account access — same as every other handler in this
// file).
func (s *server) renameApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	oldSlug := r.PathValue("slug")
	app, ok := s.loadApp(w, r, acct, oldSlug)
	if !ok {
		return
	}
	var req api.RenameAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", err.Error()))
		return
	}
	if !validSlug(req.NewSlug) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug",
			"slug must be 3-40 chars, lowercase letters, digits, and hyphens"))
		return
	}
	if req.NewSlug == oldSlug {
		// Idempotent no-op: skip the DB round-trip and return the
		// current app shape so retries don't 4xx.
		resp := s.appResponse(app, acct.Plan)
		writeJSON(w, http.StatusOK, s.withParkedDeploymentRef(r.Context(), resp, app))
		return
	}
	updated, err := s.store.RenameApp(r.Context(), acct.ID, oldSlug, req.NewSlug)
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeAppRenameFailed,
				"Slug taken",
				fmt.Sprintf("another app already uses slug %q", req.NewSlug)))
			return
		}
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
				"App not found", "no app with the given slug exists"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not rename app"))
		return
	}
	// F-04: renamed emit now carries app_id. The old appsRoot/<oldSlug>/
	// directory becomes orphan (renamed app is the new slug); the cleanup
	// is left to imaged's GC, which removes stale snapshot rows and their
	// files. We do NOT scrub the old slug directory on rename — that
	// race-conditions with concurrent deploys that still reference the
	// old slug in their deployment.app_id-to-slug lookup.
	_ = s.notif.Notify(r.Context(), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"renamed","app_id":"%s","from":%q,"to":%q}`, app.ID, oldSlug, req.NewSlug))
	// CodeQL go/log-injection (CWE-117): oldSlug came from the
	// apps.slug column (regex-validated at create) and req.NewSlug
	// passed the same validSlug check on this request's body. Wrap
	// both so a future relax of validSlug (or a hostile migration)
	// cannot smuggle CR/LF into the audit line.
	s.log.Info("app renamed", "app", updated.ID, "from", logsanitize.Field(oldSlug), "to", logsanitize.Field(req.NewSlug), "account", acct.ID)
	resp := s.appResponse(updated, acct.Plan)
	writeJSON(w, http.StatusOK, s.withParkedDeploymentRef(r.Context(), resp, updated))
}

// --- instances -------------------------------------------------------------

func (s *server) listInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	instances, err := s.store.ListInstancesForApp(r.Context(), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list instances"))
		return
	}
	out := make([]api.InstanceResponse, 0, len(instances))
	for _, ins := range instances {
		out = append(out, instanceResponse(ins, app.EffectiveMinInstances()))
	}
	writeJSON(w, http.StatusOK, out)
}

// --- custom domains --------------------------------------------------------

func (s *server) createDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateCustomDomainRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Domain == "" || req.AppID == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", "domain and app_id are required"))
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	token := randomToken(16)
	d, err := s.store.CreateCustomDomain(r.Context(), strings.ToLower(req.Domain), app.ID, token)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Domain taken", err.Error()))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyDomainChanged, `{"kind":"created","domain":"`+d.Domain+`"}`)
	// d.Domain came in via the HTTP body (bearer-token authenticated).
	// Sanitize at the log sink — CodeQL go/log-injection (CWE-117).
	// The notify payload above is JSON-encoded so the pg_notify channel
	// can't be tricked into parsing an attacker-supplied structure, but
	// the structured log line is the unencoded sink.
	s.log.Info("domain created", "domain", logsanitize.Field(d.Domain), "app", app.ID, "account", acct.ID)
	// IAM-4 (issue #291): record the domain attachment. data.domain
	// is the lowercased canonical form already stored on the row, so
	// the audit row and the row stay in sync — a dashboard that
	// joins events.domain with domains.domain gets no surprises.
	s.audit.Emit(r.Context(), "domain.added", &acct.ID, map[string]any{
		"app_id": d.AppID,
		"domain": d.Domain,
	})
	writeJSON(w, http.StatusAccepted, domainResponse(d))
}

func (s *server) listDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domains, err := s.store.ListDomainsForAccount(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list domains"))
		return
	}
	out := make([]api.CustomDomainResponse, 0, len(domains))
	for _, d := range domains {
		out = append(out, domainResponse(d))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domain := strings.ToLower(r.PathValue("domain"))
	d, err := s.store.DomainByName(r.Context(), domain)
	if err != nil {
		s.notFound(w, "no such domain")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such domain")
		return
	}
	if err := s.store.DeleteCustomDomain(r.Context(), domain); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete domain"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyDomainChanged, `{"kind":"deleted","domain":"`+domain+`"}`)
	s.log.Info("domain deleted", "domain", domain, "account", acct.ID)
	// IAM-4 (issue #291): record the domain detachment. Symmetric
	// to domain.added; like the cron family, the pair is what an
	// operator queries ("when did this domain get attached and
	// later detached?"). data carries the low-cased canonical
	// form for the same dashboard-join reason.
	s.audit.Emit(r.Context(), "domain.removed", &acct.ID, map[string]any{
		"app_id": d.AppID,
		"domain": domain,
	})
	w.WriteHeader(http.StatusNoContent)
}

// --- crons -----------------------------------------------------------------

func (s *server) createCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateCronRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if !validCron(req.Schedule) {
		api.WriteProblem(w, api.ErrCronInvalid("expected 5-field cron expression (m h dom mon dow)"))
		return
	}
	// Plan-tier gate (spec §4.4 / paid-only event-shaped primitives).
	// Fires BEFORE AppByID so a Free customer gets a clean 402 rather
	// than a 404 (no app can be theirs anyway, but the wire shape
	// matters: the 402 carries the upgrade-to-Hobby copy the dashboard
	// renders). The store-level check still reads CronLimitPerApp==0
	// as a fail-closed defence in depth.
	//
	// Use LimitsFor (not MustLimitsFor) so an unknown acct.Plan — a
	// future tier that hasn't been added to pkg/api/limits.go yet, or
	// a migration that wrote a stale plan value — surfaces as a clean
	// 402 rather than a process panic → 500. Fail-closed: an unknown
	// plan is treated as if crons weren't unlocked.
	limits, ok := api.LimitsFor(acct.Plan)
	if !ok || limits.CronLimitPerApp == 0 {
		api.WriteProblem(w, api.ErrPlanCronsNotAllowed(acct.Plan))
		return
	}
	app, err := s.store.AppByID(r.Context(), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	c, err := s.store.CreateCronIfUnderQuota(r.Context(), req.AppID, req.Schedule, path, enabled, limits)
	if err != nil {
		var qe *state.CronQuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrPlanCronQuota(acct.Plan, string(qe.Scope), qe.Limit, qe.Observed))
		case errors.Is(err, state.ErrNotFound):
			s.notFound(w, "no such app")
		default:
			api.WriteProblem(w, api.ErrCapacity("could not create cron"))
		}
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"created","app_id":"`+app.ID+`"}`)
	s.log.Info("cron created", "cron", c.ID, "app", app.ID, "account", acct.ID)
	// IAM-4 (issue #291): record the cron schedule so a stolen CI
	// token that adds a backend beacon is observable. Sits AFTER
	// the PR #340 plan-tier gate (lines above); a Free customer
	// gets a 402 and never reaches this line, so no audit row is
	// emitted for the rejected attempt.
	s.audit.Emit(r.Context(), "cron.created", &acct.ID, map[string]any{
		"cron_id":  c.ID,
		"app_id":   c.AppID,
		"schedule": c.Schedule,
		"path":     c.Path,
		"enabled":  c.Enabled,
	})
	writeJSON(w, http.StatusCreated, cronResponse(c))
}

func (s *server) listCrons(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// List every cron owned by any of this account's apps.
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list crons"))
		return
	}
	out := make([]api.CronResponse, 0)
	for _, app := range apps {
		cs, err := s.store.ListCronsForApp(r.Context(), app.ID)
		if err != nil {
			continue
		}
		for _, c := range cs {
			out = append(out, cronResponse(c))
		}
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) updateCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	var req api.UpdateCronRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if req.Schedule != nil && !validCron(*req.Schedule) {
		api.WriteProblem(w, api.ErrCronInvalid("expected 5-field cron expression"))
		return
	}
	c, err := s.store.CronByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	updated, err := s.store.UpdateCron(r.Context(), id, req.Schedule, req.Path, req.Enabled, nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update cron"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"updated","cron":"`+id+`"}`)
	// IAM-4 (issue #291): record what changed and to what. We
	// capture only the fields the caller actually sent (req.X != nil)
	// so a schedule-only patch does NOT carry a `path` key on either
	// side — the row answers "what did the customer alter on this
	// cron, with what value" without re-derivation from logs.
	oldCron := map[string]any{}
	newCron := map[string]any{}
	if req.Schedule != nil {
		oldCron["schedule"] = c.Schedule
		newCron["schedule"] = updated.Schedule
	}
	if req.Path != nil {
		oldCron["path"] = c.Path
		newCron["path"] = updated.Path
	}
	if req.Enabled != nil {
		oldCron["enabled"] = c.Enabled
		newCron["enabled"] = updated.Enabled
	}
	s.audit.Emit(r.Context(), "cron.updated", &acct.ID, map[string]any{
		"cron_id": updated.ID,
		"app_id":  updated.AppID,
		"old":     oldCron,
		"new":     newCron,
	})
	writeJSON(w, http.StatusOK, cronResponse(updated))
}

func (s *server) deleteCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	c, err := s.store.CronByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	if err := s.store.DeleteCron(r.Context(), id, c.AppID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete cron"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"deleted","cron":"`+id+`"}`)
	// IAM-4 (issue #291): record the cron removal so a teammate
	// removing a customer's alarm is observable in the audit feed.
	// Symmetric to cron.created; ADR-035's `key.*` / `secret.*`
	// already pair .created with .deleted, so this closes the
	// surface for the cron family.
	s.audit.Emit(r.Context(), "cron.deleted", &acct.ID, map[string]any{
		"cron_id": c.ID,
		"app_id":  c.AppID,
	})
	w.WriteHeader(http.StatusNoContent)
}

// listCronRuns is the per-cron execution history (issue #791):
// GET /v1/crons/{id}/runs.
//
// Answers "did my last N fires work, and how long did they take" —
// the question /v1/invocations cannot express, because it has no
// cron_id filter and no server-side duration.
//
// Ownership uses the same two-step as updateCron/deleteCron: resolve
// the cron, then resolve its app and compare account ids. Both
// failure branches emit the identical "no such cron" 404 so a probe
// cannot distinguish "wrong id" from "someone else's cron".
func (s *server) listCronRuns(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	c, err := s.store.CronByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(r.Context(), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	limit, perr := parseCronRunsLimit(r)
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	rows, err := s.store.ListCronRunsForCron(r.Context(), id, limit, r.URL.Query().Get("before"))
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list cron runs"))
		return
	}
	runs := make([]api.CronRun, 0, len(rows))
	for _, inv := range rows {
		runs = append(runs, cronRunFromInvocation(inv))
	}
	writeJSON(w, http.StatusOK, api.ListCronRunsResponse{Runs: runs})
}

// cronRunFromInvocation projects an invocations row onto the narrow
// CronRun wire shape. Two translations matter:
//
//   - duration is computed here rather than by the client, so every
//     consumer agrees on the definition (completed_at - created_at,
//     i.e. enqueue-to-terminal, which includes wake time).
//   - a NULL outcome means the row is non-terminal, which the wire
//     reports as "running" rather than an empty string.
func cronRunFromInvocation(inv state.Invocation) api.CronRun {
	run := api.CronRun{
		ID:          inv.ID,
		StartedAt:   inv.CreatedAt,
		CompletedAt: inv.CompletedAt,
		Attempts:    inv.Attempts,
		InstanceID:  inv.InstanceID,
		Error:       inv.LastError,
		Outcome:     api.CronRunRunning,
	}
	if inv.Outcome != nil {
		run.Outcome = api.CronRunOutcome(*inv.Outcome)
	}
	if inv.CompletedAt != nil {
		ms := inv.CompletedAt.Sub(inv.CreatedAt).Milliseconds()
		// Clamp: a clock step between the insert default and the
		// terminal update can otherwise surface a negative duration.
		if ms < 0 {
			ms = 0
		}
		run.DurationMs = &ms
	}
	return run
}

// --- api keys --------------------------------------------------------------

func (s *server) createKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateKeyRequest
	_ = decodeJSON(r, &req)
	scopes, err := api.NormalizeCreateKeyScopes(req.Scopes)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid scopes", err.Error()))
		return
	}
	// IAM-5 (issue #189): cap check before mint. CountAPIKeys
	// excludes status='revoked' so the customer's history (revoked
	// keys left in the table for audit lineage) doesn't pin them
	// out of quota. rotateKey is quota-neutral and is always
	// permitted at the cap (the old key retired just before the
	// new one is minted — net 0).
	limits := api.MustLimitsFor(acct.Plan)
	if cur, cerr := s.store.CountAPIKeys(r.Context(), acct.ID); cerr == nil && cur >= limits.KeysMax {
		api.WriteProblem(w, api.ErrAPIKeyLimitExceeded(limits, cur))
		return
	} else if cerr != nil {
		api.WriteProblem(w, api.ErrCapacity("could not count keys"))
		return
	}
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	// IAM-5: expiry policy. Non-admin scopes get a 365-day
	// expires_at (spec: "set-and-forget" CI rotation, plus a
	// bounded exposure window for exfiltrated keys). Admin keys
	// default to nil expiry (legacy admin semantics — never
	// expire, must be explicitly revoked). The customer can
	// rotate an admin key with grace=0 to opt into finite expiry.
	var expiresAt *time.Time
	if !slices.Contains(scopes, api.ScopeAdmin) {
		t := time.Now().UTC().Add(time.Duration(api.DefaultAPIKeyLifetimeDays) * 24 * time.Hour)
		expiresAt = &t
	}
	// PR 6 (issue #190 / IAM-6) dual-write: the legacy /v1/keys
	// POST persists the key against the caller's personal org
	// so api_keys.org_id is populated (the 00127 NOT NULL flip).
	// When the personal-org backfill (PR 3) hasn't run yet for
	// this account (very old test fixtures — PR 3 is a soft
	// migration, not a hard schema flip), fall back to the
	// legacy CreateAPIKeyWithExpiry so the customer is never
	// locked out of their own key mint. ErrNotFound is the
	// expected signal; any other error is a 500.
	//
	// IAM hardening mega-PR (logical change 2): stamp the
	// provenance columns (created_ip, created_ua) on the new row
	// so a SOC 2 auditor can answer "who minted this key from
	// which UA" without joining through Loki. parent_key_id is
	// nil for first-mints (the FK is reserved for an explicit
	// lineage path; rotation already uses rotated_from_id).
	bindIP := clientIPFromRequest(r)
	bindUA := logsanitize.Field(r.UserAgent())
	org, perr := s.store.OrgByPersonalAccount(r.Context(), acct.ID)
	var k state.APIKey
	switch {
	case perr == nil:
		k, err = s.store.CreateOrgAPIKeyWithProvenance(r.Context(), org.ID, acct.ID, hash, req.Label, scopes, expiresAt, bindIP, bindUA, nil)
	case errors.Is(perr, state.ErrNotFound):
		// Legacy fallback path (pre-00127 fixtures). The
		// 5-arg CreateAPIKeyWithExpiry is the production
		// handler we have here; the provenance variant
		// stamps the same audit-relevant columns so the
		// fallback path is the same shape as the org-bound
		// path.
		k, err = s.store.CreateAPIKeyWithExpiryAndProvenance(r.Context(), acct.ID, hash, req.Label, scopes, expiresAt, bindIP, bindUA, nil)
	default:
		api.WriteProblem(w, api.ErrCapacity("could not resolve personal org"))
		return
	}
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create key"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"created","account":"`+acct.ID+`"}`)
	s.log.Info("key created", "key", k.ID, "account", acct.ID)
	// IAM-4 (ADR-035): record the key mint. subject = account_id (the
	// owner); data.scopes is the per-key permission set so the
	// audit row can answer "who minted which scopes today?".
	//
	// PR 6 dual-emit: the legacy `key.created` event stays byte-
	// identical (schema break would trip legacy dashboards) and
	// the new `api_key.created` event carries the org_id stamp
	// so PR 5+ dashboards can group by org. PR 9 drops the
	// legacy event.
	auditPayload := map[string]any{
		"key_id":     k.ID,
		"scopes":     scopes,
		"created_ip": bindIP,
		"created_ua": bindUA,
	}
	if k.ExpiresAt != nil {
		auditPayload["expires_at"] = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	s.audit.Emit(r.Context(), "key.created", &acct.ID, auditPayload)
	if perr == nil {
		newPayload := map[string]any{
			"key_id":     k.ID,
			"scopes":     scopes,
			"org_id":     k.OrgID,
			"created_ip": bindIP,
			"created_ua": bindUA,
		}
		if k.ExpiresAt != nil {
			newPayload["expires_at"] = k.ExpiresAt.UTC().Format(time.RFC3339)
		}
		s.audit.Emit(r.Context(), "api_key.created", &acct.ID, newPayload)
	}
	resp := api.APIKeyResponse{
		ID:        k.ID,
		OrgID:     k.OrgID,
		Prefix:    keyPrefix(plaintext),
		Label:     k.Label,
		Scopes:    k.Scopes,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		Status:    k.Status,
	}
	if k.ExpiresAt != nil {
		resp.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339)
	}
	resp.Plaintext = plaintext
	writeJSON(w, http.StatusCreated, resp)
}

func (s *server) listKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	keys, err := s.store.ListAPIKeys(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list keys"))
		return
	}
	out := make([]api.APIKeyResponse, 0, len(keys))
	for _, k := range keys {
		resp := api.APIKeyResponse{
			ID:        k.ID,
			Prefix:    keyPrefixFromHash(k.Hash),
			Label:     k.Label,
			Scopes:    k.Scopes,
			CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
			Status:    k.Status,
		}
		if !k.LastUsedAt.IsZero() {
			resp.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		if k.ExpiresAt != nil {
			resp.ExpiresAt = k.ExpiresAt.UTC().Format(time.RFC3339)
		}
		if k.RevokedAt != nil {
			resp.RevokedAt = k.RevokedAt.UTC().Format(time.RFC3339)
		}
		if k.RotatedFromID != nil {
			resp.RotatedFromID = *k.RotatedFromID
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	// IAM-5 (issue #189): DELETE is now a soft revoke. The row
	// stays in the table for audit lineage (rotated_from_id chain
	// preserves the predecessor's id; revoced_at marks the kill).
	// Repeated DELETE on a revoked key is idempotent — MarkAPIKeyRevoked
	// is a "update if not revoked" and returns the row either way.
	updated, err := s.store.MarkAPIKeyRevoked(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such key")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not revoke key"))
		return
	}
	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"revoked","account":"`+acct.ID+`"}`)
	// IAM-4 + IAM-1 (ADR-034 rev2 / ADR-035): record the key
	// revocation carrying the dismissed scopes so an operator can
	// answer "what did this key allow before it died?" without
	// re-deriving it from logs. The `reason` field is "manual"
	// (this path) vs "rotation" (rotateKey) vs "expired" (lazy
	// auth-time gate). Dashboard filters by reason.
	s.audit.Emit(r.Context(), "key.revoked", &acct.ID, map[string]any{
		"key_id": updated.ID,
		"scopes": updated.Scopes,
		"reason": "manual",
	})
	w.WriteHeader(http.StatusNoContent)
}

// rotateKey mints a new key and demotes the old key in a single
// transaction (issue #189 / IAM-5). The new key inherits the
// old key's label + scopes so the rotation is a no-op for the
// customer's CI scripts. The old key's expires_at is overwritten
// to the grace deadline (now() + graceWindow), so a customer
// who wants to schedule the kill has a hard deadline instead of
// an open-ended "until someone deletes the row".
//
// Quota: rotation is quota-neutral (-1 +1 = 0 net). The cap
// check happens BEFORE the rotation so a customer ALREADY at the
// cap can still rotate (the old key retires just before the new
// one is minted). The CreateAPIKey cap check (Plan.KeysMax) does
// not run on the rotation path.
//
// Audit: key.rotated carries the linkage
// ({old_key_id, new_key_id, grace_window_days, old_key_expires_at}).
// When the rotation is non-atomic (graceWindow > 0), we do NOT
// also emit key.revoked — the old key is in 'grace', not
// 'revoked', and the audit row would mislead. When the rotation
// is atomic (graceWindow == 0), the underlying UPDATE flips
// status='revoked' directly; the key.rotated event is the
// only audit row the customer sees, and it carries the
// equivalent information.
//
// Response: {key, key_plaintext, old_key_id, old_key_expires_at}.
// The old plaintext is never returned — we only store the hash.
func (s *server) rotateKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")

	// Resolve grace window. Per-account override wins; nil = use
	// the plan default (api.DefaultAPIKeyGraceWindowDays = 7). The
	// cache keeps the per-rotation handler off a hot PG path;
	// admin updates invalidate the cache and propagate on the
	// next rotation.
	var graceWindow time.Duration
	gw, err := s.resolveGraceWindow(r.Context(), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not resolve grace window"))
		return
	}
	if gw == nil {
		graceWindow = time.Duration(api.DefaultAPIKeyGraceWindowDays) * 24 * time.Hour
	} else {
		graceWindow = time.Duration(*gw) * 24 * time.Hour
	}

	// Mint the new plaintext + hash BEFORE the rotation so the
	// store op can persist the real hash. The handler is the only
	// site that ever sees the plaintext in memory.
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}

	// PR 6 (issue #190 / IAM-6) dual-write: discover the old key's
	// org_id before rotating so the RotateOrgAPIKey call can pin
	// the (id, org_id) lock predicate. GetAPIKey collapses
	// cross-account reads to ErrNotFound at the SQL level — the
	// same IDOR-safe shape the legacy MarkAPIKeyRevoked uses.
	oldKey, err := s.store.GetAPIKey(r.Context(), acct.ID, id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such key")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not load key"))
		return
	}

	// Inherit the old label so the customer's CI config doesn't
	// need to chase a label change. The store layer reads the
	// old row's label in the transaction; the empty string tells
	// the store to use the old row's value as-is.
	//
	// IAM hardening mega-PR (logical change 2): stamp the
	// provenance columns on the new row. parent_key_id is the
	// explicit FK to the predecessor key — distinct from the
	// rotation-internal rotated_from_id column, but for
	// rotations both point to the same predecessor. This makes
	// the rotation lineage queryable from two angles: the
	// fast rotated_from_id index (the rotation-internal stamp)
	// and the parent_key_id FK (the provenance lineage).
	bindIP := clientIPFromRequest(r)
	bindUA := logsanitize.Field(r.UserAgent())
	parentID := oldKey.ID
	newKey, oldKey, err := s.store.RotateOrgAPIKeyWithProvenance(r.Context(), oldKey.OrgID, id, hash, "", graceWindow, bindIP, bindUA, &parentID)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such key")
			return
		}
		if errors.Is(err, state.ErrAPIKeyRevoked) {
			s.notFound(w, "key already revoked")
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not rotate key"))
		return
	}

	_ = s.notif.Notify(r.Context(), db.NotifyKeyChanged, `{"kind":"rotated","account":"`+acct.ID+`"}`)
	var graceWindowDays int
	if graceWindow > 0 {
		graceWindowDays = int(graceWindow / (24 * time.Hour))
	}
	auditPayload := map[string]any{
		"old_key_id":         oldKey.ID,
		"new_key_id":         newKey.ID,
		"grace_window_days":  graceWindowDays,
		"old_key_expires_at": oldKey.ExpiresAt.UTC().Format(time.RFC3339),
		"created_ip":         bindIP,
		"created_ua":         bindUA,
		"parent_key_id":      oldKey.ID,
	}
	s.audit.Emit(r.Context(), "key.rotated", &acct.ID, auditPayload)
	// PR 6 dual-emit: the new `api_key.rotated` carries org_id
	// so the PR 5+ dashboard can group rotations by org. PR 9
	// drops the legacy event.
	s.audit.Emit(r.Context(), "api_key.rotated", &acct.ID, map[string]any{
		"old_key_id":         oldKey.ID,
		"new_key_id":         newKey.ID,
		"grace_window_days":  graceWindowDays,
		"old_key_expires_at": oldKey.ExpiresAt.UTC().Format(time.RFC3339),
		"org_id":             oldKey.OrgID,
		"created_ip":         bindIP,
		"created_ua":         bindUA,
		"parent_key_id":      oldKey.ID,
	})
	s.log.Info("key rotated",
		"old_key", oldKey.ID, "new_key", newKey.ID,
		"account", acct.ID, "grace_window_days", graceWindowDays)

	resp := api.RotateKeyResponse{
		Key: api.APIKeyResponse{
			ID:            newKey.ID,
			OrgID:         newKey.OrgID,
			Prefix:        keyPrefix(plaintext),
			Label:         newKey.Label,
			Scopes:        newKey.Scopes,
			CreatedAt:     newKey.CreatedAt.UTC().Format(time.RFC3339),
			Status:        newKey.Status,
			RotatedFromID: oldKey.ID,
		},
		KeyPlaintext: plaintext,
		OldKeyID:     oldKey.ID,
	}
	if oldKey.ExpiresAt != nil {
		resp.OldKeyExpiresAt = oldKey.ExpiresAt.UTC().Format(time.RFC3339)
	}
	writeJSON(w, http.StatusOK, resp)
}

// resolveGraceWindow reads the per-account grace override via the
// in-process cache, falling back to the store on a miss. The
// handler is the only caller; nil-safe when the cache is nil
// (legacy tests that pre-date the cache field).
func (s *server) resolveGraceWindow(ctx context.Context, accountID string) (*int, error) {
	if s.graceWindowCache == nil {
		return s.store.GetAccountKeyGraceWindow(ctx, accountID)
	}
	return s.graceWindowCache.resolveGraceWindow(ctx, s.store, accountID)
}

// --- usage -----------------------------------------------------------------

func (s *server) getUsage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	monthStr := r.URL.Query().Get("month")
	month, err := time.Parse("2006-01", orDefault(monthStr, time.Now().UTC().Format("2006-01")))
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad month", "expected YYYY-MM"))
		return
	}
	rows, err := s.store.UsageByMonth(r.Context(), acct.ID, month)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load usage"))
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	out := make([]api.UsageResponse, 0, len(rows))
	for _, u := range rows {
		out = append(out, api.UsageResponse{
			AppID:           u.AppID,
			MBSeconds:       u.MBSeconds,
			Requests:        u.Requests,
			IncludedGBHours: int64(limits.IncludedGBHours),
			// CPUUsageUsec is the informational per-app monthly
			// CPU-µs the meterd sampler accumulated in
			// usage_minutes.cpu_usec (issue #279 / PR-B). 0
			// when no CPU has been recorded yet (boot, or the
			// app has not been woken). Not billed.
			CPUUsageUsec: u.CPUUsec,
			// ADR-046 (step 10): per-app monthly egress
			// bytes — informational only, not billed.
			// The two columns are sourced independently
			// (TXBytes = gateway response bytes via
			// pkg/gateway statusRecorder;
			// NetTxBytes = root-side vethHost.rx_bytes
			// delta via vmmd netstats.Cache). The
			// gateway-side tx_bytes producer lands in
			// PR-2; both fields are 0 until then.
			TXBytes:    u.TXBytes,
			NetTxBytes: u.NetTxBytes,
			// ADR-048: ingress + cold-boot transition
			// count. Informational only — not billed.
			// Wire regen (PR-A commit #2 follow-up)
			// gates the live data path; today both stay
			// 0 from the meterd sampler.
			NetRxBytes:    u.NetRxBytes,
			ColdBootCount: u.ColdBootCount,
		})
	}
	writeJSON(w, http.StatusOK, out)
}

// --- plan changes ----------------------------------------------------------

// changePlan implements PATCH /v1/account/plan. Only the Free → Hobby
// upgrade is permitted via the dashboard in M5; everything else flows
// through Stripe (the webhook hits POST /v1/webhooks/stripe and calls
// into here with the network-trusted plan). M5 keeps this minimal —
// the table is wired, full dunning flow lands with M7 + meterd.
//
// Issue #142: also gate every paid upgrade on the account having a
// Stripe subscription item. Previously the handler accepted any valid
// plan from any authenticated bearer token, which let a Free account
// self-upgrade to Pro/Scale via API key alone — getting 1024 MB RAM,
// 25 deployments, 5 concurrent instances, with no Stripe subscription
// to invoice. meterd's quota tick skips customers with empty
// StripeSubscriptionItem so the overage was silently absorbed. The
// gate below 402s with a billing portal URL pointing at the Stripe
// checkout path; the Stripe webhook remains the legitimate way to
// land on a paid plan (it stamps StripeSubscriptionItem on the way
// through).
func (s *server) changePlan(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req struct {
		Plan string `json:"plan"`
	}
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	plan := api.Plan(req.Plan)
	if !plan.Valid() {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad plan", "plan must be free|hobby|pro|scale"))
		return
	}
	// Issue #142 gate: any paid upgrade that does not have a Stripe
	// subscription item on the account is blocked. Downgrades and
	// same-tier moves always pass; the free → hobby M5 path is the
	// only free → paid direct upgrade.
	if acct.Plan.RequiresStripeUpgradeTo(plan) && acct.StripeSubscriptionItem == "" {
		// CodeQL go/log-injection (CWE-117): plan was enum-validated
		// against the 4 Plan constants (free|hobby|pro|scale) by
		// plan.Valid() in this handler, but CodeQL's taint engine
		// doesn't model that branch. Wrap so a future relax of
		// plan.Valid() cannot smuggle CR/LF into the audit line.
		s.log.Info("plan change blocked",
			"account", acct.ID,
			"from", logsanitize.Field(string(acct.Plan)),
			"to", logsanitize.Field(string(plan)),
		)
		prob := &api.Problem{
			Status: http.StatusPaymentRequired,
			Code:   api.CodePayment,
			Title:  "Billing subscription required",
			// The "faas billing retry" hint matches the dunning
			// email copy at pkg/mail/account.go:107,150 so a
			// customer who lands here from a failed-charge path
			// (vs a clean free→paid path) sees the same command
			// the mailer told them to run. Issue #242.
			Detail: "plan upgrades to " + string(plan) + " require an active subscription; run `faas billing retry` to recover from a failed charge, or complete checkout to upgrade",
		}
		// Provider dispatch: if the active provider has a real upgrade
		// path (Paddle), call CreateUpgradeTransaction and surface the
		// hosted checkout URL + tx handle on the Problem. The Stripe
		// stub returns ("", "", nil) — apid reads txID == "" to fall
		// through to the precomputed FAAS_BILLING_PORTAL_URL template
		// (which the operator controls per environment).
		//
		// No-provider box (FAAS_BILLING_PROVIDER unset) and Stripe both
		// land here with the same template response, so the pre-PR-#3
		// Stripe path is bit-for-bit unchanged.
		//
		// Capabilities() is the primary dispatch signal (added in
		// PR-P1 of the pluggable-billing rollout). The txID == "" check
		// stays as a defensive fallback for any provider wired before
		// the capability introspection was introduced.
		if s.billingProvider != nil && s.billingProvider.Capabilities().Has(billing.CapHostedCheckout) {
			// PR-P3: Paddle's CreateUpgradeTransaction requires an
			// existing Paddle customer (ctm_…) to attach the
			// subscription to. Stripe's path does not need this
			// sidecar (cus_… is created lazily on the first
			// Stripe-side subscription POST), so the guard is
			// capability-scoped — only providers that opt into
			// CapHostedCheckout pay the extra round-trip.
			//
			// Idempotent: if acct.ProviderCustomerID is already set
			// the call is a no-op. A second changePlan request from
			// the same browser session reuses the existing ID and
			// does not POST a duplicate customer to Paddle.
			upgradeAcct := acct
			if upgradeAcct.ProviderCustomerID == "" {
				custID, cerr := s.billingProvider.CreateCustomer(r.Context(), acct)
				if cerr != nil {
					s.log.Error("create_customer",
						"account", acct.ID,
						"target_plan", logsanitize.Field(string(plan)),
						"err", cerr)
					api.WriteProblem(w, api.ErrCapacity("upgrade unavailable"))
					return
				}
				if err := s.store.UpdateAccountProviderCustomerID(r.Context(), acct.ID, custID); err != nil {
					// PR-P4 review finding #1: at this point a
					// Paddle customer has been created on Paddle's
					// side (custID is the ctm_… handle) but the DB
					// stamp failed — the customer is orphaned with
					// no row binding it to acct. On retry the
					// sidecar fires again because
					// upgradeAcct.ProviderCustomerID is still "",
					// creating a second orphan on Paddle's
					// dashboard.
					//
					// Compensating action (DELETE on
					// /customers/{custID} via Paddle's REST API)
					// is out of scope for this PR — it requires a
					// new paddle.Provider.ArchiveCustomer method +
					// a tx-scoped defer in this handler. Filed
					// for PR-P5. For now we surface the orphan via
					// a structured field so an operator can grep
					// `orphan_paddle_customer=true` and reconcile
					// by hand on the Paddle dashboard.
					s.log.Error("stamp_customer_id",
						"account", acct.ID,
						"customer_id", custID,
						"orphan_paddle_customer", true,
						"err", err)
					api.WriteProblem(w, api.ErrCapacity("upgrade unavailable"))
					return
				}
				upgradeAcct.ProviderCustomerID = custID
			}
			txID, checkoutURL, err := s.billingProvider.CreateUpgradeTransaction(r.Context(), upgradeAcct, plan)
			if err != nil {
				s.log.Error("create_upgrade_tx",
					"account", acct.ID,
					"target_plan", logsanitize.Field(string(plan)),
					"err", err)
				api.WriteProblem(w, api.ErrCapacity("upgrade unavailable"))
				return
			}
			if txID != "" {
				prob.PaddleCheckoutURL = checkoutURL
				prob.TxID = txID
				api.WriteProblem(w, prob)
				return
			}
		}
		prob.BillingPortalURL = s.billingPortalURLFor(acct)
		api.WriteProblem(w, prob)
		return
	}
	if err := s.store.UpdateAccountPlan(r.Context(), acct.ID, plan); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update plan"))
		return
	}
	updated, _ := s.store.AccountByID(r.Context(), acct.ID)
	// CodeQL go/log-injection (CWE-117): plan was enum-validated against
	// the 4 Plan constants (free|hobby|pro|scale) by plan.Valid() in this
	// handler — a bad value is rejected with 400 before reaching here,
	// but CodeQL's taint engine doesn't model that branch. Wrap so a
	// future relax of plan.Valid() cannot smuggle CR/LF into the audit
	// line.
	s.log.Info("plan changed", "account", acct.ID, "plan", logsanitize.Field(string(plan)))
	// IAM-4 (ADR-035): record the plan transition. data carries
	// the pre-change plan (acct.Plan) and post-change plan so the
	// audit row is self-describing — no need to walk the gdpr ledger
	// to find the prior state.
	s.audit.Emit(r.Context(), "account.plan_changed", &acct.ID, map[string]any{
		"from": string(acct.Plan),
		"to":   string(plan),
	})
	// IAM-2 (issue #186): plan-upgrade chokepoint. Crossing the
	// free|hobby → pro|scale boundary arms mfa_required so the
	// customer's next login flips to mfa_pending. Re-fetched
	// `updated` carries the post-change plan in case a future
	// change moves the live row state; mfaFlipOnUpgrade only
	// needs the old/new pair, which we still have here.
	if mfaFlipOnUpgrade(acct.Plan, plan) {
		s.flipMFARequiredIfUnenrolled(r.Context(), updated, "plan_upgrade", map[string]any{
			"from": string(acct.Plan),
			"to":   string(plan),
		})
	}
	writeJSON(w, http.StatusOK, api.AccountResponse{
		ID: updated.ID, Email: updated.Email, Plan: string(updated.Plan), Status: string(updated.Status),
	})
}

// --- spend cap raise (issue #561) --------------------------------------------

// raiseOverageCap implements POST /v1/account/overage-cap. Body:
// {"overage_cap_cents": <int|null>}; *int64 so a missing/null field
// clears the cap (NULL). Account-self-scoped (the bearer key's
// account; PATs and session cookies both reach here via withAuth).
// No per-plan allowlist — Free/Pro/Scale customers may set the cap;
// Free's effective cap=0 was a defensive default from #279 PR-A and
// raising to a positive value is harmless (the workload gate then
// refuses at that ceiling). Returns 200 + a fresh account view.
//
// The service routine raiseOverageCapSvc is the shared code path
// also called by the dashboard /dashboard/raise-overage-cap form so
// the two surfaces stay in lock-step (validation + storage + audit
// row are identical).
func (s *server) raiseOverageCap(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req struct {
		OverageCapCents *int64 `json:"overage_cap_cents"`
	}
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad request", err.Error()))
		return
	}
	if req.OverageCapCents != nil && *req.OverageCapCents < 0 {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"overage_cap_cents must be >= 0 or null", "negative values are rejected"))
		return
	}
	updated, err := s.raiseOverageCapSvc(r.Context(), acct, req.OverageCapCents)
	if err != nil {
		// PG write failure on a state mutation is a 500 (CodeInternal),
		// NOT a 503 ErrCapacity (which is reserved for schedd-side
		// admission back-pressure and would misroute alerts on the
		// gateway_* capacity dashboards). Review finding #1.
		api.WriteProblem(w, api.ErrInternal("could not update overage cap"))
		return
	}
	writeJSON(w, http.StatusOK, api.AccountResponse{
		ID: updated.ID, Email: updated.Email, Plan: string(updated.Plan), Status: string(updated.Status),
	})
}

// raiseOverageCapSvc is the shared validation-free mutation body used
// by both the v1 API endpoint (raiseOverageCap) and the dashboard
// form handler (handlers_dashboard.go). The cents argument is
// validated at the call site — this routine only stamps the row +
// emits the audit row. Returns the refreshed account view so the
// caller can render the post-update state without a second round-trip.
func (s *server) raiseOverageCapSvc(ctx context.Context, acct state.Account, cents *int64) (state.Account, error) {
	// Read old value for the audit row's "old_cents" field. If the
	// existing row is missing (test fixture or freshly-minted), the
	// old value reads back as (0, false) and we surface the actor as
	// "self" — the shape stays consistent for the audit reader.
	oldCents, _, _ := s.store.GetAccountOverageCapCents(ctx, acct.ID)
	if err := s.store.UpdateAccountOverageCapCents(ctx, acct.ID, cents); err != nil {
		return acct, err
	}
	updated, err := s.store.AccountByID(ctx, acct.ID)
	if err != nil {
		return acct, err
	}
	var newCents any
	if cents == nil {
		newCents = auditOverageCapNullSentinel
	} else {
		newCents = *cents
	}
	s.audit.Emit(ctx, "overage.cap_changed", &acct.ID, map[string]any{
		"old_cents": oldCents,
		"new_cents": newCents,
		"actor":     "self",
	})
	return updated, nil
}

// stripeWebhook accepts signed Stripe events. M7 enforces the v1 HMAC
// against s.stripeWebhookSecret (empty secret = verify disabled, dev
// only). It handles:
//
//   - customer.subscription.deleted → suspend the account
//   - invoice.payment_failed        → past_due (apps still serve, deploys blocked)
//   - invoice.payment_succeeded     → active (if the account was past_due)
//   - customer.subscription.updated with a plan → update plan
//
// Unknown event types return 200 with no side effect — Stripe expects
// 2xx for everything it didn't recognize so it doesn't retry forever.
// Returns 400 on bad payload / bad signature.
func (s *server) stripeWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	// Fail closed (security review A2): refuse to process events when
	// STRIPE_WEBHOOK_SECRET is unset. Previously the handler accepted
	// unsigned bodies, letting anyone suspend any account. The 503
	// tells the operator (via journal/log scrape) that the secret
	// needs to be provisioned; dev-mode callers should set
	// STRIPE_WEBHOOK_SECRET to a fixed test secret to reach the
	// handler's success path.
	if s.stripeWebhookSecret == "" {
		s.log.Error("stripe_webhook.no_secret",
			"err", "STRIPE_WEBHOOK_SECRET is unset; refusing to process events")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"stripe webhook not configured",
			"STRIPE_WEBHOOK_SECRET is unset; refusing to process events"))
		return
	}
	header := r.Header.Get("Stripe-Signature")
	if err := stripe.VerifySignature(body, header, s.stripeWebhookSecret, 5*time.Minute); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signature", err.Error()))
		return
	}
	var ev struct {
		ID   string `json:"id"`
		Type string `json:"type"`
		Data struct {
			Object struct {
				Customer string `json:"customer"`
				Status   string `json:"status"`
				Plan     string `json:"plan"`
			} `json:"object"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &ev); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	acct, err := s.lookupAccountByStripeID(r.Context(), ev.Data.Object.Customer)
	if err != nil {
		// Unknown customer: 200 so Stripe stops retrying. New customers
		// land via CreateCustomer; we don't auto-provision on a webhook.
		w.WriteHeader(http.StatusOK)
		return
	}
	// Issue #294: webhook replay dedupe. ev.ID is the Stripe
	// `event.id` (the delivery UUID). When non-empty, consult the
	// shared dedupe table; a redelivery within the 5-minute TTL is
	// rejected with 200 (idempotent — Stripe stops retrying) and
	// the audit row webhook.replay_rejected is emitted. Empty ev.ID
	// (older Stripe payloads) skips the check — pre-#294 behaviour.
	//
	// The replay check runs AFTER the customer lookup so the audit
	// row carries the resolved account id as the subject (matching
	// the refund.processed precedent at spec §5.7 line 336).
	//
	// Transport / connection errors from the dedupe table fail open
	// (log WARN + forward) — the dedupe is defence-in-depth, not the
	// authenticity gate; HMAC was verified above.
	if ev.ID != "" {
		if err := webhookdedupe.CheckReplay(r.Context(), webhookdedupe.ProviderStripe, ev.ID); err != nil {
			if webhookdedupe.IsReplay(err) {
				acctID := acct.ID
				s.audit.Emit(r.Context(), "webhook.replay_rejected", &acctID, map[string]any{
					"provider":    webhookdedupe.ProviderStripe,
					"delivery_id": logsanitize.Field(ev.ID),
				})
				w.WriteHeader(http.StatusOK)
				return
			}
			s.log.Warn("stripe replay-check infra error; forwarding", "event_id", logsanitize.Field(ev.ID), "err", err)
		}
	}
	// Map Stripe's event_type strings to the normalized billing.EventType
	// the dunning state machine dispatches on. The Paddle webhook (see
	// paddleWebhook) builds a billing.Event directly from the SDK's
	// VerifyWebhook return value, so both paths converge on
	// handleBillingEvent.
	normalized := billing.Event{
		Type:       mapStripeTypeToEventType(ev.Type),
		CustomerID: ev.Data.Object.Customer,
		PlanID:     ev.Data.Object.Plan,
		Raw:        body,
	}
	s.handleBillingEvent(r.Context(), normalized, acct)
	w.WriteHeader(http.StatusOK)
}

// billingWebhookTolerance returns the configured replay-protection
// window for the active billing provider. PR-P4 introduced the
// operator knob (FAAS_PADDLE_WEBHOOK_TOLERANCE_SECONDS, default 300s);
// pre-PR-P4 the value was a literal 5*time.Minute at the call site.
// For Stripe the existing stripe.Config.ToleranceSeconds path is
// preserved; for Paddle the loader's BuildAPID closure calls
// SetWebhookTolerance after construction, and this helper queries
// the provider. On a non-Paddle deployment the helper returns the
// default (the only consumer — paddleWebhook — has already gated on
// the provider name before calling VerifyWebhook).
func (s *server) billingWebhookTolerance() time.Duration {
	if p, ok := s.billingProvider.(interface {
		WebhookTolerance() time.Duration
	}); ok {
		return p.WebhookTolerance()
	}
	return 5 * time.Minute
}

// paddleWebhook accepts signed Paddle events. Mirrors stripeWebhook
// (cmd/apid/handlers_ext.go:672) but the signature verify + JSON parse
// happen inside s.billingProvider.VerifyWebhook so apid never sees
// Paddle-shaped wire format. The dunning state machine is provider-
// neutral — handleBillingEvent is the shared dispatch.
//
// Returns 503 if FAAS_BILLING_PROVIDER != paddle (a misrouted POST from
// Paddle to a Stripe-only box should be visible in logs, not silently
// 200'd). Returns 400 on bad payload / bad signature. Unknown event
// types return 200 so Paddle stops retrying.
func (s *server) paddleWebhook(w http.ResponseWriter, r *http.Request) {
	if s.billingProvider == nil {
		s.log.Error("paddle_webhook.no_provider",
			"err", "FAAS_BILLING_PROVIDER != paddle; refusing to process events")
		api.WriteProblem(w, api.NewProblem(http.StatusServiceUnavailable, api.CodeCapacity,
			"paddle webhook not configured",
			"FAAS_BILLING_PROVIDER != paddle; refusing to process events"))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad webhook", err.Error()))
		return
	}
	// The Paddle provider inspects the Paddle-Signature header inside
	// VerifyWebhook. Pass it as a one-entry map — the provider's
	// own keying is case-insensitive (paddle/provider.go:168-171
	// checks both casings).
	headers := map[string]string{
		"Paddle-Signature": r.Header.Get("Paddle-Signature"),
	}
	ev, err := s.billingProvider.VerifyWebhook(body, headers, s.billingWebhookTolerance())
	if err != nil {
		// PR-P4 — surface the verify failure to operators. Pre-PR-P4
		// the handler returned 400 with no log line, so an operator
		// debugging "wrong secret in the dashboard" had nothing to
		// grep for. The Counter is unlabelled — fleet-level signal,
		// not per-event detail (the per-event detail lives in the
		// audit row + the journal).
		s.log.Warn("paddle_webhook.verify_failed",
			"err", logsanitize.Field(err.Error()),
		)
		if s.ops != nil {
			s.ops.IncPaddleWebhookVerifyFailed()
		}
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signature", err.Error()))
		return
	}
	acct, err := s.lookupAccountByPaddleID(r.Context(), ev.CustomerID)
	if err != nil {
		// Unknown customer: 200 so Paddle stops retrying. New
		// customers land via CreateCustomer; we don't auto-provision
		// on a webhook.
		//
		// PR-P4 — surface the unknown-customer path to operators.
		// The customer ID is a `ctm_…` identifier, NOT a secret, so
		// no sanitiser is needed. The existing failure-mode table in
		// docs/ops/billing-provider-switch.md tells operators to go
		// grep for this exact line when a `transaction.paid` 200s
		// without a state flip — pre-PR-P4 that grep returned
		// nothing.
		s.log.Info("paddle_webhook.unknown_customer",
			"customer_id", ev.CustomerID,
			"event_type", int(ev.Type),
		)
		w.WriteHeader(http.StatusOK)
		return
	}
	// Issue #294: webhook replay dedupe. ev.EventID is the Paddle
	// `event_id` (the delivery UUID). Mirrors stripeWebhook's
	// replay check (handlers_ext.go:1148); a redelivery within the
	// 5-minute TTL is rejected with 200 + a webhook.replay_rejected
	// audit row. Empty ev.EventID (older Paddle payloads) skips the
	// check — pre-#294 behaviour. Fail-open on dedupe transport
	// errors (mirrors gatewayd-internal).
	if ev.EventID != "" {
		if err := webhookdedupe.CheckReplay(r.Context(), webhookdedupe.ProviderPaddle, ev.EventID); err != nil {
			if webhookdedupe.IsReplay(err) {
				acctID := acct.ID
				s.audit.Emit(r.Context(), "webhook.replay_rejected", &acctID, map[string]any{
					"provider":    webhookdedupe.ProviderPaddle,
					"delivery_id": logsanitize.Field(ev.EventID),
				})
				// PR-P4 — counter for the replay-suppressed branch.
				// Unlabelled (closed-vocabulary event_type is implied
				// by the audit row). Mirrors alertEvalFiredTotal
				// (pkg/wire/metrics.go:293). Helps operators
				// distinguish "Paddle is redelivering" from "Paddle
				// is sending brand-new events".
				if s.ops != nil {
					s.ops.IncPaddleWebhookReplaySuppressed()
				}
				w.WriteHeader(http.StatusOK)
				return
			}
			s.log.Warn("paddle replay-check infra error; forwarding", "event_id", logsanitize.Field(ev.EventID), "err", err)
		}
	}
	s.handleBillingEvent(r.Context(), ev, acct)
	w.WriteHeader(http.StatusOK)
}

// handleBillingEvent is the provider-neutral dunning state machine.
// Both stripeWebhook and paddleWebhook call it after their per-provider
// VerifyWebhook succeeds and the account is resolved. The four
// transitions (active / past_due / suspended / plan-change) and the
// associated dunning emails are the same for both providers — only
// the event source differs.
//
// ev.Type is the normalized billing.EventType. Stripe-shaped event
// strings ("invoice.payment_failed" etc.) are mapped to the normalized
// enum by the stripeWebhook handler before the call; Paddle's
// VerifyWebhook returns the normalized enum directly.
func (s *server) handleBillingEvent(ctx context.Context, ev billing.Event, acct state.Account) {
	switch ev.Type {
	case billing.EventSubscriptionCreated:
		// IAM-2 (issue #186) card-attached chokepoint. The
		// customer's first successful subscription event is the
		// "real customer with payment" boundary — flipping the
		// flag here means a customer who lands on the platform
		// via a Stripe Checkout flow is gated to MFA enrollment
		// on next login. SubscriptionUpdated (the periodic
		// plan-change ping) does NOT re-fire — Stripe and Paddle
		// both redeliver it on every plan change, and the audit
		// log would double up. Re-fetch `acct` carries the
		// post-event status for the chokepoint's enrolled check.
		fresh, err := s.store.AccountByID(ctx, acct.ID)
		if err != nil {
			s.log.Warn("card_attached chokepoint account fetch failed",
				"account", acct.ID, "err", err.Error())
		} else {
			s.flipMFARequiredIfUnenrolled(ctx, fresh, "card_attached", map[string]any{
				"provider": "stripe_or_paddle",
			})
		}
	case billing.EventSubscriptionCanceled:
		_ = s.store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended)
	case billing.EventPaymentFailed:
		// Apps keep serving; deploys blocked at the auth gate (handlers
		// reading acct.Active() refuse writes). 7-day dunning timer
		// (M7 dunning state machine) lives in pkg/meter.Dunning.
		//
		// We route through MarkDunningStep(active → past_due) instead
		// of the unconditional UpdateAccountStatus we used to call, so
		// past_due_at is stamped on the webhook event itself (not on
		// the dunning timer's first-observation backfill, which could
		// be up to one DunningInterval later — spec §4.7). The
		// compare-and-flip guard rejects rows already in past_due
		// (provider redelivery) and rows in suspended/deleted_pending
		// (no business flipping state backwards), both of which we
		// swallow as no-ops.
		if err := s.store.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				s.log.Debug("apid: payment_failed on already-advanced account",
					"account", acct.ID, "from_status", acct.Status)
			} else {
				s.log.Warn("apid: payment_failed MarkDunningStep", "account", acct.ID, "err", err)
			}
		} else {
			// First delivery — the CAS flip succeeded and past_due_at
			// was stamped. Send the entry-point email per spec §171
			// "All transitions emailed". Provider redelivery is
			// naturally silent here: MarkDunningStep returns
			// ErrNotFound on a second delivery (status already
			// past_due), which routes through the if branch above
			// and skips the mail.
			subject, body := mail.PaymentFailedBody(acct.Email, time.Now().UTC())
			if err := s.mailer.Send(ctx, Message{
				To: []string{acct.Email}, Subject: subject, TextBody: body,
			}); err != nil {
				s.log.Warn("apid: payment_failed mail",
					"account", acct.ID, "err", err)
			}
		}
	case billing.EventSubscriptionPastDue:
		// PR-P3: Paddle's `subscription.past_due` event lands here.
		// Stripe's path uses EventPaymentFailed (the
		// invoice.payment_failed webhook) for the same logical
		// transition; Paddle separates "payment failed" (the per-
		// charge transaction.failed) from "subscription is now in
		// past_due state" (the subscription-level ping). Both
		// flip active → past_due via the same CAS, so a Paddle
		// subscription.past_due arriving AFTER a Stripe
		// invoice.payment_failed on the same account collapses
		// safely (the second MarkDunningStep returns ErrNotFound
		// because status is already past_due).
		//
		// No email here: the past_due transition email is fired
		// exactly once on the first delivery (EventPaymentFailed
		// or this branch, whichever lands first). Emitting twice
		// would double-mail customers.
		if err := s.store.MarkDunningStep(ctx, acct.ID, state.AccountActive, state.AccountPastDue); err != nil {
			if errors.Is(err, state.ErrNotFound) {
				s.log.Debug("apid: subscription_past_due on already-advanced account",
					"account", acct.ID, "from_status", acct.Status)
			} else {
				s.log.Warn("apid: subscription_past_due MarkDunningStep",
					"account", acct.ID, "err", err)
			}
		}
	case billing.EventPaymentSucceeded:
		// Restore the account if it was past_due. meterd will refresh
		// quota state on its next tick. We also clear the dedupe stamp
		// on last_quota_warning_at so the next quota tick (if the
		// customer is still over quota from a prior cycle) emits a
		// fresh warning — otherwise the stamp from the previous day
		// would suppress it (spec §4.7).
		if acct.Status == state.AccountPastDue {
			if err := s.store.UpdateAccountStatus(ctx, acct.ID, state.AccountActive); err != nil {
				s.log.Warn("apid: payment_succeeded restore",
					"account", acct.ID, "err", err)
			} else {
				// Status just flipped back to active. Send the
				// recovery email (spec §171 "All transitions emailed").
				// payment_succeeded is naturally idempotent — the
				// status guard above ensures we only fire on a real
				// past_due → active transition, never on a no-op
				// redelivery.
				subject, body := mail.AccountRestoredBody(acct.Email, time.Now().UTC())
				if err := s.mailer.Send(ctx, Message{
					To: []string{acct.Email}, Subject: subject, TextBody: body,
				}); err != nil {
					s.log.Warn("apid: payment_succeeded mail",
						"account", acct.ID, "err", err)
				}
			}
		}
		// Clear the dedupe stamp on every payment_succeeded, not just
		// past_due → active flips: a fresh signup's first provider
		// confirmation shouldn't wait until the next UTC midnight to
		// hear about a quota they crossed during the trial, and the
		// cost of an extra pg_notify on a no-op event is nil.
		_ = s.store.ClearQuotaWarning(ctx, acct.ID)
	case billing.EventSubscriptionUpdated:
		if ev.PlanID != "" {
			_ = s.store.UpdateAccountPlan(ctx, acct.ID, api.Plan(ev.PlanID))
		}
	case billing.EventRefundProcessed:
		// Issue #279: a refund was issued against one of the account's
		// charges (Stripe: charge.refunded). The provider-initiated
		// refund path runs through Provider.Refund (POST /v1/admin/
		// accounts/{id}/credits), not this webhook — the webhook is
		// the asynchronous confirmation that Stripe accepted the
		// refund. We emit an audit row so an operator can correlate
		// the audit log with the Stripe dashboard.
		//
		// Idempotent: a redelivered charge.refunded hits the same
		// case and emits another audit row; auditors expect this
		// (it's a real "event happened" — the second delivery is a
		// different event in time). The dedupe happens upstream
		// (Stripe's webhook delivery has its own retry semantics).
		s.audit.Emit(ctx, "refund.processed", &acct.ID, map[string]any{
			"actor":              acct.ID,
			"actor_email":        acct.Email,
			"provider_refund_id": ev.ProviderRefundID,
			"charge_id":          ev.ChargeID,
			"amount_cents":       ev.AmountCents,
			"currency":           ev.Currency,
		})
	}
}

// mapStripeTypeToEventType translates Stripe's `type` strings into the
// normalized billing.EventType. Kept here (not in pkg/billing/stripe)
// because the mapping is apid's dunning-state-machine concern, not a
// provider-internal mapping. The Paddle path's pkg/billing/paddle
// already returns the normalized enum from its VerifyWebhook.
//
// Unknown types return EventUnknown so handleBillingEvent falls
// through to a 200 no-op (providers expect 2xx for everything they
// didn't recognize so they don't retry forever).
func mapStripeTypeToEventType(t string) billing.EventType {
	switch t {
	case "customer.subscription.created":
		return billing.EventSubscriptionCreated
	case "customer.subscription.updated":
		return billing.EventSubscriptionUpdated
	case "customer.subscription.deleted":
		return billing.EventSubscriptionCanceled
	case "customer.subscription.past_due":
		return billing.EventSubscriptionPastDue
	case "invoice.payment_failed":
		return billing.EventPaymentFailed
	case "invoice.payment_succeeded":
		return billing.EventPaymentSucceeded
	default:
		return billing.EventUnknown
	}
}

// lookupAccountByStripeID is a thin wrapper around
// state.Store.AccountByProviderCustomerID. The reverse index lives on the
// Store so the webhook stays O(1) regardless of account count (MemStore
// uses a map; PgStore uses a unique index).
func (s *server) lookupAccountByStripeID(ctx context.Context, stripeID string) (state.Account, error) {
	if stripeID == "" {
		return state.Account{}, errors.New("apid: empty stripe customer id")
	}
	return s.store.AccountByProviderCustomerID(ctx, stripeID)
}

// lookupAccountByPaddleID is the Paddle counterpart to
// lookupAccountByStripeID. The accounts.provider_customer_id column is
// reused (ADR-025 — column rename is a separate migration PR), so the
// underlying store method is a 1-line pass-through; the dedicated
// helper name keeps the Paddle call sites self-documenting.
func (s *server) lookupAccountByPaddleID(ctx context.Context, paddleID string) (state.Account, error) {
	if paddleID == "" {
		return state.Account{}, errors.New("apid: empty paddle customer id")
	}
	return s.store.AccountByProviderCustomerID(ctx, paddleID)
}

// --- response helpers ------------------------------------------------------

func (s *server) deploymentResponse(d state.Deployment) api.DeploymentResponse {
	// Issue #460 / ADR-053: echo the override_* columns on the
	// response. Env values are NEVER echoed (override_env_keys
	// carries only the key set). Env-secret refs ARE echoed
	// verbatim because "secret:NAME" is non-secret by design — the
	// customer needs to see which secret they bound to which env
	// var to debug a misconfigured deploy.
	hasOverrides := len(d.OverrideEntrypoint) > 0 ||
		len(d.OverrideCmd) > 0 ||
		len(d.OverrideEnv) > 0 ||
		len(d.OverrideEnvSecrets) > 0 ||
		d.OverridePort != 0 ||
		len(d.OverrideHealthcheck) > 0
	resp := api.DeploymentResponse{
		ID:           d.ID,
		AppID:        d.AppID,
		BuildID:      d.BuildID,
		ImageDigest:  d.ImageDigest,
		Kind:         string(d.Kind),
		Status:       string(d.Status),
		Error:        d.Error,
		ErrorCode:    d.ErrorCode,
		CreatedAt:    d.CreatedAt.UTC().Format(time.RFC3339),
		HasOverrides: hasOverrides,
		MinInstances: d.MinInstances,
		// Issue #556 PR-A: traffic_percent echoes the per-deployment
		// split weight. Σ over live rows for the app is 100 by
		// construction (CreateDeployment zeros the prior row in the
		// same tx; UpdateDeploymentTraffic zeros siblings in its
		// tx). For the single-live-deployment case (the most common
		// shape today), Σ = 100 is trivially this one field.
		TrafficPercent: d.TrafficPercent,
	}
	if len(d.OverrideEntrypoint) > 0 {
		resp.OverrideEntrypoint = d.OverrideEntrypoint
	}
	if len(d.OverrideCmd) > 0 {
		resp.OverrideCmd = d.OverrideCmd
	}
	if len(d.OverrideEnv) > 0 {
		// Decode the jsonb map to surface the KEYS only. Values are
		// never echoed (ADR-053 §Decision 4). Stable order: sorted
		// alphabetically so two deploys with the same key set hash
		// to the same JSON body.
		var env map[string]string
		if err := json.Unmarshal(d.OverrideEnv, &env); err == nil {
			keys := make([]string, 0, len(env))
			for k := range env {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			resp.OverrideEnvKeys = keys
		}
	}
	if len(d.OverrideEnvSecrets) > 0 {
		// Decode the jsonb map to surface keys + refs. Refs are
		// non-secret; both keys and refs are safe to echo.
		var refs map[string]string
		if err := json.Unmarshal(d.OverrideEnvSecrets, &refs); err == nil {
			keys := make([]string, 0, len(refs))
			for k := range refs {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			resp.OverrideEnvSecretKeys = keys
			resp.OverrideEnvSecretRefs = refs
		}
	}
	if d.OverridePort != 0 {
		resp.OverridePort = d.OverridePort
	}
	if len(d.OverrideHealthcheck) > 0 {
		var hc api.DeploymentHealthcheck
		if err := json.Unmarshal(d.OverrideHealthcheck, &hc); err == nil {
			resp.OverrideHealthcheck = &hc
		}
	}
	// Liveness probe override (issue #554 / ADR-078). The
	// per-deployment override; cmd/vmmd's liveness_recv goroutine
	// picks this up at every BringUp via the resolved struct
	// (cmd/vmmd/liveness_recv.go::livenessProbeConfig).
	if len(d.OverrideLivenessProbe) > 0 {
		var lp api.DeploymentLivenessProbe
		if err := json.Unmarshal(d.OverrideLivenessProbe, &lp); err == nil {
			resp.OverrideLivenessProbe = &lp
		}
	}
	// Per-deploy grype scan (issue #464 / ADR-075 / PR-3).
	// Populate DeploymentResponse.Scan from the typed payload
	// written by imaged's deploy-complete hook (PR-3 commit 2).
	// The handler-side conversion from the on-disk jsonb shape
	// (state.Deployment.ScanResult []byte) into the wire DTO
	// (api.ScanResult) happens via s.scanResponse so this
	// handler and the /scan drill-down route (PR-4) emit
	// IDENTICAL typed payloads for a given row. The single
	// source of truth lives in cmd/apid/handlers_scan.go.
	//
	// scanResponse returns nil when the row has no scan_status
	// (mid-pipeline / pre-#464 row); the Scan field's omitempty
	// tag then drops the field from the wire response. The
	// dashboard renders "scan pending" on the absence.
	resp.Scan = s.scanResponse(d)
	// Issue #554 / ADR-079 follow-up (AC #3 wire): surface the
	// per-deployment parked_reason + parked_at columns from
	// migration 00157. omitempty on the DTO handles the "never
	// parked" branch — the field is absent on the wire for the
	// vast majority of deployments. The closed-set vocabulary
	// is enforced at the schema layer.
	resp.ParkedReason = d.ParkedReason
	resp.ParkedAt = d.ParkedAt
	return resp
}

// buildProvenanceResponse renders a state.BuildProvenance as the
// public DTO (ADR-038). Field-by-field mirror; timestamps render
// as RFC3339 UTC strings. Empty strings (cache-hit builds today;
// pre-Phase-3 builds once Phase 3 ships) pass through as-is so the
// customer can branch on <field> != "".
func (s *server) buildProvenanceResponse(p state.BuildProvenance) api.BuildProvenanceResponse {
	return api.BuildProvenanceResponse{
		ID:             p.ID,
		BuildID:        p.BuildID,
		BuildkitVer:    p.BuildkitVer,
		RailpackVer:    p.RailpackVer,
		BaseDigest:     p.BaseDigest,
		SourceSHA256:   p.SourceSHA256,
		SourceURL:      p.SourceURL,
		CommitSHA:      p.CommitSHA,
		Plan:           p.Plan,
		RunnerDigest:   p.RunnerDigest,
		BuilderNodeID:  p.BuilderNodeID,
		StartedAt:      p.StartedAt.UTC().Format(time.RFC3339),
		FinishedAt:     p.FinishedAt.UTC().Format(time.RFC3339),
		SBOMStorageKey: p.SBOMStorageKey,
		FrameworkVer:   p.FrameworkVer,
	}
}

// buildResponse projects a state.Build into the wire BuildResponse
// (DEPLOY-PROV-6 / ADR-089, issue #741). state.Build is the
// in-memory shape (string IDs, plain time.Time) — the sqlc
// pgtype.Timestamptz values are translated to time.Time by the
// store layer. We treat the zero time as "unset" (queued builds
// have no started_at; running builds have no finished_at) and
// rely on the omitempty tags on BuildResponse so the JSON stays
// minimal.
//
// duration_seconds is server-computed: only set when BOTH
// StartedAt and FinishedAt are non-zero — a CI script can always
// rely on its presence meaning "the build reached a terminal
// state and elapsed N wall-clock seconds."
func (s *server) buildResponse(b state.Build) api.BuildResponse {
	out := api.BuildResponse{
		ID:           b.ID,
		DeploymentID: b.DeploymentID,
		Kind:         string(b.Kind),
		SourceBytes:  b.SourceBytes,
		Status:       string(b.Status),
	}
	if b.FailureClass != "" {
		out.FailureClass = string(b.FailureClass)
	}
	if b.LogPath != "" {
		out.LogPath = b.LogPath
	}
	if !b.StartedAt.IsZero() {
		out.StartedAt = b.StartedAt.UTC().Format(time.RFC3339)
	}
	if !b.FinishedAt.IsZero() {
		out.FinishedAt = b.FinishedAt.UTC().Format(time.RFC3339)
	}
	if !b.StartedAt.IsZero() && !b.FinishedAt.IsZero() {
		out.DurationSeconds = int(b.FinishedAt.Sub(b.StartedAt).Seconds())
	}
	out.EnqueuedAt = b.EnqueuedAt.UTC().Format(time.RFC3339)
	return out
}

// instanceResponse projects a state.Instance into the wire
// InstanceResponse. The minInstancesTarget parameter carries the
// parent app's effective min_instances (issue #557 / ADR-071) so
// dashboards can verify the proactive floor is being met on a
// per-instance basis. Zero is omitted via the JSON `omitempty`
// contract — customers who never opted in see no field.
func instanceResponse(ins state.Instance, minInstancesTarget int) api.InstanceResponse {
	r := api.InstanceResponse{
		ID:                 ins.ID,
		AppID:              ins.AppID,
		DeploymentID:       ins.DeploymentID,
		State:              ins.State,
		HostIP:             ins.HostIP,
		RAMMB:              ins.RAMMB,
		WakeID:             ins.WakeID,
		MinInstancesTarget: minInstancesTarget,
	}
	if !ins.StartedAt.IsZero() {
		r.StartedAt = ins.StartedAt.UTC().Format(time.RFC3339)
	}
	if !ins.LastRequestAt.IsZero() {
		r.LastRequestAt = ins.LastRequestAt.UTC().Format(time.RFC3339)
	}
	if !ins.ParkedAt.IsZero() {
		r.ParkedAt = ins.ParkedAt.UTC().Format(time.RFC3339)
	}
	return r
}

func domainResponse(d state.CustomDomain) api.CustomDomainResponse {
	r := api.CustomDomainResponse{
		Domain:         d.Domain,
		AppID:          d.AppID,
		ChallengeToken: d.ChallengeToken,
		Verified:       d.Verified(),
	}
	if d.Verified() {
		r.VerifiedAt = d.VerifiedAt.UTC().Format(time.RFC3339)
	}
	if d.ChallengeToken != "" {
		r.TXTRecord = "_faas-verify." + d.Domain + `  TXT  "` + d.ChallengeToken + `"`
	}
	return r
}

func cronResponse(c state.Cron) api.CronResponse {
	resp := api.CronResponse{
		ID:        c.ID,
		AppID:     c.AppID,
		Schedule:  c.Schedule,
		Path:      c.Path,
		Enabled:   c.Enabled,
		CreatedAt: c.CreatedAt.UTC().Format(time.RFC3339),
	}
	if !c.LastFiredAt.IsZero() {
		resp.LastFiredAt = c.LastFiredAt.UTC().Format(time.RFC3339)
	}
	return resp
}

// --- dashboard support endpoints (M7.5 slice 4) -----------------------------

// listDeployments serves GET /v1/deployments — every deployment the
// account owns, in created_at DESC order. Cursor pagination via
// ?before=<RFC3339Nano>; limit defaults to 50, capped at 200.
func (s *server) listDeployments(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		// RFC3339Nano — matches state.Deployment.CreatedAt. Lenient on
		// RFC3339 too via a fallback parse so callers sending either
		// format succeed.
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			before = t
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp"))
			return
		}
	}
	rows, err := s.store.ListDeploymentsForAccount(r.Context(), acct.ID, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list deployments"))
		return
	}
	resp := api.DeploymentListResponse{Items: make([]api.DeploymentResponse, 0, len(rows))}
	for _, d := range rows {
		resp.Items = append(resp.Items, s.deploymentResponse(d))
	}
	if len(rows) == limit && limit > 0 && len(resp.Items) > 0 {
		// NextBefore = CreatedAt of the LAST row (the oldest on this
		// page). Pass it back as `before` to fetch the next page.
		last := resp.Items[len(resp.Items)-1].CreatedAt
		if t, err := time.Parse(time.RFC3339, last); err == nil {
			resp.NextBefore = t.UTC().Format(time.RFC3339Nano)
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// listBuilds serves GET /v1/builds — every build the account owns,
// in started_at desc nulls last order (DEPLOY-PROV-6 follow-up /
// ADR-091, issue #741 close-out). Optional ?app=<slug> narrows
// to one app; optional ?status=<s> filters to the 4-value status
// enum. Cursor pagination via ?before=<RFC3339Nano>; limit
// defaults to 50, capped at 200.
//
// IDOR: when ?app=<slug> is set, AppBySlug + App.AccountID == acct.ID
// gates the query (cross-account slug renders 404 app_not_found,
// same envelope as getApp). When ?app is omitted, the SQL itself
// filters on a.account_id = $1 so cross-account data never leaves
// the store.
//
// Filter validation: status must be one of queued|running|
// succeeded|failed (bad values → 400 CodeValidation). Cursor must
// parse as RFC3339Nano with RFC3339 fallback (bad → 400).
//
// Per ADR-089 §6 the route uses authLimited(requireScope(...))
// WITHOUT requireMFA — same shape as GET /v1/builds/{id} (this
// is intentional; GET /v1/deployments does use requireMFA but
// the builds family does not — see ADR-089 §6). The route mount
// in cmd/apid/server.go calls this out.
func (s *server) listBuilds(w http.ResponseWriter, r *http.Request, acct state.Account) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 200 {
				n = 200
			}
			limit = n
		}
	}
	var before time.Time
	if v := r.URL.Query().Get("before"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			before = t
		} else if t, err := time.Parse(time.RFC3339, v); err == nil {
			before = t
		} else {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp"))
			return
		}
	}
	statusFilter := r.URL.Query().Get("status")
	if statusFilter != "" {
		switch statusFilter {
		case api.BuildStatusQueued, api.BuildStatusRunning,
			api.BuildStatusSucceeded, api.BuildStatusFailed:
			// ok
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad status filter",
				"expected one of queued|running|succeeded|failed"))
			return
		}
	}
	appIDFilter := ""
	if slug := r.URL.Query().Get("app"); slug != "" {
		app, ok := s.loadApp(w, r, acct, slug)
		if !ok {
			return
		}
		appIDFilter = app.ID
	}
	rows, err := s.store.ListBuildsForAccountPaged(
		r.Context(), acct.ID, statusFilter, appIDFilter, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list builds"))
		return
	}
	resp := api.BuildListResponse{Items: make([]api.BuildResponse, 0, len(rows))}
	for _, b := range rows {
		resp.Items = append(resp.Items, s.buildResponse(b))
	}
	if len(rows) == limit && limit > 0 && len(resp.Items) > 0 {
		// NextBefore = started_at of the LAST row that has a non-null
		// started_at (queued builds at the tail can't be a cursor —
		// passing them would skip the running/succeeded rows behind).
		// BuildResponse.StartedAt is the RFC3339 string (whole-second
		// precision) per buildResponse at line 2994; sub-second
		// precision is intentionally not exposed on the wire. The
		// cursor is therefore always whole-second-aligned — passing
		// it back with `<` semantics means rows whose wall-clock
		// second equals the cursor are dropped (same second as the
		// last row on this page = already returned). Tests that
		// want second-precise equality should use RFC3339Nano on
		// the store side, but the handler contract is second-
		// aligned by design (matches buildResponse's wire shape).
		for i := len(resp.Items) - 1; i >= 0; i-- {
			if resp.Items[i].StartedAt != "" {
				if t, err := time.Parse(time.RFC3339, resp.Items[i].StartedAt); err == nil {
					resp.NextBefore = t.UTC().Format(time.RFC3339Nano)
				}
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// usageSummary serves GET /v1/usage/summary — the aggregate
// current-month (or ?month=YYYY-MM) roll-up the dashboard's usage
// page renders. All money is integer cents; GB-h is float.
func (s *server) usageSummary(w http.ResponseWriter, r *http.Request, acct state.Account) {
	monthStr := r.URL.Query().Get("month")
	if monthStr == "" {
		monthStr = time.Now().UTC().Format("2006-01")
	}
	month, err := time.Parse("2006-01", monthStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad month", "expected YYYY-MM"))
		return
	}
	rows, err := s.store.UsageByMonth(r.Context(), acct.ID, month)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load usage"))
		return
	}
	var mbSec, cpuUsec, netRxBytes, coldBoots int64
	for _, u := range rows {
		mbSec += u.MBSeconds
		cpuUsec += u.CPUUsec
		netRxBytes += u.NetRxBytes
		coldBoots += u.ColdBootCount
	}
	usedGB := float64(mbSec) / 3_600_000.0
	// usedCPUHours is the per-month CPU-hours — informational only
	// (issue #279 / PR-B). Billing math is on usedGB (plan RAM +
	// 8 MB per running second). The conversion is the same shape
	// the SDK exposes via UsageResponse.CPUHours() / UsageExportResponse
	// .CPUHours() — keep them in lockstep by going through
	// meter.CPUHours.
	usedCPUHours := meter.CPUHours(cpuUsec)
	limits := api.MustLimitsFor(acct.Plan)
	included := int64(limits.IncludedGBHours)
	overage := usedGB - float64(included)
	if overage < 0 {
		overage = 0
	}
	// Spec §1 / financial model: €0.01/GB-h overage → 1 cent per
	// GB-h. Plan's overage rate can vary in the model; constants here
	// are the production default. Storing cents as int64 keeps
	// floats away from money (spec §Conventions).
	overageCents := int64(overage * 1.0)
	writeJSON(w, http.StatusOK, api.UsageSummaryResponse{
		Month:           monthStr,
		UsedGBHours:     usedGB,
		IncludedGBHours: included,
		OverageGBHours:  overage,
		OverageCents:    overageCents,
		UsedCPUHours:    usedCPUHours,
		// ADR-048: ingress Σ + cold-boot Σ across every
		// app on this account for the month. Both
		// informational, not billed.
		UsedIngressGB: float64(netRxBytes) / (1024 * 1024 * 1024),
		ColdBootTotal: coldBoots,
	})
}

// usageDaily serves GET /v1/usage/daily?day=YYYY-MM-DD — the
// per-app rollup row the meterd rollup loop has populated into
// usage_daily (ADR-048 §5, migration 00067). Distinct from
// usageSummary's per-month SUM: this is a single-day read for the
// dashboard hot path. All numeric fields are informational per ADR-048.
//
// day is required; without it we 400 to keep the dashboard
// unambiguously anchored. A future ?month= query can layer on
// top if needed.
func (s *server) usageDaily(w http.ResponseWriter, r *http.Request, acct state.Account) {
	dayStr := r.URL.Query().Get("day")
	if dayStr == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing day", "expected ?day=YYYY-MM-DD"))
		return
	}
	day, err := time.Parse("2006-01-02", dayStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad day", "expected YYYY-MM-DD"))
		return
	}
	rows, err := s.store.UsageDaily(r.Context(), acct.ID, day)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load daily usage"))
		return
	}
	resp := api.DailyUsageListResponse{Items: make([]api.DailyUsageResponse, 0, len(rows))}
	for _, u := range rows {
		resp.Items = append(resp.Items, api.DailyUsageResponse{
			AppID:          u.AppID,
			Day:            u.Day.UTC().Format("2006-01-02"),
			MBSeconds:      u.MBSeconds,
			Requests:       u.Requests,
			CPUUsageUsec:   u.CPUUsec,
			TXBytes:        u.TXBytes,
			NetTxBytes:     u.NetTxBytes,
			NetRxBytes:     u.NetRxBytes,
			ColdBootCount:  u.ColdBootCount,
			BuilderSeconds: u.BuilderSeconds,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// usageStorage serves GET /v1/usage/storage?day=YYYY-MM-DD — the
// per-(app, day) snapshot+layer byte rollup (ADR-049 §B.3,
// migration 00070). Distinct from /v1/usage which reports billable
// compute; this route reports storage footprint. Informational
// only today — the future "Pro plan 1 GB included" PR consumes
// this surface.
//
// day is required (same contract as /v1/usage/daily). All numeric
// fields are informational; not billed.
func (s *server) usageStorage(w http.ResponseWriter, r *http.Request, acct state.Account) {
	dayStr := r.URL.Query().Get("day")
	if dayStr == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Missing day", "expected ?day=YYYY-MM-DD"))
		return
	}
	day, err := time.Parse("2006-01-02", dayStr)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Bad day", "expected YYYY-MM-DD"))
		return
	}
	rows, err := s.store.StorageUsage(r.Context(), acct.ID, day)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load storage usage"))
		return
	}
	resp := api.StorageUsageListResponse{Items: make([]api.StorageUsageResponse, 0, len(rows))}
	for _, u := range rows {
		resp.Items = append(resp.Items, api.StorageUsageResponse{
			AppID:         u.AppID,
			Day:           u.Day.UTC().Format("2006-01-02"),
			SnapshotBytes: u.SnapshotBytes,
			LayerBytes:    u.LayerBytes,
		})
	}
	writeJSON(w, http.StatusOK, resp)
}

// getBillingPortal serves GET /v1/billing/portal — issue #253. It
// returns the operator-configured Stripe billing portal URL for the
// authenticated account. The URL itself does not mutate anything; the
// customer-facing mutations (card update, cancel, plan change) live
// inside the Stripe-hosted portal that the URL points to.
//
// Auth: standard /v1/* Bearer-or-cookie chain (server.go:703 cluster).
// We deliberately do NOT require MFA — viewing a portal link is a
// read; the mutations gated by the portal itself happen after the
// customer has authenticated to Stripe with 2FA on their side.
//
// Empty URL response means the box has FAAS_BILLING_PORTAL_URL unset.
// We return 200 with `{"url":""}` (omitempty) rather than 404 because
// the request itself succeeded; the absence is conveyed in the
// payload, not the status. The CLI branches on the empty string to
// print a friendly "portal not configured" hint.
func (s *server) getBillingPortal(w http.ResponseWriter, r *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, api.BillingPortalResponse{URL: s.billingPortalURLFor(acct)})
}

// listInvoices serves GET /v1/invoices — issue #259 invoice history.
// Account-scoped: the authenticated principal is the only source of
// accountID. Pagination is RFC3339Nano cursor (period_end DESC); month
// is an optional "YYYY-MM" filter (half-open UTC range). Empty history
// returns 200 with an empty items array, never 404.
func (s *server) listInvoices(w http.ResponseWriter, r *http.Request, acct state.Account) {
	monthPtr, before, limit, perr := parseInvoiceListParams(r)
	if perr != nil {
		api.WriteProblem(w, perr)
		return
	}
	rows, err := s.store.ListInvoicesForAccount(r.Context(), acct.ID, monthPtr, before, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list invoices"))
		return
	}
	resp := api.InvoiceListResponse{Items: make([]api.Invoice, 0, len(rows))}
	for _, inv := range rows {
		resp.Items = append(resp.Items, invoiceResponse(inv))
	}
	if len(rows) == limit && len(resp.Items) > 0 {
		// Same emit convention as listDeployments: format the last
		// row's period_end as RFC3339Nano so the handler can hand
		// the value back unchanged as `before` on the next request.
		resp.NextBefore = resp.Items[len(resp.Items)-1].PeriodEnd.UTC().Format(time.RFC3339Nano)
	}
	writeJSON(w, http.StatusOK, resp)
}

// parseInvoiceListParams parses the GET /v1/invoices query string.
// Returns the structured (problem, nil) pair on validation failure so
// the handler can write through without duplicating the WriteProblem
// boilerplate. month filter is optional; limit is clamped 1..100
// (default 25). On bad limit we thread the limit + observed values
// into the Problem so RFC 7807 consumers can surface actionable
// guidance (CLAUDE.md "limit errors include limit + observed + docs").
func parseInvoiceListParams(r *http.Request) (month *time.Time, before time.Time, limit int, err *api.Problem) {
	const limitMax = 100
	limit = 25
	if v := r.URL.Query().Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > limitMax {
			observed := int64(0)
			if perr == nil {
				observed = int64(n)
			}
			return nil, time.Time{}, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad limit", "expected 1..100").
				WithLimit(int64(limitMax), observed).
				WithDocs("https://" + wire.DocsHost + "/billing#invoices")
		}
		limit = n
	}
	if v := r.URL.Query().Get("month"); v != "" {
		m, perr := time.Parse("2006-01", v)
		if perr != nil {
			return nil, time.Time{}, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad month", "expected YYYY-MM")
		}
		month = &m
	}
	if v := r.URL.Query().Get("before"); v != "" {
		if t, perr := time.Parse(time.RFC3339Nano, v); perr == nil {
			before = t
		} else if t, perr := time.Parse(time.RFC3339, v); perr == nil {
			before = t
		} else {
			return nil, time.Time{}, 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected RFC3339 timestamp")
		}
	}
	return month, before, limit, nil
}

// parseCronRunsLimit validates the ?limit= query string for
// GET /v1/crons/{id}/runs. Default 10; clamped 1..100. Bad input is
// surfaced as a 400 Problem with limit + observed + docs URL so
// clients see the misconfiguration instead of silently falling back
// to the default (which masks their bug). Mirrors the invoice
// helper above; kept as a separate function because cron's `before`
// is an invocation id, not a timestamp — that parsing belongs with
// the storage layer's cursor handling.
func parseCronRunsLimit(r *http.Request) (int, *api.Problem) {
	const limitMax = 100
	limit := 10
	if v := r.URL.Query().Get("limit"); v != "" {
		n, perr := strconv.Atoi(v)
		if perr != nil || n < 1 || n > limitMax {
			observed := int64(0)
			if perr == nil {
				observed = int64(n)
			}
			return 0, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad limit", "expected 1..100").
				WithLimit(int64(limitMax), observed).
				WithDocs("https://" + wire.DocsHost + "/crons#runs")
		}
		limit = n
	}
	return limit, nil
}

// invoiceResponse maps state.Invoice to the API DTO. Direct field
// pass-through — the state row is already in the on-the-wire shape,
// except HostedURL which is intentionally dropped (PR-B audit only).
func invoiceResponse(inv state.Invoice) api.Invoice {
	return api.Invoice{
		ID:                inv.ID,
		Provider:          inv.Provider,
		ProviderInvoiceID: inv.ProviderInvoiceID,
		Number:            inv.Number,
		Status:            inv.Status,
		PeriodStart:       inv.PeriodStart,
		PeriodEnd:         inv.PeriodEnd,
		SubtotalCents:     inv.SubtotalCents,
		TaxCents:          inv.TaxCents,
		TotalCents:        inv.TotalCents,
		AmountPaidCents:   inv.AmountPaidCents,
		Currency:          inv.Currency,
		PDFAvailable:      inv.PDFAvailable,
		CreatedAt:         inv.CreatedAt,
	}
}

// --- small helpers ---------------------------------------------------------

func randomToken(nBytes int) string {
	b := make([]byte, nBytes)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// keyPrefix returns the first 16 chars of the plaintext key (matches the
// "fp_live_xxxxxxxx" prefix that shows up in dashboards).
func keyPrefix(plaintext string) string {
	if len(plaintext) < 16 {
		return plaintext
	}
	return plaintext[:16]
}

// keyPrefixFromHash derives the display prefix from the stored hash. The hash
// itself is sha256(plaintext); the prefix is hex(sha256)[:12] so the customer
// can correlate the hash back to the plaintext key they were shown once.
func keyPrefixFromHash(hash []byte) string {
	if len(hash) < 6 {
		return api.APIKeyPrefix
	}
	return api.APIKeyPrefix + hex.EncodeToString(hash)[:12]
}

// validCron returns true if s is a 5-field cron expression. The actual
// scheduler (spec §4.3) reuses robfig/cron's parser in pkg/sched — this is a
// quick shape check so apid rejects obviously bad input at the API boundary
// instead of letting it through to schedd.
func validCron(s string) bool {
	fields := strings.Fields(s)
	return len(fields) == 5
}

// streamDeploymentLogs serves the build log for a deployment as a
// real Server-Sent Event stream backed by the deployment_logs table
// (M7.5 slice 5; spec §14 + ADR-011). Two phases:
//
//  1. Initial page — `ListDeploymentLogs(deploymentID, before_seq,
//     limit)`, written out in order from oldest → newest (the table
//     returns DESC, the SSE client expects chronological).
//  2. Live tail — subscribe to the in-process broadcaster, write
//     every published log line until the context cancels or the
//     deployment transitions to `live`/`failed` (an `end` event is
//     emitted).
//
// ?before_seq=0 (default) opens with the oldest 50; pass ?before_seq=N
// to resume from seq N. ?limit= caps the initial page (default 50,
// max 500). ?follow=0 closes after the initial page (CLI-friendly
// "fetch once" mode).
//
//nolint:contextcheck // r.Context() === r.Context(); suppressed per-call to avoid line-by-line noise in a long SSE handler.
func (s *server) streamDeploymentLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	beforeSeq := int64(0)
	if v := r.URL.Query().Get("before_seq"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			beforeSeq = n
		}
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			if n > 500 {
				n = 500
			}
			limit = n
		}
	}
	follow := r.URL.Query().Get("follow") != "0"

	apislogs.StartSSE(w)
	flusher, _ := w.(http.Flusher)

	// Walk backwards: the table returns DESC by seq, the SSE stream
	// wants chronological. MemStore + PgStore both order DESC.
	//nolint:contextcheck // Long SSE handler; r.Context() == r.Context() but the linter loses the alias across the function's many statements.
	page, _, err := s.store.ListDeploymentLogs(r.Context(), id, beforeSeq, limit)
	if err != nil {
		_, _ = fmt.Fprintf(w, "event: error\ndata: {\"error\":%q}\n\n", err.Error())
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	for i := len(page) - 1; i >= 0; i-- {
		writeLogEvent(w, flusher, page[i])
	}

	if !follow {
		_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}

	// Live tail: subscribe to the broadcaster until deploy is done
	// or the client disconnects.
	sub, cancel := s.events.Subscribe(events.TopicDeploymentLog)
	defer cancel()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	// Issue #64 D4: poll the deployment row cheaply while we tail so
	// we can emit `event: status` as soon as the build resolves
	// instead of waiting for the 10-min build timeout. imaged is a
	// separate process so the in-process TopicDeploymentLog pub/sub
	// can't see the transition; the store is the only shared source
	// of truth within the same apid process. One indexed lookup per
	// poll tick — negligible load compared to the build itself.
	statusTicker := time.NewTicker(2 * time.Second)
	defer statusTicker.Stop()

	// Move 3: one-shot backstop timer (replaces per-iteration
	// time.After(10*time.Minute), which was never reaping the timer
	// on a busy stream — the select arm would re-allocate a fresh
	// timer every pass and the 10-min cap was effectively a
	// "10-min-after-last-event" cap, not an absolute cap). Reset
	// only on terminal status / client disconnect; the busy
	// subscriber never resets, so a build that keeps emitting
	// never escapes the cap if the status poll ever flakes.
	backstop := time.NewTimer(10 * time.Minute)
	defer backstop.Stop()

	for {
		// Done status short-circuits the tail. deployment status flips
		// to live/failed via NotifyDeploymentChanged; the dashboard
		// already lives off that channel for the dashboard app
		// update. Slice 6 wires the full pg_notify fan-in. Slice 5
		// keeps it simple with a deadline: builds max out at 10
		// minutes; we cap the tail to that.
		select {
		case <-r.Context().Done():
			return
		case e, ok := <-sub:
			if !ok {
				return
			}
			// payload is the marshalled LogEntry — write verbatim.
			_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", e.Payload)
			if flusher != nil {
				flusher.Flush()
			}
		case <-statusTicker.C:
			// Cheap status poll. Emits `event: status` and exits
			// when the deployment reaches a terminal state. The
			// 10-min backstop below still fires if something hangs.
			if d2, err := s.store.DeploymentByID(r.Context(), id); err == nil &&
				(d2.Status == state.DeployLive || d2.Status == state.DeployFailed) {
				_, _ = fmt.Fprintf(w, "event: status\ndata: {\"status\":%q}\n\n", d2.Status)
				if flusher != nil {
					flusher.Flush()
				}
				return
			}
		case <-ticker.C:
			// heartbeat — keeps idle proxies from dropping the
			// connection. Doesn't carry data.
			_, _ = fmt.Fprint(w, ":\n\n")
			if flusher != nil {
				flusher.Flush()
			}
		case <-backstop.C:
			_, _ = fmt.Fprint(w, "event: end\ndata: {\"reason\":\"timeout\"}\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
	}
}

// writeLogEvent formats one LogEntry as a single SSE event. Used by
// both the initial-page path and the live tail.
func writeLogEvent(w http.ResponseWriter, flusher http.Flusher, e state.LogEntry) {
	payload, _ := json.Marshal(map[string]any{
		"seq":        e.Seq,
		"stream":     e.Stream,
		"line":       e.Line,
		"written_at": e.WrittenAt.UTC().Format(time.RFC3339Nano),
	})
	_, _ = fmt.Fprintf(w, "event: log\ndata: %s\n\n", payload)
	if flusher != nil {
		flusher.Flush()
	}
}

// getBuild returns the lifecycle row for a build id (DEPLOY-PROV-6
// / ADR-089, issue #741). Companion to the ADR-038
// /v1/builds/{id}/provenance (post-mortem export) and
// /v1/builds/{id}/sbom (post-mortem blob) routes — this one is
// the LIFECYCLE surface (status, timestamps, failure_class,
// duration). CI scripts call this to fail-fast on build error
// without scraping SSE; the CLI's streamDeployLogs fallback
// (pollBuildStatus in commands2.go) also loops on it.
//
// IDOR chain mirrors getBuildProvenance: BuildByID →
// DeploymentByID → AppByID, comparing App.AccountID against the
// requesting account. Every negative path renders 404 with
// code=build_not_found so cross-account probes can't enumerate.
// The "no such build" envelope is shared with the other
// /v1/builds/{id}/* routes.
//
// Per ADR-034 rev2 the route is gated by api.ScopesReadSurface
// (the same chain as getBuildProvenance / getBuildSbom; see
// cmd/apid/server.go:803).
func (s *server) getBuild(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	build, err := s.store.BuildByID(r.Context(), id)
	if err != nil {
		api.WriteProblem(w, api.ErrBuildNotFound())
		return
	}
	dep, err := s.store.DeploymentByID(r.Context(), build.DeploymentID)
	if err != nil {
		api.WriteProblem(w, api.ErrBuildNotFound())
		return
	}
	app, err := s.store.AppByID(r.Context(), dep.AppID)
	if err != nil || app.AccountID != acct.ID {
		api.WriteProblem(w, api.ErrBuildNotFound())
		return
	}
	writeJSON(w, http.StatusOK, s.buildResponse(build))
}

// getBuildProvenance returns the ADR-038 provenance row for a build
// (Tier 3 / issue #197 B3.10-read half). Two-step ownership check:
// BuildByID → DeploymentByID → AppByID, comparing App.AccountID against
// the requesting account. A mismatch renders 404 with the same
// envelope getDeployment uses ("no such build") so IDOR probes
// can't enumerate cross-account build ids (security round-4 finding).
//
// A build with no provenance row (pre-PR build, or successful
// build whose populator INSERT failed and was logged at WARN
// inside builderd.recordProvenance) renders 404 with code
// build_provenance_not_found — distinct from "no such build"
// so the customer can branch on the difference.
// Per ADR-034 rev2 the route is gated by the same `build:read`
// scope the rest of the build surface uses.
func (s *server) getBuildProvenance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	build, err := s.store.BuildByID(r.Context(), id)
	if err != nil {
		s.notFound(w, "no such build")
		return
	}
	dep, err := s.store.DeploymentByID(r.Context(), build.DeploymentID)
	if err != nil {
		s.notFound(w, "no such build")
		return
	}
	app, err := s.store.AppByID(r.Context(), dep.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such build")
		return
	}
	prov, err := s.store.BuildProvenanceByBuildID(r.Context(), build.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrBuildProvenanceNotFound())
		return
	}
	writeJSON(w, http.StatusOK, s.buildProvenanceResponse(prov))
}

// getBuildSbom streams the CycloneDX SBOM for a build id (issue #299
// / ADR-038 Phase 3). `faas build sbom <id>` and the SDK's
// GetBuildsIdSbom surface route through here.
//
// IDOR-safe: every step mirrors getBuildProvenance — Build → Deployment
// → App → AccountID — so a customer cannot infer the existence of
// another customer's build id by timing the response. The 404 surface
// is "no such build" on every negative path so probing returns the
// same answer as a genuinely-missing id.
//
// Three different failure modes, each with its own code so the SDK can
// branch:
//
//   - 404 no such build — id never existed, no build_provenance row,
//     or the build belongs to another account. Standard path.
//   - 503 build_sbom_unavailable — the build row exists and belongs
//     to this account, but imaged's syft populator (pkg/imaged/loop.go)
//     did not land a build_provenance.sbom_storage_key (pre-Phase-3 build,
//     or the populator INSERT was best-effort WARNed). The CLI prints
//     "no SBOM for this build" and exits 1; the customer's branch is
//     "retry after operator rebuilds imaged" not "abort the audit".
//   - 503 with sbom_storage_key set but the file missing on disk —
//     same code (the populator exists in the schema but not on the
//     filesystem; recoverable by re-running imaged).
//
// The SBOM is served with `application/vnd.cyclonedx+json` so external
// tooling (cyclonedx-cli validate, jq's @cdx/manifest formatters) can
// route on the content-type without sniffing the body. The blob is
// streamed with io.Copy — sized SBOMs run to ~50 KB compressed JSON,
// well under the handler heap budget.
func (s *server) getBuildSbom(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	sbomPath, prob := s.resolveSbomPath(r, id, acct)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	f, err := os.Open(sbomPath) //nolint:forbidigo // sbomPath is constructed in resolveSbomPath from a server-trusted DB column (build_provenance.sbom_storage_key, written by imaged's populator) joined onto s.sbomRoot; the path-traversal guard at resolveSbomPath:2082-2085 already rejects leading "/", "..", and "." segments. Unlike cmd/faas/commands5.go's openCustomerFile, this is a server-side read of an operator-controlled root, not a customer-supplied path — the Lstat-on-final-component TOCTOU guard the CLI enforces is unnecessary here because the customer cannot influence the sbomRoot or sbomStorageKey contents.
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			api.WriteProblem(w, api.ErrBuildSBOMUnavailable())
			return
		}
		api.WriteProblem(w, api.ErrCapacity("build SBOM unreadable"))
		return
	}
	defer func() { _ = f.Close() }()
	w.Header().Set("Content-Type", "application/vnd.cyclonedx+json")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(w, f)
}

// resolveSbomPath centralises the IDOR-safe lookup + storage-key
// validation logic so getBuildSbom stays under the 50-line handler
// budget (CLAUDE.md). Returns the local-filesystem path to the SBOM
// blob, or a 0 path + non-zero *api.Problem indicating the failure
// mode:
//
//   - 404 not_found "no such build": build / deployment / app /
//     AccountID mismatch — IDOR-safe (every negative path collapses
//     to the same response so probing can't infer other customers'
//     build ids).
//   - 503 build_sbom_unavailable: SBOM populator didn't write the column
//     (pre-Phase-3 build, populator INSERT best-effort WARN'd) or
//     sbomRoot is unset, or the storage key fails the path-traversal guard.
//
// The caller never inspects the *api.Problem's code/message — just
// renders it via api.WriteProblem. Pinned by TestGetBuildSbom_*
// in handlers_ext_test.go.
func (s *server) resolveSbomPath(r *http.Request, buildID string, acct state.Account) (string, *api.Problem) {
	notFound := func() (string, *api.Problem) {
		return "", api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such build")
	}
	build, err := s.store.BuildByID(r.Context(), buildID)
	if err != nil {
		return notFound()
	}
	dep, err := s.store.DeploymentByID(r.Context(), build.DeploymentID)
	if err != nil {
		return notFound()
	}
	app, err := s.store.AppByID(r.Context(), dep.AppID)
	if err != nil || app.AccountID != acct.ID {
		return notFound()
	}
	prov, err := s.store.BuildProvenanceByBuildID(r.Context(), build.ID)
	if err != nil {
		// Provenance row absent — pre-PR build.
		return "", api.ErrBuildSBOMUnavailable()
	}
	if prov.SBOMStorageKey == "" {
		// Populator didn't stamp sbom_storage_key.
		return "", api.ErrBuildSBOMUnavailable()
	}
	if s.sbomRoot == "" {
		// Operator hasn't wired a SBOM root.
		return "", api.ErrBuildSBOMUnavailable()
	}
	// Path-traversal guard: storage key MUST be a relative path
	// under sbomRoot — no leading "/" or ".." segments. imaged's
	// syft populator enforces a fixed "sboms/<buildID>.cdx.json"
	// shape but the column is general-purpose, so re-validate here.
	clean := filepath.Clean(prov.SBOMStorageKey)
	if strings.HasPrefix(clean, "/") || strings.HasPrefix(clean, "..") || clean == "." {
		return "", api.ErrBuildSBOMUnavailable()
	}
	return filepath.Join(s.sbomRoot, clean), nil
}

// policyPtrFromReq converts the wire DTO `*api.ScalingPolicy` to the
// state-layer `*state.ScalingPolicy`. Returns nil when the DTO is
// nil (the "don't touch the column" case — UpdateAppParams.Set bit
// is the canonical signal). The conversion is a struct-by-struct
// copy because the two types must NOT alias. With the pointer-
// Target shape on both sides the inner field copies 1:1.
//
// `req.SetScalingPolicy` (the boolean bit on UpdateAppRequest) is
// the load-bearing signal: `req.ScalingPolicy == nil` with
// Set=false means "don't touch the column"; `req.ScalingPolicy ==
// nil` with Set=true means "explicit zero policy" (which is the
// canonical "scale to zero" form). The handler's caller contract
// pins Set=true whenever a non-nil policy is supplied; the
// validateUpdateApp gate ensures the in-memory state is consistent.
func policyPtrFromReq(req *api.UpdateAppRequest) *state.ScalingPolicy {
	if req == nil || req.ScalingPolicy == nil {
		return nil
	}
	sp := req.ScalingPolicy
	out := &state.ScalingPolicy{
		MinInstances:      sp.MinInstances,
		MaxInstances:      sp.MaxInstances,
		ScaleOutCooldownS: sp.ScaleOutCooldownS,
		ScaleInCooldownS:  sp.ScaleInCooldownS,
	}
	if sp.Target != nil {
		out.Target = &state.ScalingTarget{
			Metric: sp.Target.Metric,
			Value:  sp.Target.Value,
		}
	}
	return out
}
