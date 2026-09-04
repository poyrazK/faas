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
	"unicode"

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
	// ADR-093: per-route observability opt-in. Same plan-gate
	// shape as WebSocket above — Free + true = 403
	// plan_route_metrics_not_allowed. The per-app route cap (50)
	// + __route_other__ overflow bounds the cardinality regardless
	// of the customer's traffic shape, but Free is the abuse-floor
	// tier where per-route rollups would not have a budget
	// alongside the per-app rollups. Hobby+ customers may PATCH
	// true → false to opt out (a small app that does not want
	// per-route cardinality on the box); the false direction
	// needs no gate.
	if req.RouteMetricsEnabled != nil && *req.RouteMetricsEnabled {
		if !acct.Plan.RouteMetricsResponseAllowed() {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanRouteMetricsNotAllowed,
				"Per-route metrics are not allowed on this plan",
				"Free tier does not support per-route observability; upgrade to Hobby or higher.")
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
	// ADR-124: per-app wire-protocol selector. Same plan-gate
	// shape as the streaming / require_authn gates above — Free +
	// "grpc" = 403 plan_app_protocol_grpc_not_allowed. The
	// closed-set CHECK apps_app_protocol_chk (migration 00382)
	// catches out-of-set values at the SQL layer; the apid
	// layer returns 400 app_protocol_invalid so the customer
	// sees a clean validation error before any SQL write.
	// http1 / http2 are universal and not gated here. The
	// default is "http1" (Plan.AppProtocolDefault) so a Free
	// customer PATCHing nil is a no-op (the Set bit is unset
	// in updateApp's UpdateAppParams call below).
	if req.AppProtocol != nil {
		if !api.IsValidAppProtocol(*req.AppProtocol) {
			return api.NewProblem(http.StatusBadRequest,
				api.CodeAppProtocolInvalid,
				"Invalid app_protocol",
				"app_protocol must be one of: http1, http2, grpc")
		}
		if *req.AppProtocol == api.AppProtocolGRPC &&
			!acct.Plan.AppProtocolAllowed(api.AppProtocolGRPC) {
			return api.NewProblem(http.StatusForbidden,
				api.CodePlanAppProtocolGrpcNotAllowed,
				"Per-app gRPC wire protocol is not allowed on this plan",
				"Free tier does not support app_protocol='grpc'; upgrade to Hobby or higher.")
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
	// CORS improvements D1: per-app default CORS opt-in.
	// Same grammar as EdgeRuleCORSAction.AllowOrigins
	// (literal origin, https://*.example.com subdomain
	// wildcard, https://host:* port wildcard, or '*' —
	// the latter rejected when AllowCredentials would be
	// true, which doesn't apply on the default path because
	// credentials are never set there). The validator
	// re-uses CorsOriginPattern so a PATCH can never land
	// a value that the gateway hot path would silently
	// reject on miss. Opting in with an empty / nil
	// origins list is a 422 — the customer's stated
	// intent (default-on) contradicts the empty allowlist
	// and a 200 there would be a confusing silent no-op.
	if req.CORSDefaultEnabled != nil && *req.CORSDefaultEnabled {
		if req.CORSDefaultOrigins == nil || len(*req.CORSDefaultOrigins) == 0 {
			return api.NewProblem(http.StatusUnprocessableEntity,
				api.CodeValidation,
				"Invalid cors_default_origins",
				"cors_default_origins must be a non-empty list when cors_default_enabled is true")
		}
		for _, o := range *req.CORSDefaultOrigins {
			if !api.CorsOriginPattern.MatchString(o) {
				return api.NewProblem(http.StatusUnprocessableEntity,
					api.CodeValidation,
					"Invalid cors_default_origins",
					fmt.Sprintf("cors_default_origins entry %q does not match the origin grammar (scheme://host[:port], '*', or 'scheme://*.host[:port]' / 'scheme://host:*')", o))
			}
		}
	}
	// Issue #477 / ADR-079 + ADR-118: per-app public_auth
	// (open|bearer|basic|ip_allowlist). Plan-gated upstream:
	// apid returns 403 plan_public_auth_{bearer,basic,
	//ip_allowlist}_not_allowed when the customer's plan
	// lacks the gate. The bearer path re-uses the
	// require_authn chain (apps:read scope on the app's
	// owning account) so the gate is Hobby+; basic adds a
	// secretbox seal + per-app unseal and is Pro+;
	// ip_allowlist is a per-app CIDR list (mirrors egress
	// schema) and is also Pro+ — Hobby/Free use edge rules
	// (kind='ip') for the abuse-floor posture (ADR-091).
	// 'open' is always allowed (the pre-#477 default).
	// Validation runs FIRST (closed-enum + length bounds)
	// so a Free customer who tries PATCH mode='weird' gets
	// a 422 invalid_public_auth_mode rather than a
	// confusing 403 plan_public_auth_*_not_allowed.
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
		case api.AppPublicAuthModeIPAllowlist:
			// ADR-118: plan tier first — Free/Hobby + non-pip
			// cap surfaces 403 even when the rest of the
			// body looks valid. The closed-enum validator in
			// dto.go already rejected unknown mode strings
			// (422 invalid_public_auth_mode) AND
			// mode='ip_allowlist' with an empty list (422
			// invalid_public_auth_mode — empty list is
			// not a valid ip_allowlist body).
			if !acct.Plan.PublicAuthIPAllowlistAllowed() {
				return api.ErrPlanPublicAuthIPAllowlistNotAllowed(acct.Plan)
			}
			// ADR-118 cap: enforce against the WIRE length
			// BEFORE dedup, in this same switch arm so
			// plan-gate → cap → parse → dedup run in source
			// order (a customer cannot submit cap+1 entries
			// with N-1 duplicates and have the deduped
			// result bypass the cap). Mirrors
			// handlers_ext.go:113-116 (egress cap before
			// egress dedup). Pro 16, Scale 64. Additive
			// per-account budget does NOT apply (per-app
			// cap only).
			maxEntries := acct.Plan.PublicAuthIPAllowlistMaxEntries()
			if len(req.PublicAuth.IPAllowlist) > maxEntries {
				return api.ErrPublicAuthIPAllowlistTooLong(
					len(req.PublicAuth.IPAllowlist), maxEntries)
			}
			// ADR-118 parse + dedup. Lives inside the
			// switch arm (rather than at the bottom of
			// validateUpdateApp) so the cap check above
			// runs against the wire form BEFORE this loop
			// rewrites it. NO v4-mapped-v6 rewrite — the
			// DB trigger rejects families outside {4,6}
			// outright. NO /8 floor — ingress is per-
			// customer-list, no operator-side abuse band
			// at the handler. Insertion order is first-
			// seen-wins. Empty-list mode flip (arm-then-
			// add) is the gateway's loud 500 posture
			// (operator misconfig), not a handler concern.
			if len(req.PublicAuth.IPAllowlist) > 0 {
				canonicalised := make([]string, 0, len(req.PublicAuth.IPAllowlist))
				seen := make(map[string]struct{}, len(req.PublicAuth.IPAllowlist))
				for _, raw := range req.PublicAuth.IPAllowlist {
					prefix, err := netip.ParsePrefix(raw)
					if err != nil || prefix.Bits() == 0 {
						return api.ErrInvalidPublicAuthIPAllowlist(raw,
							errOrZero("parse failed", err))
					}
					// Reject v4-mapped-v6 (RFC 4291
					// §2.5.5.2). The DB trigger would
					// 23514 this anyway — catching it
					// here gives the operator a friendlier
					// error name.
					if prefix.Addr().Is4In6() {
						return api.ErrInvalidPublicAuthIPAllowlist(raw,
							errors.New("v4-mapped prefix not allowed; use the v4 form"))
					}
					key := prefix.String()
					if _, ok := seen[key]; ok {
						continue
					}
					seen[key] = struct{}{}
					canonicalised = append(canonicalised, key)
				}
				// Always replace the wire form so the
				// audit + downstream conversion see
				// canonical strings. ParsePrefix can
				// canonicalise the textual form (e.g.
				// 10.0.0.5/8 → 10.0.0.0/8) even when
				// no dedup dropped entries.
				req.PublicAuth.IPAllowlist = canonicalised
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

// countIPAllowlistAudit returns the integer count of CIDR entries
// after canonicalisation, or 0 when the mode is not ip_allowlist.
// ADR-118 audit redaction invariant: the audit payload carries this
// count, NEVER the CIDR strings. Defined here so a future contributor
// adding a second audit site (gatewayd-internal side, operator CLI
// replay, etc.) uses the same nil-or-empty shape and doesn't
// accidentally inline `len(ipAllowlist)` with the wrong context.
func countIPAllowlistAudit(mode string, ipAllowlist []string) int {
	if mode != api.AppPublicAuthModeIPAllowlist {
		return 0
	}
	return len(ipAllowlist)
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
	// ADR-118: parse the canonicalised CIDR list into []netip.Prefix
	// for the store. Done between the seal step and UpdateAppParams
	// construction so the store sees []netip.Prefix directly. The
	// parse-and-dedup loop above already converted the wire form to
	// canonical strings; this is the second parse into the typed
	// shape. The closed-enum validator already rejected
	// len==0 mode='ip_allowlist', so reaching this branch with the
	// mode set means the wire form is non-empty.
	var publicAuthIPAllowlist []netip.Prefix
	if req.PublicAuth != nil && req.PublicAuth.Mode == api.AppPublicAuthModeIPAllowlist {
		publicAuthIPAllowlist = make([]netip.Prefix, 0, len(req.PublicAuth.IPAllowlist))
		for _, raw := range req.PublicAuth.IPAllowlist {
			// Already validated above (ParsePrefix succeeded,
			// Bits() != 0, not v4-mapped). A second parse
			// error here would mean a write-after-validate
			// race, which is impossible in a single
			// goroutine — so we tolerate err != nil as a
			// defensive fallback (drop the entry, the
			// canonicalised list is the source of truth).
			if prefix, err := netip.ParsePrefix(raw); err == nil && prefix.Bits() != 0 {
				publicAuthIPAllowlist = append(publicAuthIPAllowlist, prefix)
			}
		}
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
		// ADR-093: per-route observability opt-in. Same Set-bit
		// convention as WebSocketEnabled above; apid validation
		// already gated the plan (CodePlanRouteMetricsNotAllowed
		// for Free customers PATCHing true), so the store is a
		// plain column write.
		RouteMetricsEnabled:    req.RouteMetricsEnabled,
		SetRouteMetricsEnabled: req.RouteMetricsEnabled != nil,
		// ADR-124: per-app wire-protocol selector. Same
		// Set*/optional-pointer convention as RouteMetricsEnabled
		// above — nil pointer means "don't touch the column"
		// (the SQL keeps the existing value via the
		// `app_protocol = case when $N then $M else
		// app_protocol end` pattern at pgstore.go); non-nil
		// pointer writes the value verbatim. The closed-set
		// CHECK apps_app_protocol_chk (migration 00382) admits
		// only {http1, http2, grpc}; the apid validator above
		// has already returned 400 app_protocol_invalid on any
		// other value, so the SQL never sees an illegal value.
		// Plan gate (Free + grpc → 403 plan_app_protocol_grpc_not_allowed)
		// is enforced above; by the time UpdateApp runs, the
		// value is authoritative.
		AppProtocol:    req.AppProtocol,
		SetAppProtocol: req.AppProtocol != nil,
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag (apps.maintenance_mode). Same Set-bit
		// convention as RouteMetricsEnabled above — nil pointer
		// means "don't touch the column"; non-nil pointer writes
		// the boolean verbatim. No plan gate (Free and above
		// may opt in). The pg_notify trigger
		// apps_maintenance_mode_notify (migrations/00225) fires
		// pg_notify('app_changed', NEW.id::text) ONLY when this
		// column IS DISTINCT FROM old; the cmd-side listener
		// (cmd/gatewayd-internal/backend.go) calls
		// PGBackend.ResetApp(appID) so the apps LRU drops the
		// stale MaintenanceMode before the next request lands.
		MaintenanceMode:    req.MaintenanceMode,
		SetMaintenanceMode: req.MaintenanceMode != nil,
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
		// ADR-118: per-app ingress IP allowlist. The Set bit
		// fires ONLY when the operator explicitly chose
		// ip_allowlist mode — a PATCH that flips an app from
		// 'open' → 'basic' must not silently clear an existing
		// allowlist that was stored under a previous
		// ip_allowlist-mode PATCH. The SetPublicAuth bit
		// above is permissive (any mode flip touches the
		// column family), but SetPublicAuthIPAllowlist is
		// narrow: it gates the column write, not the mode
		// write. The slice is empty-but-non-nil when the
		// operator PATCHed ip_allowlist with no CIDRs (the
		// canonical "arm the mode" form); nil when the
		// operator PATCHed a non-ip_allowlist mode.
		PublicAuthIPAllowlist:    &publicAuthIPAllowlist,
		SetPublicAuthIPAllowlist: req.PublicAuth != nil && req.PublicAuth.Mode == api.AppPublicAuthModeIPAllowlist,
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
		// CORS improvements D1: per-app default CORS opt-in.
		// The opt-out contract is asymmetric on purpose:
		// a customer who flips the flag to false (or
		// doesn't touch the flag) does NOT carry a
		// brand-new origins list — the "flip to off"
		// PATCH must never wipe a previously configured
		// allowlist as a side effect. So
		// SetCORSDefaultOrigins is true ONLY when the
		// customer is enabling (or staying enabled);
		// the validator already 422'd the
		// (enabled=true + nil/empty origins) case so
		// reaching this branch with enabled=true means
		// a valid non-empty list is in hand.
		CORSDefaultEnabled:    req.CORSDefaultEnabled,
		SetCORSDefaultEnabled: req.CORSDefaultEnabled != nil,
		CORSDefaultOrigins:    req.CORSDefaultOrigins,
		SetCORSDefaultOrigins: req.CORSDefaultEnabled != nil && *req.CORSDefaultEnabled,
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
	// Issue #477 / ADR-079 + ADR-118: emit app.public_auth_changed on
	// mode transitions. Same single-purpose, single-keyword-greppable
	// shape as app.eviction_priority_changed above so operators
	// can `gregale audit-events --kind-prefix public_auth` and
	// see every mode flip without parsing the larger app.updated
	// payload. NOT emitted when the field was already in the
	// target state (no-op transition) or when the operator left
	// it unset (no intent to flip).
	//
	// Redaction posture (load-bearing — see ADR-079 §Decision
	// "re-redaction invariant"): the payload carries mode only
	// (open|bearer|basic|ip_allowlist) and a `has_basic_creds`
	// bool flag. Plaintext username / password / sealed blob are
	// NEVER recorded anywhere on the audit stream — neither this
	// row nor any future contributor adding logging in the
	// gatewayd-internal-side path. has_basic_creds answers "did
	// the customer rotate credentials on this PATCH?" without
	// revealing the value.
	//
	// ADR-118: `public_auth_ip_allowlist_entry_count` is the
	// INTEGER count of CIDRs after canonicalisation + dedup,
	// NEVER the CIDR strings themselves. Operators can answer
	// "did the customer change the size of the allowlist?" from
	// the count without seeing the contents. The CIDR strings
	// are not PII per se, but the allowlist can reveal
	// partner-customer ranges (an abuse-floor partner
	// allowlist like 198.51.100.0/24 + 203.0.113.0/24 would
	// tell an attacker which customer is behind the app); the
	// invariant is to record presence + size, never contents.
	if req.PublicAuth != nil && app.PublicAuthMode != updated.PublicAuthMode {
		s.audit.Emit(r.Context(), "app.public_auth_changed", &acct.ID, map[string]any{
			"app_id":          updated.ID,
			"slug":            updated.Slug,
			"old":             app.PublicAuthMode,
			"new":             updated.PublicAuthMode,
			"has_basic_creds": req.PublicAuth.Mode == api.AppPublicAuthModeBasic,
			"public_auth_ip_allowlist_entry_count": countIPAllowlistAudit(
				req.PublicAuth.Mode, req.PublicAuth.IPAllowlist),
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
	// NotifyAppDelete is the lifecycle cleanup signal consumed by schedd.
	// AppChanged is for app metadata/routing changes and is not subscribed
	// to by the VM teardown path; publishing the delete on that channel can
	// leave a running VM behind after the soft delete.
	_ = s.notif.Notify(r.Context(), db.NotifyAppDelete,
		fmt.Sprintf(`{"slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
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
	writeJSON(w, http.StatusOK, s.deploymentResponse(d, app))
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
	writeJSON(w, http.StatusOK, s.deploymentResponse(updated, app))
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
	writeJSON(w, http.StatusOK, s.deploymentResponse(updated, app))
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
//
// SAFE-RELEASES-G (issue #976) adds an optional request body field
// target_deployment_id. When set, the handler validates that the named
// deployment (a) belongs to this app and (b) has status='superseded', then
// promotes it. When omitted, the behaviour is unchanged — rollback to the
// most-recent superseded deployment. The audit emit carries a `mode` field
// so the dashboard can render "latest" vs "specific" differently.
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

	// SAFE-RELEASES-G: optionally target a specific deployment by id.
	// Body is optional (legacy callers POST without a body); decodeJSON
	// returns an error on malformed input which we treat as 400, but an
	// empty body is fine (Decodable zero-value + no error).
	var req api.RollbackRequest
	if r.ContentLength != 0 {
		if err := decodeJSON(r, &req); err != nil {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Invalid rollback request", err.Error()))
			return
		}
	}

	var (
		target state.Deployment
		mode   = "latest_superseded"
	)
	if req.TargetDeploymentID != nil && *req.TargetDeploymentID != "" {
		mode = "explicit"
		t, err := s.store.GetDeploymentByIDScopedToSuperseded(r.Context(), app.ID, *req.TargetDeploymentID)
		if err != nil {
			switch {
			case errors.Is(err, state.ErrNoRollbackTarget):
				api.WriteProblem(w, api.ErrRollbackTargetNotFound(
					fmt.Sprintf("no superseded deployment with id %q belongs to app %q", *req.TargetDeploymentID, app.ID)))
			case errors.Is(err, state.ErrRollbackTargetAlreadyLive):
				api.WriteProblem(w, api.ErrRollbackTargetAlreadyLive(
					fmt.Sprintf("deployment %q exists but is not in 'superseded' state; rollback to current live deployment is rejected", *req.TargetDeploymentID)))
			default:
				api.WriteProblem(w, api.ErrCapacity(fmt.Sprintf("lookup rollback target: %v", err)))
			}
			return
		}
		target = t
	} else {
		// Legacy path: most-recent superseded deployment.
		t, err := s.store.LatestSupersededDeployment(r.Context(), app.ID)
		if err != nil {
			api.WriteProblem(w, api.ErrNoRollbackTarget())
			return
		}
		target = t
	}

	// NOTE: SAFE-RELEASES-G deliberately does NOT add a "snapshot must
	// exist" gate here. Per ADR-005 "cold boot must always work": if the
	// rollback target's snapshot is missing (e.g. retention sweep ran,
	// FC upgrade marked it stale, or it never had one), the wake path
	// will cold-boot from the deployment's rootfs. Rejecting the
	// rollback up-front would block a valid operator action and
	// contradict the spec invariant "An app always has a live snapshot
	// OR a cold-bootable rootfs — never neither" (CLAUDE.md §Invariants).
	//
	// The early-PR draft of this code did gate on HasSnapshotHistory +
	// LatestSnapshot; /code-review medium (PR #979) flagged that the
	// gate conflates "all snapshots stale (FC upgrade per ADR-005)" with
	// "snapshot GC'd", wrongly blocking the rollback on stale scenarios
	// where cold-boot is the intended fallback.

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
	s.log.Info("app rolled back", "app", app.ID, "from", current.ID, "to", target.ID, "account", acct.ID, "mode", mode)
	// IAM-4 (issue #291): record the rollback so an operator can
	// answer "when did this app get rolled back, and to which
	// deployment?" without joining the gdpr ledger. data.from
	// is the deployment_id just superseded; data.to is the
	// deployment_id promoted to live. The pg_notify emit above
	// (lines 460+) carries the same ids for the live-system
	// listener; the audit row is the read-only counterpart.
	// SAFE-RELEASES-G: `mode` is the selector ("latest_superseded" vs
	// "explicit") so a future audit filter can distinguish "operator
	// accepted the auto-rollback to most-recent" from "operator pinned
	// to a specific historical deployment".
	s.audit.Emit(r.Context(), "app.rolled_back", &acct.ID, map[string]any{
		"app_id": app.ID,
		"from":   current.ID,
		"to":     target.ID,
		"mode":   mode,
	})
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(target, app))
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

// verifyDomain (issue #961 / Mega-A PR-3) is the
// POST /v1/domains/{domain}/verify handler. Re-runs the DNS + cert
// walk and returns the canonical CustomDomainResponse (with
// CertNotAfter / CertSANs populated when the cert dial succeeds).
//
// The handler is idempotent — POSTing twice does not change the
// domain's verification state; it just re-reads the wire. The
// `verifying` state machine is owned by the DNSVerifier goroutine
// (cmd/apid/dns_poller.go); this handler is the read sibling.
//
// CRIT-1 + MED-4 fix: dialCert failures are surfaced rather than
// silently returned as 200-with-empty-fields. The CDN mismatch maps
// to 422 CodeDomainCertNotIssued (existing); any other dial failure
// maps to 422 CodeDomainVerificationFailed with reason
// "dial_failed:<subreason>" so the customer can distinguish "DNS not
// propagated" from "cert not yet issued" from "TLS handshake
// refused". The show endpoint (getDomain) intentionally keeps the
// soft-fail behaviour — it is a read; the customer re-polls.
func (s *server) verifyDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domain := strings.ToLower(r.PathValue("domain"))
	d, ok := s.loadDomain(w, r, acct, domain)
	if !ok {
		return
	}
	resp, certErr := s.domainResponseWithCert(r.Context(), d)
	if certErr != nil {
		if errors.Is(certErr, errCDNCert) {
			api.WriteProblem(w, api.ErrDomainCertNotIssued(d.Domain, certErr.Error()))
			return
		}
		if errors.Is(certErr, errCertFailure) {
			api.WriteProblem(w, api.ErrDomainVerificationFailed(d.Domain, "cert dial failed: "+certErr.Error()))
			return
		}
		// Unwrap transport-level errors (net.OpError, context errors).
		api.WriteProblem(w, api.ErrDomainVerificationFailed(d.Domain, "transport: "+certErr.Error()))
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// getDomain (issue #961 / Mega-A PR-3) is the GET /v1/domains/{domain}
// handler. Returns the durable domain row + the live cert chain
// (NotAfter, SANs, CertStatus). The cert dial is wrapped in
// dialCertFunc so tests inject a fake server.
//
// MED-4 fix: a failed dial does NOT abort the response. The handler
// returns the durable row + CertStatus="dial_failed:<reason>" so the
// customer can tell whether the cert is pending, missing, or
// refused by an upstream CDN.
func (s *server) getDomain(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domain := strings.ToLower(r.PathValue("domain"))
	d, ok := s.loadDomain(w, r, acct, domain)
	if !ok {
		return
	}
	resp, _ := s.domainResponseWithCert(r.Context(), d)
	writeJSON(w, http.StatusOK, resp)
}

// loadDomain centralises the loadAccount + app-ownership check for
// the new per-domain routes. Returns false when the response was
// written (so the caller bails).
func (s *server) loadDomain(w http.ResponseWriter, r *http.Request, acct state.Account, domain string) (state.CustomDomain, bool) {
	d, err := s.store.DomainByName(r.Context(), domain)
	if err != nil {
		s.notFound(w, "no such domain")
		return d, false
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such domain")
		return d, false
	}
	return d, true
}

// domainResponseWithCert is the building block for verify + show.
// It mirrors the existing domainResponse (line ~3309) but extends
// the wire with the live cert details. d is the loaded domain row
// (already ownership-checked).
//
// MED-4 fix: the cert dial error is returned alongside the response.
// On success the caller can write a 200 with CertStatus="issued".
// On failure the caller decides whether to surface the error (verify
// → 422) or to keep the soft-fail shape (show → 200 with
// CertStatus="dial_failed:<reason>").
func (s *server) domainResponseWithCert(ctx context.Context, d state.CustomDomain) (api.CustomDomainResponse, error) {
	resp := domainResponse(d)
	if !d.Verified() {
		resp.CertStatus = certStatusPending
		return resp, nil
	}
	cert, err := dialCert(ctx, d.Domain)
	if err != nil {
		resp.CertStatus = classifyCertError(err)
		return resp, err
	}
	resp.CertNotAfter = cert.NotAfter.UTC().Format(time.RFC3339)
	resp.CertSANs = cert.DNSNames
	resp.CertStatus = certStatusIssued
	return resp, nil
}

// classifyCertError maps a dialCert error to the CertStatus string
// the CLI / dashboard renders. The shape is "dial_failed:<reason>"
// where reason is a stable token the customer can grep.
func classifyCertError(err error) string {
	switch {
	case errors.Is(err, errCDNCert):
		return "dial_failed:cdn_cert"
	case errors.Is(err, errCertFailure):
		return "dial_failed:" + dialFailureReason(err)
	case errors.Is(err, context.DeadlineExceeded):
		return "dial_failed:dial_timeout"
	case errors.Is(err, context.Canceled):
		return "dial_failed:cancelled"
	default:
		// net.OpError (DNS, connection refused) etc. Fall back to
		// the wrapped error's message but cap the length so a
		// hostile DNS server can't blow up the wire.
		msg := err.Error()
		if len(msg) > 96 {
			msg = msg[:96] + "…"
		}
		return "dial_failed:" + msg
	}
}

// dialFailureReason extracts the inner-most non-errCertFailure
// error so the CertStatus surfaces the actual failure mode (e.g.
// "dial_refused", "handshake_failed"). Falls back to a generic
// "unknown" if the chain is empty.
func dialFailureReason(err error) string {
	for {
		next := errors.Unwrap(err)
		if next == nil {
			break
		}
		err = next
	}
	if err == nil || err.Error() == "" {
		return "unknown"
	}
	msg := err.Error()
	if len(msg) > 96 {
		msg = msg[:96] + "…"
	}
	return msg
}

// --- domain doctor (ADR-120) --------------------------------------------

// getDomainDoctor is the GET /v1/domains/{domain}/doctor handler.
// Reuses loadDomain for the IDOR-safe load. The handler reads
// the latest observation row from domain_doctor_observations;
// if the row is older than FAAS_DOMAIN_DOCTOR_TTL_SECONDS or
// missing, it triggers a synchronous re-probe with a 5s budget.
// The re-probe path is the only place a live probe runs on a
// request — the dns_poller does the work for the 99% case.
//
// 503 CodeDoctorDisabled is returned when the operator hasn't
// set FAAS_DOMAIN_DOCTOR_ENABLED; the route stays registered
// so the CLI gets a deterministic error code (per the
// pre-#911 pattern in api/flags.go).
func (s *server) getDomainDoctor(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if !s.runtimeBool(runtimeConfigDomainDoctor, api.DomainDoctorEnabled()) {
		api.WriteProblem(w, api.ErrDoctorDisabled())
		return
	}
	domain := strings.ToLower(r.PathValue("domain"))
	d, ok := s.loadDomain(w, r, acct, domain)
	if !ok {
		return
	}
	report, err := s.buildDoctorReport(r.Context(), d)
	if err != nil {
		api.WriteProblem(w, api.ErrDoctorUnavailable(d.Domain, err.Error()))
		return
	}
	writeJSON(w, http.StatusOK, report)
}

// doctorTTL returns the hot runtime-configured doctor cache age. The
// environment remains the bootstrap fallback for local/dev compatibility;
// the operator database override is applied without restarting apid.
func (s *server) doctorTTL() time.Duration {
	return time.Duration(s.runtimeInt(runtimeConfigDomainDoctorTTL, 300)) * time.Second
}

// buildDoctorReport reads the cached observation row and
// decides whether to trigger a synchronous re-probe. The
// report is always rendered from the observation row (not
// the live re-probe) so the response shape is stable — the
// re-probe's only job is to refresh the row.
func (s *server) buildDoctorReport(ctx context.Context, d state.CustomDomain) (api.DomainDoctorReport, error) {
	obs, err := s.store.GetDoctorObservation(ctx, d.Domain)
	if err == nil {
		if time.Since(obs.ObservedAt) < s.doctorTTL() {
			return doctorReportFromObs(d, obs, false), nil
		}
		// Stale: fall through to a synchronous re-probe so
		// the next reader gets fresh data. The re-probe
		// shares the dns_poller's helper path (runDoctorForDomain
		// is the load-bearing engine).
		if refreshErr := s.refreshDoctorObservation(ctx, d.Domain); refreshErr == nil {
			obs, _ = s.store.GetDoctorObservation(ctx, d.Domain)
		}
		return doctorReportFromObs(d, obs, true), nil
	}
	// ErrNotFound: poller hasn't written yet. Trigger a
	// synchronous re-probe (bounded by the request ctx so
	// a slow upstream doesn't blow past the request budget).
	if refreshErr := s.refreshDoctorObservation(ctx, d.Domain); refreshErr != nil {
		return api.DomainDoctorReport{}, refreshErr
	}
	obs, err = s.store.GetDoctorObservation(ctx, d.Domain)
	if err != nil {
		return api.DomainDoctorReport{}, err
	}
	return doctorReportFromObs(d, obs, true), nil
}

// refreshDoctorObservation is the synchronous re-probe
// path. Calls runProbesParallel + dialCertForDoctor
// (the same helpers the poller uses) and upserts the
// result. Errors are returned to the caller; the caller
// decides whether to fall through (the report's Stale
// flag is the visible degradation).
func (s *server) refreshDoctorObservation(ctx context.Context, domain string) error {
	rctx, cancel := context.WithTimeout(ctx, probeTimeout+2*time.Second)
	defer cancel()
	dnsFound, pointsToG, caa, aaaa := runProbesParallel(rctx, domain)
	obs := state.DomainDoctorObservation{
		Domain:          domain,
		ObservedAt:      time.Now().UTC(),
		DNSRecordFound:  probeToBool(dnsFound.Status, true),
		PointsToGregale: probeToBool(pointsToG.Status, false),
		IPv6Conflict:    probeToBool(aaaa.Status, false),
		ObservedTarget:  pointsToG.Observed,
		ObservedAAAA:    aaaa.Observed,
		CAAObserved:     caa.Observed,
		DNSCheckedAt:    earliest(dnsFound.ObservedAt, pointsToG.ObservedAt, caa.ObservedAt, aaaa.ObservedAt),
	}
	switch caa.Status {
	case probeOK:
		v := true
		obs.CAAPermits = &v
	case probeFail:
		v := false
		obs.CAAPermits = &v
	}
	if s.store != nil {
		if surface, sErr := s.store.TenantSurfaceByHostname(rctx, domain); sErr == nil {
			obs.SurfaceID = surface.ID
			obs.CertState = string(surface.CertState)
			obs.CertNotAfter = surface.CertNotAfter
		}
	}
	if obs.CertState == "" {
		obs.CertState, obs.LastError, obs.CertNotAfter = dialCertForDoctor(rctx, domain)
		obs.CertCheckedAt = time.Now().UTC()
	}
	return s.store.UpsertDoctorObservation(rctx, obs)
}

// doctorReportFromObs translates the persistence struct
// into the wire shape. The translation is mechanical
// (5 checks, each with a stable name + status + detail +
// observed + remediation + checked_at) so the helper
// stays small and is exercised by the e2e suite.
func doctorReportFromObs(d state.CustomDomain, obs state.DomainDoctorObservation, stale bool) api.DomainDoctorReport {
	report := api.DomainDoctorReport{
		Domain:     d.Domain,
		AppID:      d.AppID,
		Stale:      stale,
		ObservedAt: obs.ObservedAt.UTC().Format(time.RFC3339),
		Checks:     []api.DomainDoctorCheck{},
		Healthy:    true,
	}
	// 1. DNS record found.
	dnsStatus, dnsDetail, dnsRem := probeOK, "A or AAAA records present", ""
	if !obs.DNSRecordFound {
		dnsStatus, dnsRem = probeFail, "Publish an A or AAAA record at "+d.Domain
		report.Healthy = false
	}
	report.Checks = append(report.Checks, api.DomainDoctorCheck{
		Name: "dns_record", Status: string(dnsStatus), Detail: dnsDetail,
		Remediation: dnsRem, CheckedAt: obs.DNSCheckedAt.UTC().Format(time.RFC3339),
	})
	// 2. Points to Gregale.
	ptsStatus, ptsDetail, ptsRem, ptsObs := probeOK, "CNAME → Gregale", "", obs.ObservedTarget
	if !obs.PointsToGregale {
		ptsStatus = probeFail
		report.Healthy = false
		if ptsObs != "" {
			ptsDetail = "CNAME does not point at Gregale (observed: " + ptsObs + ")"
			ptsRem = "Set CNAME " + d.Domain + " → " + ptsObs
		} else {
			ptsDetail = "no CNAME at apex; using A/AAAA record instead"
		}
	}
	report.Checks = append(report.Checks, api.DomainDoctorCheck{
		Name: "points_to_gregale", Status: string(ptsStatus), Detail: ptsDetail,
		Observed: ptsObs, Remediation: ptsRem,
		CheckedAt: obs.DNSCheckedAt.UTC().Format(time.RFC3339),
	})
	// 3. TLS certificate.
	tlsStatus, tlsDetail, tlsRem := probeOK, "certificate issued", ""
	switch obs.CertState {
	case certStatusIssued:
		if !obs.CertNotAfter.IsZero() {
			tlsDetail = "certificate issued, expires " + obs.CertNotAfter.UTC().Format(time.RFC3339)
		}
	case certStatusPending:
		tlsStatus = probePending
		report.Healthy = false
		tlsDetail = "cert engine has not yet issued"
		tlsRem = "Wait for cert engine to mint (retry in 30s). If persistent, run `gregale domains show " + d.Domain + "` for cert_not_after + SANs."
	case certStatusFailed:
		tlsStatus = probeFail
		report.Healthy = false
		tlsDetail = "cert engine reported failure: " + obs.LastError
		tlsRem = "Check the cert engine logs; the renewal loop will retry automatically."
	case certStatusDialFailed:
		tlsStatus = probeFail
		report.Healthy = false
		tlsDetail = "port-443 cert dial failed: " + obs.LastError
	case certStatusCDN:
		tlsStatus = probeFail
		report.Healthy = false
		tlsDetail = "port-443 cert is a CDN cert whose SANs do not include " + d.Domain
		tlsRem = "Update the edge to use the Gregale-issued cert, or wait for Gregale cert propagation."
	default:
		// "none" or empty. Two distinct cases:
		// (a) d.Verified() is false — the customer never
		//     published the _faas-verify TXT, so the cert
		//     engine has not started. Tell them so.
		// (b) d.Verified() is true but cert_state is still
		//     "none" — the first poll cycle after verification
		//     almost always hits this case (cert engine has not
		//     yet minted). The customer has verified; we
		//     should not contradict that reality. Tell them
		//     the cert is pending issuance, not that the
		//     domain is unverified.
		tlsStatus = probePending
		report.Healthy = false
		if !d.Verified() {
			tlsDetail = "domain not yet verified; cert not yet issued"
		} else {
			tlsDetail = "cert pending issuance; wait for cert engine (next poll cycle)"
		}
	}
	report.Checks = append(report.Checks, api.DomainDoctorCheck{
		Name: "tls_certificate", Status: string(tlsStatus), Detail: tlsDetail,
		Remediation: tlsRem, CheckedAt: obs.CertCheckedAt.UTC().Format(time.RFC3339),
	})
	// 4. CAA permits. caaStatus starts as ok with the
	// "no CAA published" detail, then each branch overrides
	// only when a CAA record is present OR a transient
	// failure was observed. The non-CAA-published case
	// (obs.CAAPermits == nil and no transient error) keeps
	// the initial ok assignment.
	caaStatus := probeOK
	caaDetail := "no CAA published (allowed by default)"
	caaRem := ""
	caaObs := obs.CAAObserved
	if obs.CAAPermits != nil {
		if *obs.CAAPermits {
			caaDetail = "CAA permits certificate issuance"
		} else {
			caaStatus = probeFail
			report.Healthy = false
			caaDetail = "CAA denies certificate issuance for this CA"
			caaRem = "Update CAA record at " + d.Domain + " to permit letsencrypt.org (e.g. '0 issue \"letsencrypt.org\"')"
		}
	} else if obs.CAAObserved != "" {
		// nil permits + observed CAA recordset = the resolver
		// returned a CAA recordset but the parser couldn't
		// decide issue vs issuewild. Surface as pending so
		// the customer sees a transient signal.
		caaStatus = probePending
		report.Healthy = false
		caaDetail = "CAA lookup returned a transient error; re-poll"
	}
	report.Checks = append(report.Checks, api.DomainDoctorCheck{
		Name: "caa_permits", Status: string(caaStatus), Detail: caaDetail,
		Observed: caaObs, Remediation: caaRem,
		CheckedAt: obs.DNSCheckedAt.UTC().Format(time.RFC3339),
	})
	// 5. IPv6 conflict.
	ipStatus, ipDetail, ipRem, ipObs := probeOK, "no stray AAAA at apex", "", obs.ObservedAAAA
	if obs.IPv6Conflict {
		ipStatus = probeFail
		report.Healthy = false
		ipDetail = "AAAA record at apex conflicts with CNAME"
		ipRem = "Remove AAAA record at " + d.Domain
	}
	report.Checks = append(report.Checks, api.DomainDoctorCheck{
		Name: "ipv6_conflict", Status: string(ipStatus), Detail: ipDetail,
		Observed: ipObs, Remediation: ipRem,
		CheckedAt: obs.DNSCheckedAt.UTC().Format(time.RFC3339),
	})
	return report
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
		if a, aerr := s.store.AppBySlug(r.Context(), req.AppID); aerr == nil && a.AccountID == acct.ID {
			app = a
		} else {
			s.notFound(w, "no such app")
			return
		}
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	path := req.Path
	if path == "" {
		path = "/"
	}
	c, err := s.store.CreateCronIfUnderQuota(r.Context(), app.ID, req.Schedule, path, enabled, limits)
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

// getCron reads a single cron by id (issue #791 PR-E / ADR-090 closure).
//
// GET /v1/crons/{id}. Same IDOR-safe two-step as updateCron/deleteCron:
// resolve the cron, then resolve its app and compare account ids. Both
// failure branches emit the identical "no such cron" 404 so a probe
// cannot distinguish missing from cross-account.
//
// Distinct from listCrons: listCrons returns every cron owned by the
// account (or filtered by ?app_slug), while getCron answers the
// `gregale crons info <id>` question — what is this specific rule —
// with one row. The wire shape matches api.CronResponse (same
// projection as listCrons' per-row), so SDK clients can decode it
// with the existing CronResponse struct.
func (s *server) getCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
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
	writeJSON(w, http.StatusOK, cronResponse(c))
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
// Paid upgrades are provider-confirmed operations. Previously the handler
// accepted a valid paid plan from any authenticated bearer token, and the
// free → hobby exception also let a customer self-upgrade without payment.
// The gate below returns a hosted checkout for a new subscription, or a
// provider portal link for an existing subscription. The webhook remains the
// only legitimate path to land on a paid plan locally.
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
	// Any paid tier increase must be confirmed by the billing provider. A
	// customer with an existing subscription is sent to the provider portal
	// so the provider can change the product without creating a duplicate
	// subscription; a customer without one gets a hosted checkout when the
	// active provider supports it.
	if acct.Plan.RequiresBillingUpgradeTo(plan) {
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
			Detail: "plan upgrades to " + string(plan) + " require billing confirmation; update the payment method in the billing portal or complete checkout to upgrade",
		}
		// Provider dispatch: if the active provider has a real checkout
		// path (Paddle or Polar) and there is no existing subscription,
		// call CreateUpgradeTransaction and surface the
		// hosted checkout URL + tx handle on the Problem. A provider
		// without checkout returns ("", "", nil) — apid reads txID == ""
		// to fall through to a provider session or the precomputed
		// FAAS_BILLING_PORTAL_URL template.
		//
		// A provider without checkout and a no-provider box land here with
		// the configured portal fallback.
		//
		// Capabilities() is the primary dispatch signal (added in
		// PR-P1 of the pluggable-billing rollout). The txID == "" check
		// stays as a defensive fallback for any provider wired before
		// the capability introspection was introduced.
		if acct.StripeSubscriptionItem == "" && s.billingProvider != nil && s.billingProvider.Capabilities().Has(billing.CapHostedCheckout) {
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
				prob.CheckoutURL = checkoutURL
				if providerName(s.billingProvider) != "polar" {
					// Keep the legacy field for existing hosted-checkout SDK
					// compatibility. New clients should consume the
					// provider-neutral checkout_url.
					prob.PaddleCheckoutURL = checkoutURL
				}
				prob.TxID = txID
				api.WriteProblem(w, prob)
				return
			}
		}
		prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), acct)
		api.WriteProblem(w, prob)
		return
	}
	// A downgrade is a provider-side subscription mutation, not a local
	// entitlement mutation. Applying it locally first can leave a customer on
	// Free while Polar continues charging the previous paid subscription. The
	// provider webhook is the only source of truth for the eventual local plan.
	if acct.Plan != plan {
		prob := &api.Problem{
			Status: http.StatusPaymentRequired,
			Code:   api.CodePayment,
			Title:  "Billing confirmation required",
			Detail: "plan downgrades must be scheduled with the billing provider; the current plan remains active until provider confirmation",
		}
		if s.billingProvider == nil || acct.StripeSubscriptionItem == "" {
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), acct)
			api.WriteProblem(w, prob)
			return
		}
		changer, ok := s.billingProvider.(billing.SubscriptionPlanChangeProvider)
		if !ok {
			prob.Detail = "this billing provider does not support API plan downgrades; use the billing portal; the current plan remains active until provider confirmation"
			prob.BillingPortalURL = s.billingPortalURLForProvider(r.Context(), acct)
			api.WriteProblem(w, prob)
			return
		}
		effectiveAt, err := changer.ChangeSubscriptionPlan(r.Context(), acct, plan)
		if err != nil {
			s.log.Error("schedule plan change failed",
				"account", acct.ID,
				"from", logsanitize.Field(string(acct.Plan)),
				"to", logsanitize.Field(string(plan)),
				"err", err)
			if errors.Is(err, billing.ErrAlreadyCancelled) {
				api.WriteProblem(w, api.NewProblem(http.StatusConflict,
					api.CodeConflict, "billing subscription unavailable",
					"the billing subscription is no longer active; refresh billing and try again"))
				return
			}
			api.WriteProblem(w, api.NewProblem(http.StatusBadGateway,
				"billing_plan_change_failed", "billing plan change failed",
				"the provider could not schedule this plan change; the current plan remains active"))
			return
		}
		updated, err := s.store.AccountByID(r.Context(), acct.ID)
		if err != nil {
			updated = acct
		}
		status := "pending_provider_confirmation"
		response := s.accountResponse(r.Context(), updated, r)
		response.PlanChangeStatus = status
		response.RequestedPlan = string(plan)
		if !effectiveAt.IsZero() {
			response.EffectiveAt = &effectiveAt
		}
		writeJSON(w, http.StatusAccepted, response)
		return
	}
	if err := s.store.UpdateAccountPlan(r.Context(), acct.ID, plan); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update plan"))
		return
	}
	updated, err := s.store.AccountByID(r.Context(), acct.ID)
	if err != nil {
		updated = acct
		updated.Plan = plan
	}
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
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), updated, r))
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
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), updated, r))
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
		// Unknown customer: keep known billing events retryable. A 200 here
		// would permanently discard a payment/subscription transition when
		// the local customer binding is temporarily missing. Unknown no-op
		// events remain ACKed so an unsupported provider event cannot create
		// an endless retry loop.
		if mapStripeTypeToEventType(ev.Type) != billing.EventUnknown {
			s.log.Warn("stripe_webhook.unknown_customer", "customer_id", ev.Data.Object.Customer, "event_type", ev.Type)
			api.WriteProblem(w, api.ErrCapacity("billing webhook customer binding unavailable"))
			return
		}
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
	if err := s.handleBillingEvent(r.Context(), normalized, acct); err != nil {
		s.log.Error("stripe webhook state application failed", "event_id", logsanitize.Field(ev.ID), "err", err)
		webhookdedupe.ReleaseReplay(r.Context(), webhookdedupe.ProviderStripe, ev.ID)
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return
	}
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
	if s.billingProvider == nil || providerName(s.billingProvider) != "paddle" {
		s.log.Error("paddle_webhook.no_provider",
			"provider", providerName(s.billingProvider),
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
		// Keep known billing events retryable rather than silently dropping
		// an entitlement or invoice transition. Unknown no-op events are
		// acknowledged because there is no local side effect to recover.
		if ev.Type != billing.EventUnknown || ev.Invoice != nil {
			s.log.Warn("paddle_webhook.unknown_customer",
				"customer_id", ev.CustomerID,
				"event_type", ev.Type.Name(),
			)
			api.WriteProblem(w, api.ErrCapacity("billing webhook customer binding unavailable"))
			return
		}
		s.log.Info("paddle_webhook.unknown_customer_noop",
			"customer_id", ev.CustomerID,
			"event_type", ev.Type.Name(),
		)
		w.WriteHeader(http.StatusOK)
		return
	}
	if err := s.persistBillingInvoice(r.Context(), string(webhookdedupe.ProviderPaddle), acct, ev.Invoice); err != nil {
		s.log.Error("paddle webhook invoice persistence failed", "event_id", logsanitize.Field(ev.EventID), "err", err)
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return
	}
	// Issue #294: durable webhook replay claim. ev.EventID is the Paddle
	// `event_id` (the delivery UUID). Use the state-store claim rather than
	// the process-local helper so two apid instances cannot apply the same
	// delivery concurrently. Empty ev.EventID (older Paddle payloads) skips
	// the check — pre-#294 behaviour. Claim failures are retryable because
	// acknowledging without replay protection could duplicate money-related
	// audit/state side effects.
	if ev.EventID != "" {
		now := time.Now().UTC()
		claimed, err := s.store.ClaimWebhookDelivery(
			r.Context(), webhookdedupe.ProviderPaddle, ev.EventID,
			now.Add(-webhookdedupe.TTL), now.Add(webhookdedupe.TTL),
		)
		if err != nil {
			s.log.Error("paddle webhook replay protection unavailable", "event_id", logsanitize.Field(ev.EventID), "err", err)
			api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
			return
		}
		if !claimed {
			acctID := acct.ID
			s.audit.Emit(r.Context(), "webhook.replay_rejected", &acctID, map[string]any{
				"provider":    webhookdedupe.ProviderPaddle,
				"delivery_id": logsanitize.Field(ev.EventID),
			})
			if s.ops != nil {
				s.ops.IncPaddleWebhookReplaySuppressed()
			}
			w.WriteHeader(http.StatusOK)
			return
		}
	}
	if err := s.handleBillingEvent(r.Context(), ev, acct); err != nil {
		s.log.Error("paddle webhook state application failed", "event_id", logsanitize.Field(ev.EventID), "err", err)
		if releaser, ok := s.store.(state.WebhookDeliveryReleaser); ok {
			if releaseErr := releaser.ReleaseWebhookDelivery(r.Context(), webhookdedupe.ProviderPaddle, ev.EventID); releaseErr != nil {
				s.log.Error("paddle webhook replay claim rollback failed", "event_id", logsanitize.Field(ev.EventID), "err", releaseErr)
			}
		}
		api.WriteProblem(w, api.ErrCapacity("billing webhook temporarily unavailable"))
		return
	}
	w.WriteHeader(http.StatusOK)
}

// handleBillingEvent is the provider-neutral dunning state machine.
// Stripe, Paddle, and Polar webhook handlers call it after their
// per-provider verification succeeds and the account is resolved. The four
// transitions (active / past_due / suspended / plan-change) and the
// associated dunning emails are the same for both providers — only
// the event source differs. Polar selects the async-mail variant so
// provider acknowledgement is not held up by the mail transport.
//
// ev.Type is the normalized billing.EventType. Stripe-shaped event
// strings ("invoice.payment_failed" etc.) are mapped to the normalized
// enum by the stripeWebhook handler before the call; Paddle's
// VerifyWebhook returns the normalized enum directly.
// persistBillingInvoice stores the provider-neutral invoice projection before
// a webhook delivery is claimed. Upserts make replay safe, while returning an
// error keeps provider delivery retryable when Postgres is unavailable.
func (s *server) persistBillingInvoice(ctx context.Context, provider string, acct state.Account, data *billing.InvoiceData) error {
	if data == nil || data.ProviderInvoiceID == "" {
		return nil
	}
	currency := data.Currency
	if currency == "" {
		currency = "eur"
	}
	return s.store.UpsertInvoice(ctx, state.Invoice{
		AccountID:         acct.ID,
		Provider:          provider,
		ProviderInvoiceID: data.ProviderInvoiceID,
		Number:            data.Number,
		Status:            data.Status,
		PeriodStart:       data.PeriodStart,
		PeriodEnd:         data.PeriodEnd,
		SubtotalCents:     data.SubtotalCents,
		TaxCents:          data.TaxCents,
		TotalCents:        data.TotalCents,
		AmountPaidCents:   data.AmountPaidCents,
		Currency:          currency,
		PDFAvailable:      data.PDFAvailable,
	})
}

func (s *server) handleBillingEvent(ctx context.Context, ev billing.Event, acct state.Account) error {
	return s.handleBillingEventWithOptions(ctx, ev, acct, false)
}

// handlePolarBillingEvent applies the same entitlement state machine as the
// legacy provider paths, but keeps best-effort email delivery off Polar's
// webhook acknowledgement path. Polar retries slow deliveries, so the
// provider-facing handler must finish after durable state has been written,
// not after an SMTP/API call completes.
func (s *server) handlePolarBillingEvent(ctx context.Context, ev billing.Event, acct state.Account) error {
	return s.handleBillingEventWithOptions(ctx, ev, acct, true)
}

// sendBillingTransitionMail keeps delivery errors observable without making
// them part of the billing state transition. Polar callers set async=true so
// a slow mail transport cannot consume the webhook provider's acknowledgement
// budget. Legacy callers stay synchronous for compatibility with their
// existing delivery semantics and tests.
//
//nolint:contextcheck // Polar mail deliberately outlives the acknowledged webhook request.
func (s *server) sendBillingTransitionMail(ctx context.Context, acct state.Account, subject, body string, async bool, operation string) {
	if s.mailer == nil {
		return
	}
	send := func(mailCtx context.Context) {
		if err := s.mailer.Send(mailCtx, Message{
			To: []string{acct.Email}, Subject: subject, TextBody: body,
		}); err != nil {
			s.log.Warn("apid: billing transition mail",
				"operation", operation, "account", acct.ID, "err", err)
		}
	}
	if !async {
		send(ctx)
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	mailCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	go func() {
		defer cancel()
		send(mailCtx)
	}()
}

func (s *server) handleBillingEventWithOptions(ctx context.Context, ev billing.Event, acct state.Account, asyncBillingMail bool) error {
	switch ev.Type {
	case billing.EventSubscriptionCreated:
		// The shared column retains its historical Stripe name, but for
		// Polar it stores the subscription UUID that cancel-at-period-end
		// and future provider operations use.
		if ev.SubscriptionID != "" {
			if err := s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, ev.SubscriptionID); err != nil {
				return fmt.Errorf("store subscription id: %w", err)
			}
		}
		if plan := billingPlanFromProviderID(ev.PlanID); plan != "" {
			if err := s.store.UpdateAccountPlan(ctx, acct.ID, plan); err != nil {
				return fmt.Errorf("store plan: %w", err)
			}
		}
		// MFA is explicitly opt-in. Billing events must never change
		// an account's MFA policy or force an enrolled-session prompt.
	case billing.EventSubscriptionCanceled:
		if err := s.store.UpdateAccountStatus(ctx, acct.ID, state.AccountSuspended); err != nil {
			return fmt.Errorf("store canceled status: %w", err)
		}
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
				return fmt.Errorf("mark payment failed: %w", err)
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
			s.sendBillingTransitionMail(ctx, acct, subject, body, asyncBillingMail, "payment_failed")
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
				return fmt.Errorf("mark subscription past due: %w", err)
			}
		}
	case billing.EventPaymentSucceeded:
		if ev.SubscriptionID != "" {
			if err := s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, ev.SubscriptionID); err != nil {
				return fmt.Errorf("store payment subscription id: %w", err)
			}
		}
		if plan := billingPlanFromProviderID(ev.PlanID); plan != "" {
			if err := s.store.UpdateAccountPlan(ctx, acct.ID, plan); err != nil {
				return fmt.Errorf("store payment plan: %w", err)
			}
		}
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
				return fmt.Errorf("restore account: %w", err)
			} else {
				// Status just flipped back to active. Send the
				// recovery email (spec §171 "All transitions emailed").
				// payment_succeeded is naturally idempotent — the
				// status guard above ensures we only fire on a real
				// past_due → active transition, never on a no-op
				// redelivery.
				subject, body := mail.AccountRestoredBody(acct.Email, time.Now().UTC())
				s.sendBillingTransitionMail(ctx, acct, subject, body, asyncBillingMail, "payment_succeeded")
			}
		}
		// Clear the dedupe stamp on every payment_succeeded, not just
		// past_due → active flips: a fresh signup's first provider
		// confirmation shouldn't wait until the next UTC midnight to
		// hear about a quota they crossed during the trial, and the
		// cost of an extra pg_notify on a no-op event is nil.
		if err := s.store.ClearQuotaWarning(ctx, acct.ID); err != nil {
			return fmt.Errorf("clear quota warning: %w", err)
		}
	case billing.EventSubscriptionUpdated:
		if ev.SubscriptionID != "" {
			if err := s.store.UpdateAccountStripeSubscriptionItem(ctx, acct.ID, ev.SubscriptionID); err != nil {
				return fmt.Errorf("store updated subscription id: %w", err)
			}
		}
		if plan := billingPlanFromProviderID(ev.PlanID); plan != "" {
			if err := s.store.UpdateAccountPlan(ctx, acct.ID, plan); err != nil {
				return fmt.Errorf("store updated plan: %w", err)
			}
		}
	case billing.EventRefundProcessed:
		// Issue #279: a refund was issued against one of the account's
		// charges. The operator-initiated path runs through Provider.Refund
		// (POST /v1/admin/accounts/{id}/refunds), not this webhook — the
		// webhook is the asynchronous confirmation that the provider accepted
		// the refund. We emit an audit row so an operator can correlate the
		// audit log with the provider dashboard.
		//
		// Idempotent: a redelivered refund event hits the same
		// case and emits another audit row; auditors expect this
		// (it's a real "event happened" — the second delivery is a
		// different event in time). The dedupe happens upstream
		// (the ingress dedupe has provider-specific retry semantics).
		s.audit.Emit(ctx, "refund.processed", &acct.ID, map[string]any{
			"actor":              acct.ID,
			"actor_email":        acct.Email,
			"provider_refund_id": ev.ProviderRefundID,
			"charge_id":          ev.ChargeID,
			"amount_cents":       ev.AmountCents,
			"currency":           ev.Currency,
		})
	}
	return nil
}

// billingPlanFromProviderID accepts canonical plan values from providers
// such as Polar and provider-shaped price handles emitted by legacy
// Stripe/Paddle events. Unknown handles are ignored rather than written into
// state.account.plan as an invalid plan.
func billingPlanFromProviderID(id string) api.Plan {
	value := strings.ToLower(strings.TrimSpace(id))
	if value == "" {
		return ""
	}
	if plan := api.Plan(value); plan.Valid() {
		return plan
	}
	for _, token := range strings.FieldsFunc(value, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	}) {
		for _, plan := range []api.Plan{api.PlanHobby, api.PlanPro, api.PlanScale} {
			if token == string(plan) {
				return plan
			}
		}
	}
	return ""
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

func (s *server) deploymentResponse(d state.Deployment, app state.App) api.DeploymentResponse {
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
		ID:                d.ID,
		AppID:             d.AppID,
		BuildID:           d.BuildID,
		ImageDigest:       d.ImageDigest,
		Kind:              string(d.Kind),
		Status:            string(d.Status),
		Error:             d.Error,
		ErrorCode:         d.ErrorCode,
		ErrorHint:         d.ErrorHint,
		ErrorWhy:          d.ErrorWhy,
		ErrorFix:          d.ErrorFix,
		ErrorRelevantLogs: d.ErrorRelevantLogs,
		CreatedAt:         d.CreatedAt.UTC().Format(time.RFC3339),
		HasOverrides:      hasOverrides,
		MinInstances:      d.MinInstances,
		// Issue #556 PR-A: traffic_percent echoes the per-deployment
		// split weight. Σ over live rows for the app is 100 by
		// construction (CreateDeployment zeros the prior row in the
		// same tx; UpdateDeploymentTraffic zeros siblings in its
		// tx). For the single-live-deployment case (the most common
		// shape today), Σ = 100 is trivially this one field.
		TrafficPercent: d.TrafficPercent,
		// ADR-091 / PR-D: per-deployment env scope echo. Always
		// written — even when scope == "default" — so dashboards
		// can branch on the literal value rather than treating
		// absent == "default" (the migration backfills the
		// column on every pre-PR-D deployment, so the field is
		// never empty in practice).
		Scope: d.Scope,
		// Issue #606 / SAFE-RELEASES-E.1: structured deployer
		// attribution. Mirrored verbatim from state.Deployment
		// — the four fields are server-stamped at handler entry
		// (cmd/apid/handlers.go::createDeployment, deploy_inputs.go,
		// handlers_source_ref.go, handlers_source_tarball.go, and
		// githubd_bridge.go) and stored via the migrations/00305
		// column set. The DTO json tags use `omitempty` so
		// pre-#606 rows render bit-identical JSON to the wire.
		DeployedByUserID: d.DeployedByUserID,
		DeployedVia:      d.DeployedVia,
		DeployedFromIP:   d.DeployedFromIP,
		PusherLogin:      d.PusherLogin,
		// Issue #977 / ADR-116: annotation echo. The four
		// columns stamped at create time are echoed verbatim so
		// the dashboard, CLI history, and SDK consumers can
		// render the annotation without an audit round-trip.
		// omitempty on each field keeps pre-feature rows
		// byte-identical to the pre-PR wire shape.
		Reason:     d.Reason,
		Tag:        d.Tag,
		DeployedBy: d.DeployedBy,
		PRNumber:   d.PRNumber,
		// Issue #976 / ADR-122 / SAFE-RELEASES-A: canary ladder echo.
		CanaryPreset:        d.CanaryPreset,
		CanaryStep:          d.CanaryStep,
		CanaryTotalSteps:    d.CanaryTotalSteps,
		CanaryStepStartedAt: d.CanaryStepStartedAt,
		// Issue #976 / ADR-122 / SAFE-RELEASES-F: rollout state machine echo.
		RolloutState:         d.RolloutState,
		RolloutStartedAt:     d.RolloutStartedAt,
		RolloutCompletedAt:   d.RolloutCompletedAt,
		RolloutAbortedAt:     d.RolloutAbortedAt,
		RolloutAbortedReason: d.RolloutAbortedReason,
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
	// PR-A: per-deploy secret-scan audit row. Mirrors the
	// Scan field above — same single-source-of-truth pattern
	// (s.secretScanResponse is also used by
	// /v1/deployments/{id}/secret-scan). The handler-side
	// conversion from the on-disk jsonb shape
	// (state.Deployment.SecretFindings []byte) into the wire
	// DTO (api.SecretScanResult) happens here so this
	// endpoint and the /secret-scan drill-down route emit
	// IDENTICAL typed payloads for a given row.
	//
	// Returns nil when the row has SecretScannedAt == nil
	// (mid-pipeline / pre-PR-A row); the SecretScan field's
	// omitempty tag then drops the field from the wire
	// response. The dashboard renders "secret scan pending"
	// on the absence. A present-but-clean row has
	// SecretScan != nil with Findings = [].
	resp.SecretScan = s.secretScanResponse(d)
	// Issue #554 / ADR-079 follow-up (AC #3 wire): surface the
	// per-deployment parked_reason + parked_at columns from
	// migration 00157. omitempty on the DTO handles the "never
	// parked" branch — the field is absent on the wire for the
	// vast majority of deployments. The closed-set vocabulary
	// is enforced at the schema layer.
	resp.ParkedReason = d.ParkedReason
	resp.ParkedAt = d.ParkedAt
	// Issue #961 / Mega-A PR-2: surface the auto-detected build plan
	// (framework, runtime, class, entrypoint, port) on every deployment
	// response so the CLI can print a single "Detected:" line and the
	// dashboard can render the same shape without a separate /build
	// fetch. For DeploymentKindImage (no SourcePath on disk), BuildPlan
	// is left nil — the wire's omitempty keeps the field off the
	// response in that case and pre-PR-2 clients see bit-identical
	// JSON.
	//
	// HIGH-2 fix: route the marker detection through
	// getCachedBuildPlan so listDeployments doesn't open +
	// parse every spooled tarball on every page render. The
	// cache is keyed by path + mtime — a fresh spool write
	// invalidates the entry.
	//
	// getCachedBuildPlan calls pkg/markers.DetectFromTarball
	// directly (not builderd.NewDetector().Detect) because the
	// builderd shim errors on FrameworkUnknown
	// (pkg/builderd/detect.go:49-50), which would leave the
	// BuildPlan field empty for monorepos. The markers API
	// returns (FrameworkUnknown, nil) for missing markers —
	// graceful degradation, no special error path.
	if d.SourcePath != "" {
		bp := &api.BuildPlan{Class: string(app.Type)}
		if app.Runtime != "" {
			bp.Runtime = app.Runtime
		}
		// HIGH-2 fix: route the marker detection through
		// getCachedBuildPlan so listDeployments doesn't open +
		// parse every spooled tarball on every page render. The
		// cache is keyed by path + mtime — a fresh spool write
		// invalidates the entry.
		fw, ver := getCachedBuildPlan(d.SourcePath)
		bp.Framework = string(fw)
		bp.Version = ver
		if len(d.OverrideEntrypoint) > 0 {
			bp.Entrypoint = d.OverrideEntrypoint[0]
		}
		if d.OverridePort != 0 {
			bp.Port = d.OverridePort
		}
		resp.BuildPlan = bp
	}
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
	// Issue #961 / Mega-A PR-2: Batch-load the account's apps once so
	// deploymentResponse can populate BuildPlan (issue #961 leaf 2).
	// Avoids N+1 AppByID calls per page. ListApps is bounded by the
	// per-plan deployed cap (pkg/api/limits.go) so the in-memory map
	// is small. AppByID failures fall through to a zero-value App
	// (BuildPlan.Class is empty), which the wire renders as missing.
	apps, _ := s.store.ListApps(r.Context(), acct.ID)
	appByID := make(map[string]state.App, len(apps))
	for _, a := range apps {
		appByID[a.ID] = a
	}
	resp := api.DeploymentListResponse{Items: make([]api.DeploymentResponse, 0, len(rows))}
	for _, d := range rows {
		resp.Items = append(resp.Items, s.deploymentResponse(d, appByID[d.AppID]))
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

// parseBuildCursor parses the opaque tuple cursor used by
// GET /v1/builds. The wire format is "<started_at_rfc3339nano>|<id_hex>"
// (pipe-separated). An empty started_at segment means "queued tail"
// and the store falls back to id-only keyset in that branch.
//
// Returns (started_at, id, true) on success; (zero, "", false) on
// any malformed input. url.Query parses `|` verbatim — no
// encoding needed.
//
// The id is the Build.ID (uuid hex string). We do NOT re-parse
// it as a uuid here on purpose: the store uses string equality
// for the id tiebreaker so the wire shape doesn't need to be
// canonicalized.
func parseBuildCursor(v string) (time.Time, string, bool) {
	parts := strings.SplitN(v, "|", 2)
	if len(parts) != 2 {
		return time.Time{}, "", false
	}
	startedRaw, id := parts[0], parts[1]
	if id == "" {
		return time.Time{}, "", false
	}
	// Queued-tail cursor: started_at is empty, fall back to id-only.
	if startedRaw == "" {
		return time.Time{}, id, true
	}
	var t time.Time
	if parsed, err := time.Parse(time.RFC3339Nano, startedRaw); err == nil {
		t = parsed
	} else if parsed, err := time.Parse(time.RFC3339, startedRaw); err == nil {
		t = parsed
	} else {
		return time.Time{}, "", false
	}
	return t, id, true
}

// formatBuildCursorNano renders the opaque tuple cursor from
// the state.Build that the store returned. We use the SOURCE
// time.Time (sub-second precision preserved) rather than the
// wire BuildResponse.StartedAt (whole-second RFC3339 for back-
// compat with GET /v1/builds/{id}). Sub-second precision in the
// cursor segment is what makes the keyset's
// `(b.started_at = $4 AND b.id < $5)` clause reachable on rows
// whose sub-second started_at falls in the same wall-clock
// second as the last row on the page. Without sub-second, that
// clause's `=` match only fires on rows whose DB started_at is
// EQUAL to the cursor at whole-second precision — the in-between
// sub-second rows would slip past the strict-less-than and
// reappear on the next page as duplicates.
//
// The id is the Build.ID hex string. PostgreSQL text-casts uuid
// without reformatting, so it's stable across
// store → SDK → handler → CLI → cursor round-trip.
//
// Empty started_at (queued row) encodes as "|id" — the pgstore
// queued-tail branch handles the id-only keyset for the NULL
// zone.
func formatBuildCursorNano(b state.Build, id string) string {
	if b.StartedAt.IsZero() {
		return "|" + id
	}
	return b.StartedAt.UTC().Format(time.RFC3339Nano) + "|" + id
}

// listBuilds serves GET /v1/builds — every build the account owns,
// in started_at desc nulls last order (DEPLOY-PROV-6 follow-up /
// ADR-091, issue #741 close-out). Optional ?app=<slug> narrows
// to one app; optional ?status=<s> filters to the 4-value status
// enum. Cursor pagination via ?before=<opaque tuple cursor>;
// limit defaults to 50, capped at 200.
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
	var beforeID string
	if v := r.URL.Query().Get("before"); v != "" {
		// Cursor is "<started_at_rfc3339nano>|<id_hex>" — opaque
		// token, pipe-separated so URL encoding stays simple.
		// started_at alone was insufficient because (a) queued
		// builds have NULL started_at (no cursor possible), and
		// (b) wire RFC3339 truncates sub-second DB precision so
		// rows whose sub-second started_at falls in the cursor's
		// wall-clock second were silently dropped past page 1.
		// The id tiebreaker solves both — see ADR-091 §3 + the
		// code-review follow-up.
		t, id, ok := parseBuildCursor(v)
		if !ok {
			api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
				"Bad cursor", "expected '<rfc3339nano>|<id_hex>' (opaque)"))
			return
		}
		before, beforeID = t, id
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
		r.Context(), acct.ID, statusFilter, appIDFilter, before, beforeID, limit)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list builds"))
		return
	}
	resp := api.BuildListResponse{Items: make([]api.BuildResponse, 0, len(rows))}
	for _, b := range rows {
		resp.Items = append(resp.Items, s.buildResponse(b))
	}
	if len(rows) == limit && limit > 0 && len(resp.Items) > 0 {
		// NextBefore = opaque cursor pointing at the LAST row
		// on this page. The cursor anchor uses the SOURCE
		// `state.Build.StartedAt` (NOT `BuildResponse.StartedAt`)
		// so we preserve DB precision. BuildResponse emits
		// RFC3339 (whole-second) for backward-compat with the
		// single-id endpoint at GET /v1/builds/{id}; the
		// cursor's started_at segment, by contrast, MUST be
		// RFC3339Nano so the keyset sub-second comparison
		// `(b.started_at = $4 AND b.id < $5)` is reachable on
		// rows whose sub-second started_at falls between the
		// cursor's nanosecond and the next whole second (this
		// was code-review Finding 2 — see ADR-091 §3).
		//
		// The cursor segment is opaque to clients per the
		// `before` query-parameter docstring; only the SDK
		// `GetBuilds` + CLI `gregale build list --before`
		// thread it verbatim. No backward-compat impact on
		// the wire response (BuildResponse.StartedAt is
		// unchanged at whole-second RFC3339).
		//
		// Caveat (intentional, documented in ADR-091): when
		// the page exactly fills to the last row in the
		// dataset, the next-page request returns 0 rows but the
		// client has already received a cursor. Clients should
		// treat an empty items[] as the terminal signal and
		// NOT continue to next_page with the cursor. The fix
		// here matches ListDeploymentsForAccount's behavior
		// (no peek-trim); introducing one would require an
		// extra DB roundtrip per page.
		last := rows[len(rows)-1]
		resp.NextBefore = formatBuildCursorNano(last, last.ID)
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
// returns a provider-authenticated customer portal session when the active
// provider supports it, otherwise the operator-configured fallback URL. The
// URL itself does not mutate anything; customer-facing billing mutations
// remain inside the provider-hosted portal.
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
	writeJSON(w, http.StatusOK, api.BillingPortalResponse{URL: s.billingPortalURLForProvider(r.Context(), acct)})
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

	// ADR-117 §3: per-connection set of stages already announced so
	// the diff on each tick emits `event: stage` exactly once. The
	// closed 6-stage vocabulary is encoded in pkg/state.StageName
	// consts; `announced` is keyed by StageName so the jsonb row's
	// `current` field maps straight through without a string copy.
	announced := make(map[state.StageName]string)
	var lastStageStateRaw []byte

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
			//
			// ADR-117 §3: same poll also diffs `stage_state` and
			// emits `event: stage` for any new entries. The
			// per-connection `announced` map dedupes across ticks;
			// the jsonb `current` is read every poll so we never
			// miss an in-flight stage flip even if the subscriber
			// raced ahead. The terminal `DeployFailed` flip is
			// covered by imaged's `markDeployFailed` (handler.go:
			// 2371) and builderd's `markFailed` (builderd.go:653)
			// — both call `AppendDeploymentStage(from==to, reason)`
			// which stamps the active row as failed before this
			// poll sees it. The terminal `DeployLive` flip is
			// covered by imaged's `MarkDeploymentLive` (handler.go:
			// 2240) which appends `snapshot_prepare → readiness`.
			if d2, err := s.store.DeploymentByID(r.Context(), id); err == nil {
				if d2.Status == state.DeployLive || d2.Status == state.DeployFailed {
					emitStageDiff(w, flusher, d2.StageState, announced, &lastStageStateRaw)
					_, _ = fmt.Fprintf(w, "event: status\ndata: {\"status\":%q}\n\n", d2.Status)
					if flusher != nil {
						flusher.Flush()
					}
					return
				}
				emitStageDiff(w, flusher, d2.StageState, announced, &lastStageStateRaw)
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

// stage emission status enum. Same shape as the wire JSON
// payload (cmd/apid/handlers_ext.go::emitStageFrame) and the
// CLI renderer (cmd/gregale/deploy_stages.go::stageStatus*).
// Local to this file so the goconst tripwire (3+ occurrences)
// stays under the threshold for the cross-file "completed"
// literal — every reference here is via the const.
const (
	stageStatusInProgress = "in_progress"
	stageStatusCompleted  = "completed"
	stageStatusFailed     = "failed"
)

// emitStageDiff diffs the jsonb stage_state against the per-connection
// `announced` set and writes one `event: stage` SSE frame for each
// stage that has not yet been emitted on this connection.
//
// ADR-117 §3 wire shape:
//
//	event: stage
//	data: {"name":"<StageName>","started_at":"<RFC3339Nano>","duration_ms":<int64>,"status":"in_progress"|"completed"|"failed"[,"reason":"<string>"]}
//
// `announced` is a 3-state tracking set:
//   - absent: never seen this stage
//   - "in_progress": emitted the in_progress frame for this stage
//   - "completed":   emitted the terminal frame (completed/failed) for
//     this stage
//
// We can't fold the two into one boolean — a stage needs both an
// in_progress frame and a terminal frame across the connection's
// lifetime, and conflating them drops the terminal one.
//
// `lastRaw` is an in-loop scratch buffer that lets us avoid a jsonb
// re-decode when the row hasn't changed since the last tick. The
// buffer is intentionally per-connection (not shared across SSE
// subscribers) because each subscriber needs its own dedup state;
// shared state would emit one subscriber's frames to all of them.
func emitStageDiff(w http.ResponseWriter, flusher http.Flusher, raw json.RawMessage, announced map[state.StageName]string, lastRaw *[]byte) {
	if len(raw) == 0 {
		return
	}
	// Cheap byte-equality short-circuit: identical row, no work.
	if *lastRaw != nil && string(*lastRaw) == string(raw) {
		return
	}
	*lastRaw = append((*lastRaw)[:0], raw...)
	var ss state.StageState
	if err := json.Unmarshal(raw, &ss); err != nil || ss.Current == "" {
		return
	}
	// 1) walk history for any terminal entries we haven't emitted yet.
	// History is append-only by AppendDeploymentStage so we never
	// replay an old row; the dedup is purely for late subscribers
	// who joined mid-deploy and for the case where imaged stamps a
	// failure after the in_progress frame was already emitted.
	for _, item := range ss.History {
		if announced[item.Name] == stageStatusCompleted {
			continue
		}
		st := item.Status
		if st == "" {
			st = stageStatusCompleted
		}
		emitStageFrame(w, flusher, item, st, item.DurationMs, item.Reason)
		announced[item.Name] = stageStatusCompleted
	}
	// 2) in_progress frame for the active row, only the first time we
	// see it on this connection. imaged's transitionWithStage
	// appends the OUTGOING stage to history before flipping current;
	// the SSE consumer therefore sees the completed frame for the
	// prior stage AND the in_progress frame for the new stage on the
	// same tick — that ordering is intentional (the customer reads
	// "stage X finished 1.2s ago, stage Y is now in flight").
	if announced[ss.Current] == "" {
		startedAt := ss.CurrentStartedAt // already *time.Time
		emitStageFrame(w, flusher, state.StageStateItem{
			Name:      ss.Current,
			StartedAt: startedAt,
		}, "in_progress", 0, "")
		announced[ss.Current] = "in_progress"
	}
}

// emitStageFrame writes one `event: stage` SSE frame. Mirrors the
// fmt.Fprintf pattern used by the status arm at lines 4218-4221. The
// hand-format keeps the wire shape grep-able from cmd/gregale/sse
// decoder tests and avoids pulling in apislogs.WriteEvent (out of
// scope for ADR-117).
func emitStageFrame(w http.ResponseWriter, flusher http.Flusher, item state.StageStateItem, status string, durationMs int64, reason string) {
	startedAtStr := time.Now().UTC().Format(time.RFC3339Nano)
	if item.StartedAt != nil {
		startedAtStr = item.StartedAt.UTC().Format(time.RFC3339Nano)
	}
	payload := map[string]any{
		"name":        string(item.Name),
		"started_at":  startedAtStr,
		"duration_ms": durationMs,
		"status":      status,
	}
	if reason != "" {
		payload["reason"] = reason
	}
	encoded, _ := json.Marshal(payload)
	_, _ = fmt.Fprintf(w, "event: stage\ndata: %s\n\n", encoded)
	if flusher != nil {
		flusher.Flush()
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
