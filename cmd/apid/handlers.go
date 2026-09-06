package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/meter"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) whoami(w http.ResponseWriter, r *http.Request, acct state.Account) {
	writeJSON(w, http.StatusOK, s.accountResponse(r.Context(), acct, r))
}

// nilStringPtr returns a *string pointing at the given value, or
// nil when s is "". Lets the create-time path stamp an
// optional UUID onto state.App without leaking a string-shaped
// sentinel into the column (Tier A10 / ADR-088).
func nilStringPtr(s string) *string {
	if s == "" {
		return nil
	}
	cp := s
	return &cp
}

// resolveOverflowNode resolves a wire `overflow_node` value (a
// compute_nodes.name) to the underlying UUID. Returns the UUID
// string (or "" when unset/empty) and a *Problem on rejection
// (Tier A10 / ADR-088). The resolver is shared between
// createApp + validateUpdateApp so the wire contract is one
// source of truth.
//
//   - wire == nil     → ("", nil)              — no preference
//   - wire == ""      → ("", nil) if !strict   — at PATCH time
//     → ("", prob) if strict   — at CREATE time
//     (no "clear" path for a fresh row)
//   - wire non-empty  → Store.ComputeNodeByName(name)
//     · ErrNotFound  → 422 invalid_overflow_node
//     · active=false → 422 invalid_overflow_node
//     · ok           → (row.ID, nil)
func (s *server) resolveOverflowNode(ctx context.Context, wire *string, strictEmpty bool) (string, *api.Problem) {
	if wire == nil {
		return "", nil
	}
	if *wire == "" {
		if strictEmpty {
			return "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeInvalidOverflowNode,
				"Invalid overflow_node",
				"overflow_node = '' is not allowed at create-time; the column starts NULL. Use a real compute_node.name or omit the field to leave the preference unset.")
		}
		return "", nil
	}
	row, err := s.store.ComputeNodeByName(ctx, *wire)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			return "", api.ErrInvalidOverflowNode(*wire)
		}
		return "", api.NewProblem(http.StatusInternalServerError, api.CodeCapacity,
			"Capacity", fmt.Sprintf("lookup overflow_node %q: %v", *wire, err))
	}
	if !row.Active {
		return "", api.NewProblem(http.StatusUnprocessableEntity, api.CodeInvalidOverflowNode,
			"Invalid overflow_node",
			fmt.Sprintf("compute_node %q exists but is active=false; pick an active peer.", *wire))
	}
	return row.ID, nil
}

func (s *server) listApps(w http.ResponseWriter, r *http.Request, acct state.Account) {
	apps, err := s.store.ListApps(r.Context(), acct.ID)
	if err != nil {
		s.log.Error("list apps failed", "account", acct.ID, "err", err)
		api.WriteProblem(w, api.ErrCapacity("could not list apps"))
		return
	}
	out := make([]api.AppResponse, 0, len(apps))
	for _, a := range apps {
		resp := s.appResponse(a, acct.Plan)
		out = append(out, s.withParkedDeploymentRef(r.Context(), resp, a))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) createApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	var req api.CreateAppRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	// Tier A10 / ADR-088: per-app overflow_node preference.
	// Resolve the wire name → UUID server-side before buildApp
	// runs, so the resulting state.App carries the resolved
	// UUID (the column-shape integrity contract — the column
	// is uuid NULL, never text). strictEmpty=true at create
	// time because the column starts NULL and there is no
	// "clear" path on a fresh row — PATCH is the only place
	// where "" means "clear", and the OpenAPI
	// minLength: 1 + DTO docstring are the contract. The
	// resolver returns:
	//   - (uuid, nil) on a real, active compute_node
	//   - ("", nil)   when req.OverflowNode == nil (omit
	//     → no preference, the A9 fallback)
	//   - ("", prob)  on "", ErrNotFound, or active=false
	overflowUUID, prob := s.resolveOverflowNode(r.Context(), req.OverflowNode, true /*strictEmpty: reject "" at create-time*/)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	limits := api.MustLimitsFor(acct.Plan)
	app, prob := s.buildApp(acct, req, limits)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	// Stamp the resolved UUID (or nil) onto the App before the
	// store layer sees it. The store writes the column verbatim
	// — see pkg/state/pgstore.go::CreateAppIfUnderQuota +
	// pgstore.go::CreateApp for the new overflow_node column.
	app.OverflowNode = nilStringPtr(overflowUUID)
	// Phase 2 / Gate A: leave node_id NULL — schedd's
	// PlacementClaimSubscriber stamps the owner (see emitAppCreated
	// for the full architectural rationale + docs/adr/055).
	app.NodeID = ""
	// Deployed-app count quota + insert happen in the same critical
	// section inside the store (PgStore: SELECT … FOR UPDATE on the
	// parent accounts row; MemStore: m.mu). This closes the TOCTOU the
	// previous CountDeployedApps + CreateApp pair exposed on Free/Hobby
	// accounts under concurrency (spec §4.2).
	created, err := s.store.CreateAppIfUnderQuota(r.Context(), app, limits)
	if err != nil {
		var qe *state.QuotaError
		switch {
		case errors.As(err, &qe):
			api.WriteProblem(w, api.ErrPlanLimitApps(limits, qe.Observed))
		case errors.Is(err, state.ErrConflict):
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
				"Slug taken", fmt.Sprintf("app slug %q is already in use", req.Slug)))
		default:
			api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, api.CodeCapacity,
				"Capacity", "could not create app"))
		}
		return
	}
	s.log.Info("app created", "app", created.ID, "slug", logsanitize.Field(created.Slug), "account", acct.ID)
	s.audit.Emit(r.Context(), "app.created", &acct.ID, map[string]any{
		"app_id":           created.ID,
		"slug":             created.Slug,
		"type":             string(created.Type),
		"ram_mb":           created.RAMMB,
		"cpu_millicores":   created.CPUMillicores,
		"resource_profile": api.ResourceProfileForResources(created.RAMMB, created.CPUMillicores),
		"max_concurrency":  created.MaxConcurrency,
		"runtime":          created.Runtime,
	})
	s.emitAppCreated(r.Context(), created)
	resp := s.appResponse(created, acct.Plan)
	writeJSON(w, http.StatusCreated, s.withParkedDeploymentRef(r.Context(), resp, created))
}

// buildApp applies defaults and validates a create request, returning the App to
// persist or a *Problem describing the first violation.
func (s *server) buildApp(acct state.Account, req api.CreateAppRequest, limits api.Limits) (state.App, *api.Problem) {
	if !validSlug(req.Slug) {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid slug", "slug must be 3–40 chars, lowercase letters, digits, and hyphens")
	}
	typ := state.AppType(orDefault(req.Type, string(state.AppTypeApp)))
	if typ != state.AppTypeApp && typ != state.AppTypeFunction {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Invalid type", "type must be app or function")
	}
	if typ == state.AppTypeFunction && req.Runtime != "node22" && req.Runtime != "python312" && req.Runtime != "go124" && req.Runtime != "go124-alpine" && req.Runtime != "node24" && req.Runtime != "python313" {
		return state.App{}, api.NewProblem(http.StatusBadRequest, api.CodeValidation,
			"Invalid runtime", "functions require runtime node22, python312, go124, go124-alpine, node24, or python313")
	}
	ram := req.RAMMB
	cpuMillicores := req.CPUMillicores
	if req.ResourceProfile != "" {
		profile, ok := api.ResourceProfileSpecFor(req.ResourceProfile)
		if !ok {
			return state.App{}, api.ErrInvalidResourceProfile(req.ResourceProfile)
		}
		if ram != 0 && ram != profile.MemoryMB {
			return state.App{}, api.ErrResourceProfileConflict("ram_mb", profile.Name, profile.MemoryMB, ram)
		}
		if cpuMillicores != 0 && cpuMillicores != profile.CPUMillicores {
			return state.App{}, api.ErrResourceProfileConflict("cpu_millicores", profile.Name, profile.CPUMillicores, cpuMillicores)
		}
		ram = profile.MemoryMB
		cpuMillicores = profile.CPUMillicores
	}
	if ram == 0 {
		ram = limits.RAMMB
	}
	if cpuMillicores == 0 {
		cpuMillicores = api.DefaultAppCPUMillicores
	}
	if prob := api.ValidateAppCPUMillicores(cpuMillicores); prob != nil {
		return state.App{}, prob
	}
	mc := req.MaxConcurrency
	if mc == 0 {
		mc = 1
	}
	lifecycle := lifecycleManifestFromCreate(req)
	// A service's desired replica count is its steady-state instance
	// requirement. When max_concurrency is omitted, make the default large
	// enough for that target; an explicit max_concurrency remains authoritative.
	if req.MaxConcurrency == 0 && lifecycle.EffectiveExecutionMode() == api.ExecutionModeService && lifecycle.ServiceReplicas != nil && lifecycle.ServiceReplicas.Desired > mc {
		mc = lifecycle.ServiceReplicas.Desired
	}
	if prob := api.ValidateAppConfig(limits, ram, mc); prob != nil {
		return state.App{}, prob
	}
	if prob := lifecycleProblem(acct.Plan, lifecycle, mc); prob != nil {
		return state.App{}, prob
	}
	// Issue #471 / ADR-047: per-app streaming flag. Apply the
	// plan-level default when the request didn't carry one — a
	// Hobby customer's brand-new app is streaming-ready without an
	// extra PATCH round-trip. Free defaults to false (the only
	// legal value on Free; apid rejects PATCH true with 403
	// plan_streaming_not_allowed). The Plan accessor keeps the
	// fail-closed contract (pkg/api/limits.go) — Free's accessor
	// returns false just like LimitsFor(false) would.
	//
	// ADR-102 D5: plan-gate at create-time. A Free customer
	// POSTing streaming_enabled=true gets 403
	// plan_streaming_not_allowed BEFORE the App is built — same
	// shape as the PATCH-time gate (cmd/apid/handlers_ext.go:245).
	// Without this gate a Free customer's create request would
	// silently land as true (bypassing the Free-tier contract that
	// StreamingResponseAllowed is supposed to enforce). The
	// status code 403 mirrors UpdateApp exactly so the same
	// CodePlanStreamingNotAllowed returns the same status on
	// POST vs PATCH — telemetry collapsing on `code` is uniform.
	//
	// TODO(ADR-102-followup): add apps_streaming_enabled_plan_check
	// Postgres CHECK constraint via NOT VALID + VALIDATE
	// migrations once production telemetry confirms zero Free+
	// streaming_enabled=true rows. Until then this runtime gate is
	// the only enforcement; a direct-DB write or backup-restore
	// can still violate the invariant. The follow-up ships a
	// 1-cycle telemetry window after this PR lands.
	if req.StreamingEnabled != nil && *req.StreamingEnabled && !acct.Plan.StreamingResponseAllowed() {
		return state.App{}, api.NewProblem(http.StatusForbidden,
			api.CodePlanStreamingNotAllowed,
			"Streaming responses are not allowed on this plan",
			"Free tier does not support per-app streaming; upgrade to Hobby or higher.")
	}
	streaming := acct.Plan.StreamingEnabled()
	if req.StreamingEnabled != nil {
		streaming = *req.StreamingEnabled
	}
	// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge flag.
	// Mirrors the streaming-enabled shape above — plan-level
	// default applied when the request didn't carry one, request
	// override otherwise. Free stays off (a long-lived WS would
	// pin a wake past the 30 s Free idle window); Hobby/Pro/Scale
	// default on. The Plan.WebSocketEnabled() accessor is
	// fail-closed — Free's accessor returns false.
	ws := acct.Plan.WebSocketEnabled()
	if req.WebSocketEnabled != nil {
		ws = *req.WebSocketEnabled
	}
	// ADR-093: per-route observability opt-in. Mirrors the
	// WebSocketEnabled shape above — plan-level default applied
	// when the request didn't carry one, request override
	// otherwise. Free stays off (per-route cardinality would not
	// have a budget alongside the per-app rollups); Hobby/Pro/
	// Scale default on. The Plan.RouteMetricsEnabled() accessor
	// is fail-closed — Free's accessor returns false. The PATCH
	// upstream gate (CodePlanRouteMetricsNotAllowed) has already
	// rejected a Free customer trying to override the default to
	// true on a PATCH round-trip; create-time goes through the
	// same gate in handlers_ext.go.
	rm := acct.Plan.RouteMetricsEnabled()
	if req.RouteMetricsEnabled != nil {
		rm = *req.RouteMetricsEnabled
	}
	// Issue #470 / ADR-055: per-app two-tier snapshot flag. Apply
	// the plan-level default when the request didn't carry one —
	// a Pro customer's brand-new app gets warm.snap capture
	// without an extra PATCH round-trip. Free/Hobby default to
	// false (the only legal value on those tiers; apid rejects
	// PATCH-true with 403 plan_warm_snapshot_not_allowed). The
	// per-request override on Pro/Scale lets a customer opt out
	// (e.g. an app they know runs cold every request).
	warmEnabled := acct.Plan.WarmSnapshotEnabled()
	if req.WarmSnapshotEnabled != nil {
		warmEnabled = *req.WarmSnapshotEnabled
	}
	// Issue #560 + issue #695 / ADR-080: per-app require_authn
	// + public_auth_mode. The default is now per-plan — see
	// pkg/api/limits.go (Plan.RequireAuthnDefault +
	// Plan.PublicAuthModeDefault). Per-plan truth table:
	// Free={false, "open"}, Hobby={true, "open"},
	// Pro={true, "bearer"}, Scale={true, "bearer"}. Existing
	// customers are unaffected because migration 00155
	// grand-fathered every pre-flip row with
	// auth_default_flipped_at and did NOT flip their
	// require_authn / public_auth_mode values.
	//
	// Plan-gate at create-time: a Free/Hobby customer who POSTs
	// require_authn=true gets 403 plan_require_authn_not_allowed
	// BEFORE the App is built — same shape as the PATCH-time
	// gate (handlers_ext.go:268). Without this gate a Free
	// customer's create request would silently land as true
	// (bypassing the Free-tier contract that RequireAuthnAllowed
	// is supposed to enforce). Hobby unlocks the gate as a
	// default but NOT as an opt-in: a Hobby customer can keep
	// the default true on creation but cannot escalate back to
	// true once they PATCH-false (the PATCH-time gate blocks it).
	if req.RequireAuthn != nil && *req.RequireAuthn && !acct.Plan.RequireAuthnAllowed() {
		return state.App{}, api.NewProblem(http.StatusForbidden,
			api.CodePlanRequireAuthnNotAllowed,
			"Per-app authentication is not allowed on this plan",
			"Free and Hobby tiers do not support per-app require_authn; upgrade to Pro or higher.")
	}
	requireAuthn := acct.Plan.RequireAuthnDefault()
	if req.RequireAuthn != nil {
		requireAuthn = *req.RequireAuthn
	}
	// public_auth_mode has no wire-side override on
	// CreateAppRequest today — PATCH is the only surface for
	// it (the basic-cred sealing step requires plaintext
	// credentials, which only makes sense on a PATCH
	// roundtrip). The default lands here so a freshly
	// created Hobby/Pro/Scale app inherits the gate as part
	// of the secure-by-default flip.
	publicAuthMode := acct.Plan.PublicAuthModeDefault()
	// Apply the per-app threshold defaults from the plan; an
	// explicit override on the request wins. Out-of-range values
	// were already rejected at the JSON-decode layer
	// (api.ValidateWarmSnapshotBounds), so the only path that
	// produces an out-of-range value here is a buggy test or
	// internal caller.
	warmMinReqs := acct.Plan.WarmSnapshotMinRequestsDefault()
	if req.WarmSnapshotMinRequests != nil {
		warmMinReqs = *req.WarmSnapshotMinRequests
	}
	warmMinMs := acct.Plan.WarmSnapshotMinMsDefault()
	if req.WarmSnapshotMinMs != nil {
		warmMinMs = *req.WarmSnapshotMinMs
	}
	// ADR-124: per-app wire-protocol selector. Closed set
	// {http1, http2, grpc} — http1 / http2 are universal;
	// grpc is plan-gated to Hobby+/Pro/Scale (Free returns 403
	// plan_app_protocol_grpc_not_allowed). Plan-level default
	// applied when the request didn't carry one so a Hobby
	// customer's brand-new app is grpc-ready without an extra
	// PATCH round-trip; Free defaults to "http1" via the
	// Plan.AppProtocolDefault() accessor (the only legal value
	// on Free). The closed-set CHECK apps_app_protocol_chk
	// (migration 00382) is the schema-level guard; the apid
	// gate is the customer-visible 403 surface that mirrors the
	// PATCH-time gate (handlers_ext.go). Without the apid gate
	// a Free customer's create request would silently land as
	// "http1" via the plan default — bypass-closed, but the
	// error message would never reach the customer. Mirrors the
	// streaming_enabled / require_authn shape above.
	if req.AppProtocol != nil {
		if !api.IsValidAppProtocol(*req.AppProtocol) {
			return state.App{}, api.NewProblem(http.StatusBadRequest,
				api.CodeAppProtocolInvalid,
				"Invalid app_protocol",
				"app_protocol must be one of: http1, http2, grpc")
		}
		if *req.AppProtocol == api.AppProtocolGRPC &&
			!acct.Plan.AppProtocolAllowed(api.AppProtocolGRPC) {
			return state.App{}, api.NewProblem(http.StatusForbidden,
				api.CodePlanAppProtocolGrpcNotAllowed,
				"Per-app gRPC wire protocol is not allowed on this plan",
				"Free tier does not support app_protocol='grpc'; upgrade to Hobby or higher.")
		}
	}
	appProtocol := api.AppProtocolHTTP1
	if req.AppProtocol != nil {
		appProtocol = *req.AppProtocol
	}
	if req.AppProtocol != nil {
		appProtocol = *req.AppProtocol
	}
	return state.App{
		AccountID: acct.ID, Slug: req.Slug, Type: typ, Runtime: req.Runtime,
		RAMMB: ram, CPUMillicores: cpuMillicores, MaxConcurrency: mc, IdleTimeoutS: req.IdleTimeoutS, Status: state.AppActive,
		StreamingEnabled: streaming,
		WebSocketEnabled: ws,
		// ADR-093: per-route observability opt-in (plan-level
		// default applied via the block above). Mirrors the
		// WebSocketEnabled shape — the per-plan default is
		// applied here at create time so the row round-trips
		// the same value a future PATCH would land on.
		RouteMetricsEnabled: rm,
		// ADR-091 amendment / §4.1.2.0: coarse-gate per-app
		// maintenance flag. Default false (mirrors the
		// apps.maintenance_mode column DEFAULT). Customers may
		// PATCH true to put the whole app into 503 maintenance;
		// there's no plan gate (Free and above may opt in) —
		// coarse maintenance is a customer-experience feature,
		// not an abuse vector.
		MaintenanceMode:     req.MaintenanceMode != nil && *req.MaintenanceMode,
		WarmSnapshotEnabled: warmEnabled,
		// Issue #560 + issue #695 / ADR-080: see the
		// plan-default block above. Default is per-plan
		// (Plan.RequireAuthnDefault + Plan.PublicAuthModeDefault);
		// the per-plan RequireAuthnAllowed gate is enforced
		// at create-time too (see the rejection branch above)
		// AND at PATCH-time (handlers_ext.go:268) so a Free
		// customer's escalation surfaces as 403
		// plan_require_authn_not_allowed before any SQL
		// write. State layer is the canonical source
		// (apps.require_authn + apps.public_auth_mode columns);
		// the DTO surfaces the same values.
		RequireAuthn:   requireAuthn,
		PublicAuthMode: publicAuthMode,
		// Coerce to the plan minimums when the request asked for a
		// warm config but the plan says warm-snapshot is off: the
		// store ignores them anyway (the cold-boot path doesn't
		// read min_requests / min_ms), and the apid response
		// projects the plan defaults so dashboards stay consistent.
		WarmSnapshotMinRequests: warmMinReqs,
		WarmSnapshotMinMs:       warmMinMs,
		// ADR-124: per-app wire-protocol selector. Plan-default
		// applied above (Free → "http1", Hobby/Pro/Scale →
		// "http1" but customer may opt in to http2 / grpc via
		// the request body). The store's CreateApp floor
		// (pgstore.go) coerces an empty value to "http1" so
		// hand-built App{}s land safely; this branch never
		// reaches the floor in practice because every exit
		// path above assigns appProtocol explicitly.
		AppProtocol: appProtocol,
		Manifest:    stateManifestFromAPI(lifecycle),
	}, nil
}

func (s *server) createDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// DeployedApps is enforced at app-create time; the active-app
	// gate lives in store.CreateDeployment (returns ErrNotFound on a
	// soft-deleted app, which we surface as 404 here). Multipart
	// uploads go down the createDeploymentMultipart branch; the
	// JSON branch is the rest of this handler. Extracted to
	// loadAppAndPreflight so createDeployment stays under the
	// CLAUDE.md 50-line handler cap.
	app, ok, limits := s.loadAppAndPreflight(w, r, acct)
	if !ok {
		return
	}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		// Cap upload size at the plan's SourceTarballMaxMB before any
		// multipart parsing — MaxBytesReader returns a *MaxBytesError on
		// overflow which createDeploymentMultipart maps to 413.
		max := int64(limits.SourceTarballMaxMB) * 1024 * 1024
		r.Body = http.MaxBytesReader(w, r.Body, max)
		s.createDeploymentMultipart(w, r, acct, app, false)
		return
	}
	var req api.CreateDeploymentRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if len(req.Workflows) > 0 {
		if p := validateWorkflowDefinitionsAgainstPlan(req.Workflows, acct.Plan); p != nil {
			api.WriteProblem(w, p)
			return
		}
	}
	if !isDigestPinned(req.Image) {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeImageRequired,
			"Image required", "image: deploys require a digest-pinned reference, e.g. registry.gregale.dev/app@sha256:..."))
		return
	}
	// Pre-CreateDeployment validation gates (#472 / #460 / #463).
	// Gate order matters (signature → override → sidecar); each
	// helper short-circuits only on its own failure.
	if p := enforceSignatureGate(r.Context(), s, acct, app, &req); p != nil {
		api.WriteProblem(w, p)
		return
	}
	overrides, p := validateOverrides(&req, limits)
	if p != nil {
		api.WriteProblem(w, p)
		return
	}
	if p := validateAndPlanSidecars(&req, acct, limits); p != nil {
		api.WriteProblem(w, p)
		return
	}
	// ADR-091 / PR-D: per-deployment env scope validation. The
	// schema CHECK (deployments_scope_shape) would reject on the
	// INSERT — surfacing the violation here gives the customer a
	// clean 400 with the right RFC 7807 code instead of letting
	// the SQLSTATE 23514 bubble up to a generic 500. Empty
	// request scope is fine (handled: defaulted to
	// api.DefaultEnvScope in buildDeploymentForInsert), so we
	// only validate when the field is present.
	if req.Scope != "" {
		if p := api.ValidateScope(req.Scope); p != nil {
			api.WriteProblem(w, p)
			return
		}
	}
	// Issue #977 / ADR-116: validate the annotation surface from
	// the JSON wire. The same helper covers all three deploy paths
	// (image JSON here, source-tarball multipart, source-ref
	// JSON). Pointer fields map to zero (no annotation) when
	// absent; a present-but-empty string is accepted (the column
	// is nullable and "no annotation" == "").
	jsonAnn := annotationForm{
		Reason:     strFromPtr(req.Reason),
		Tag:        strFromPtr(req.Tag),
		DeployedBy: strFromPtr(req.DeployedBy),
		PRNumber:   intFromPtr(req.PRNumber),
	}
	if p := validateAnnotationForm(jsonAnn); p != nil {
		api.WriteProblem(w, p)
		return
	}
	// PR-B: prior-deployment supersede is in store.CreateDeployment's tx;
	// we read prev BEFORE the call so the supersede-notify can carry
	// its id (LatestDeployment returns the post-supersede row).
	prev, _ := s.store.LatestDeployment(r.Context(), app.ID)
	dep, sErr := buildDeploymentForInsert(app, &req, overrides, limits, acct.Plan)
	if sErr != nil {
		api.WriteProblem(w, sErr)
		return
	}
	// Issue #606 / SAFE-RELEASES-E.1: server-side actor
	// attribution. Stamped AFTER buildDeploymentForInsert so the
	// pure-struct helper stays free of HTTP context (the helper
	// is the single source of truth for "given everything we
	// validated, what row lands in the store?" — touching it for
	// every new HTTP-derived field would balloon the function's
	// signature). The three fields stamped here are
	// server-resolved and never client-supplied; the
	// closed-set CHECK on deployed_via (migration 00303) rejects
	// any out-of-set value the helper chain might emit.
	stampDeploymentActor(&dep, acct, r)
	d, err := s.store.CreateDeployment(r.Context(), dep)
	if err != nil {
		// ADR-091 / PR-D: per-deployment scope collision. mapErr
		// wraps state.ErrConflict with the constraint name —
		// detect deployments_app_scope_live_uniq here and surface a
		// dedicated 409 deployment_scope_collision code instead of
		// the generic 503/500 path. The substring match is
		// defensive: mapErr's format is "ErrConflict: constraint"
		// and ErrConflict may wrap a chain of similar errors on
		// multi-statement tx failure paths.
		if errors.Is(err, state.ErrConflict) && strings.Contains(err.Error(), "deployments_app_scope_live_uniq") {
			api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeDeploymentScopeCollision,
				"Scope already live",
				"a live deployment already targets this scope on this app; supersede it before creating another"))
			return
		}
		api.WriteProblem(w, api.ErrCapacity("could not create deployment"))
		return
	}
	notifyAndAuditDeployment(r.Context(), s, acct, app, d, prev, &req)
	writeJSON(w, http.StatusAccepted, s.deploymentResponse(d, app))
}

// handleDevSourceDeploy is the developer-only delta transport. Keeping it on
// a distinct route makes compatibility safe: an older apid returns 404, so the
// CLI can retry the complete archive through the canonical deploy endpoint
// instead of an old server accidentally treating a delta as complete source.
func (s *server) handleDevSourceDeploy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok, limits := s.loadAppAndPreflight(w, r, acct)
	if !ok {
		return
	}
	if app.PreviewOfSlug == "" || app.PreviewPrNumber != 0 {
		s.notFound(w, "no such developer environment")
		return
	}
	if !strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/form-data") {
		api.WriteProblem(w, api.ErrValidation("developer source sync requires multipart/form-data"))
		return
	}
	max := int64(limits.SourceTarballMaxMB)*1024*1024 + api.DevSourceMetadataMaxBytes
	r.Body = http.MaxBytesReader(w, r.Body, max)
	s.createDeploymentMultipart(w, r, acct, app, true)
}

// loadAppAndPreflight resolves the app from the URL slug, enforces
// IDOR (app.AccountID == acct.ID), and hoists the per-plan limits.
// On any failure path, writes the appropriate error response and
// returns ok=false; on success returns (app, true, limits).
//
// Extracted from createDeployment (handlers.go) so the handler stays
// under the CLAUDE.md 50-line cap. The IDOR check is identical to
// loadApp in auth_facade.go; the difference is we ALSO return the
// per-plan limits, which createDeployment needs both for the
// multipart-source-tarball cap and the override / sidecar
// validators.
func (s *server) loadAppAndPreflight(w http.ResponseWriter, r *http.Request, acct state.Account) (state.App, bool, api.Limits) {
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return state.App{}, false, api.Limits{}
	}
	return app, true, api.MustLimitsFor(acct.Plan)
}

// appResponse converts a state.App row into the wire DTO. The plan
// is threaded through so the DTO can surface plan-derived caps
// (issue #559: ConcurrencyPerVMBound) without re-looking-up the
// account store. Mirrors how loadAppAndPreflight (above) threads
// (state.App, api.Limits) — every caller has acct in scope.
func (s *server) appResponse(a state.App, plan api.Plan) api.AppResponse {
	// EgressAllowlist is materialised as a non-nil empty slice so
	// the JSON shape is `[]` (never `null`) regardless of plan /
	// pre-PATCH state. prefix.String() is the canonical form
	// ("1.2.3.0/24", "fe80::/10"); validateUpdateApp has already
	// rewritten any "::ffff:" v4-mapped entry to its v4 form by
	// the time it lands in the store, so we never see one here.
	ea := egressStringList(a.EgressAllowlist)
	return api.AppResponse{
		ID: a.ID, Slug: a.Slug, Type: string(a.Type), Runtime: a.Runtime,
		RAMMB: a.RAMMB, CPUMillicores: effectiveAppCPUMillicores(a, plan),
		ResourceProfile: api.ResourceProfileForResources(a.RAMMB, effectiveAppCPUMillicores(a, plan)),
		MaxConcurrency:  a.MaxConcurrency, IdleTimeoutS: a.IdleTimeoutS,
		// Issue #559: platform-advertised per-VM concurrency cap
		// for the customer's plan. Distinct from MaxConcurrency
		// (the per-app instance cap above). Unknown plans fall
		// through the accessor to 0 — same fail-closed contract
		// as MaxMinInstances.
		ConcurrencyPerVMBound: plan.ConcurrencyPerVMBound(),
		EffectiveLimits:       appEffectiveLimits(a, plan),
		ConfiguredResources: api.AppConfiguredResources{
			MemoryMB: a.RAMMB, CPUMillicores: effectiveAppCPUMillicores(a, plan),
		},
		// ux_spec §6.5: per-app floor the reaper honors when
		// parking idle instances. Pro/Scale only (apid gates).
		MinInstances: a.MinInstances,
		Status:       string(a.Status), URL: appURLForDomain(a.Slug, s.domain),
		Manifest: api.AppManifest{
			Entrypoint:       a.Manifest.Entrypoint,
			Env:              a.Manifest.Env,
			WorkingDir:       a.Manifest.WorkingDir,
			Port:             a.Manifest.Port,
			Healthz:          a.Manifest.Healthz,
			User:             a.Manifest.User,
			ExecutionMode:    a.Manifest.ExecutionMode,
			RestartPolicy:    a.Manifest.RestartPolicy,
			StartupDeadlineS: a.Manifest.StartupDeadlineS,
			MaxRetries:       a.Manifest.MaxRetries,
			ServiceReplicas:  apiManifestFromState(a.Manifest).ServiceReplicas,
		},
		EgressAllowlist: ea,
		// Issue #169 / #172: per-app reactive scale-up trigger
		// targets. 0 = "disabled" (no autoscale rule). Reactive
		// scale-up runs in pkg/sched/scaleup; the trigger reads
		// these columns every tick.
		AutoscaleTargetRPS:    a.AutoscaleTargetRPS,
		AutoscaleTargetCPUPct: a.AutoscaleTargetCPUPct,
		// Issue #471 / ADR-047: per-app streaming flag. Surfaced so
		// dashboards can show "streaming on / off" alongside the
		// egress-allowlist flag.
		StreamingEnabled: a.StreamingEnabled,
		// Issue #676 / ADR-080: per-app raw-bytes Upgrade bridge
		// flag. Surfaced so dashboards can show "websocket on / off"
		// alongside the streaming pill.
		WebSocketEnabled: a.WebSocketEnabled,
		// ADR-093: per-route observability opt-in (DB round-trip).
		// Surfaced so dashboards can show "per-route metrics on /
		// off" alongside the streaming + websocket pills and so a
		// customer can verify their PATCH landed without a second
		// round-trip.
		RouteMetricsEnabled: a.RouteMetricsEnabled,
		// ADR-124: per-app wire-protocol selector (DB
		// round-trip). Surfaced so dashboards can show
		// "protocol: http1 / http2 / grpc" alongside the
		// streaming + websocket + route-metrics pills and so
		// the customer can verify their PATCH landed
		// without a second round-trip. The schema's NOT NULL
		// DEFAULT 'http1' guarantee means a plain string
		// copy is safe (no coalesce needed).
		AppProtocol: a.AppProtocol,
		// Issue #560: per-app require_authn flag. Surfaced so
		// dashboards can show "auth required on / off" alongside
		// the streaming + require_signed pills, and so a customer
		// can verify their PATCH landed without a second
		// round-trip. The token-scope enforcement (cross-account
		// 403) lives in gatewayd-internal, not here.
		RequireAuthn: a.RequireAuthn,
		// Issue #477 / ADR-079: per-app public-URL auth.
		// Surfaced so dashboards can show "public auth: open /
		// bearer / basic" alongside the require_authn pill and
		// so a customer can verify their PATCH landed without
		// a second round-trip. The plaintext creds NEVER
		// appear here — they live in app_secrets (ADR-045).
		//
		// IPAllowlistEntryCount (ADR-118 / MED-1
		// review-fix): gated by mode so a stale column
		// after a flip to a different mode doesn't
		// mis-render. After PATCH ip_allowlist → basic
		// the column retains the prior CIDRs (the Set
		// bit only fires when the new mode is
		// ip_allowlist) — returning len() unconditionally
		// would surface "5" on a basic-mode app. The
		// OpenAPI docstring at api/openapi.yaml
		// documents "Always 0 when mode != 'ip_allowlist'"
		// — this matches. HasBasicCreds is the
		// analogous gating concern: only meaningful in
		// basic-mode context, but the field is owned by
		// the secretbox blob presence which is mode-
		// intrinsic, so no extra gate needed there.
		PublicAuth: api.PublicAuthStatus{
			Mode:          a.PublicAuthMode,
			HasBasicCreds: len(a.PublicAuthBasicSealed) > 0,
			IPAllowlistEntryCount: func() int {
				if a.PublicAuthMode != api.AppPublicAuthModeIPAllowlist {
					return 0
				}
				return len(a.PublicAuthIPAllowlist)
			}(),
		},
		// Issue #695 / ADR-080: grand-father marker. Set by
		// migration 00155 on every pre-flip row; null on
		// apps created after the flip. Surfaced so the
		// dashboard banner query + `faas apps list`
		// annotation can render the "since YYYY-MM-DD"
		// suffix on grandfathered rows.
		AuthDefaultFlippedAt: a.AuthDefaultFlippedAt,
		// Issue #462 / ADR-058 / PR-A: per-app scaling policy. nil
		// = legacy row (projected from min_instances / max_concurrency
		// by the read path). Non-nil = customer-authored policy.
		// The state layer is the canonical source; the DTO carries
		// the same shape so the dashboard / CLI surface one
		// consistent struct.
		ScalingPolicy:  statePolicyToDTO(a.ScalingPolicy),
		LastScaleOutAt: a.LastScaleOutAt,
		LastScaleInAt:  a.LastScaleInAt,
		// Issue #472 / ADR-054: per-app signature-enforcement flag.
		// Surfaced so dashboards can show "signature required" alongside
		// the streaming flag, and so a customer can verify their
		// PATCH landed without a second round-trip.
		RequireSigned: a.RequireSigned,
		// Issue #470 / ADR-055: per-app two-tier-snapshot flag +
		// thresholds. Surfaced so dashboards can show "warm snapshot
		// on / off" alongside the streaming + require_signed pills,
		// and so a customer can verify the per-app override values
		// they PATCHed.
		WarmSnapshotEnabled:     a.WarmSnapshotEnabled,
		WarmSnapshotMinRequests: a.WarmSnapshotMinRequests,
		WarmSnapshotMinMs:       a.WarmSnapshotMinMs,
		// Tier A10 / ADR-088: resolved UUID of the per-app
		// overflow_node preference. NULL on the wire when the
		// customer has not pinned a spill target — the
		// `omitempty` on the DTO drops the field so dashboards
		// can branch on field-absence rather than a sentinel
		// string. Server-side resolution (wire name → UUID)
		// happens at create/PATCH time in handlers.go /
		// handlers_ext.go, so the value is always UUID-shaped
		// (never the operator-readable name).
		OverflowNode: a.OverflowNode,
		// CORS improvements D1: per-app default CORS opt-in +
		// allowlist. Projected from the apps row so a customer
		// can verify their PATCH landed without a second
		// round-trip. The store-layer field is already *bool
		// so the three-state shape (nil = schema default,
		// *false = explicit opt-out, *true = opt-in)
		// flows through unchanged. CORSDefaultOrigins is
		// materialised as a non-nil slice so the JSON shape
		// is `[]` (never `null`) for the same ergonomic
		// reasons as EgressAllowlist above.
		CORSDefaultEnabled: a.CORSDefaultEnabled,
		CORSDefaultOrigins: cORSOriginsList(a.CORSDefaultOrigins),
	}
}

func appEffectiveLimits(a state.App, plan api.Plan) api.AppEffectiveLimits {
	limits, ok := api.LimitsFor(plan)
	if !ok {
		return api.AppEffectiveLimits{MemoryLimitMB: a.RAMMB, CPULimitMillicores: effectiveAppCPUMillicores(a, plan), MaxInstances: a.MaxConcurrency}
	}
	maxInstances := a.MaxConcurrency
	if a.ScalingPolicy != nil && a.ScalingPolicy.MaxInstances > 0 {
		maxInstances = a.ScalingPolicy.MaxInstances
	}
	cpuMillicores := effectiveAppCPUMillicores(a, plan)
	planCPUMaxMillicores := int(int64(limits.CPUQuotaUS) * 1000 / int64(limits.CPUPeriodUS))
	return api.AppEffectiveLimits{
		MemoryLimitMB: a.RAMMB, PlanMemoryMaxMB: limits.RAMMB,
		EphemeralDiskMaxMB: limits.EphemeralDiskMaxMB(),
		GuestVCPUs:         limits.VCPU, CPULimitMillicores: cpuMillicores, PlanCPUMaxMillicores: planCPUMaxMillicores, CPUWeight: limits.CPUWeight,
		MaxInstances: maxInstances, ConcurrencyPerInstance: limits.ConcurrencyPerVMBound,
		AppRequestRateRPS: limits.RateLimitRPS, AppRequestBurst: limits.RateLimitBurst,
		AccountRequestRateRPM: limits.RateLimitPerAccountRPM,
		RequestBudgetMS:       limits.RequestBudgetForType(string(a.Type)).Milliseconds(),
		RequestBudgetMaxMS:    limits.RequestBudgetMaxDuration().Milliseconds(),
		ResponseWriteTimeoutS: int64(plan.ResponseWriteTimeout().Seconds()),
	}
}

func effectiveAppCPUMillicores(a state.App, plan api.Plan) int {
	if a.CPUMillicores > 0 {
		return a.CPUMillicores
	}
	if limits, ok := api.LimitsFor(plan); ok && limits.CPUQuotaUS > 0 && limits.CPUPeriodUS > 0 {
		return int(int64(limits.CPUQuotaUS) * 1000 / int64(limits.CPUPeriodUS))
	}
	return api.DefaultAppCPUMillicores
}

// cORSOriginsList materialises the text[] column as a non-nil slice.
// A nil column (legacy row, never configured) projects as an empty
// slice on the wire so dashboards can render an empty origin list
// without a special-case for null.
func cORSOriginsList(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// withParkedDeploymentRef (issue #554 / ADR-079 follow-up, AC #3
// wire) attaches the latest parked deployment reference to an
// AppResponse. Returns the same struct (with ParkedDeployment
// populated) on success; on a store error the ParkedDeployment
// field stays nil and the error is logged at warn — the apid
// surface still renders the rest of the app, just without the
// parked-deployment reference. The closed-set reason
// (liveness_exhausted | lifecycle_park | admin_park) is enforced
// at the schema layer (migration 00157), so this helper never
// needs to validate.
func (s *server) withParkedDeploymentRef(ctx context.Context, resp api.AppResponse, app state.App) api.AppResponse {
	d, err := s.store.LatestParkedDeploymentForApp(ctx, app.ID)
	if err != nil {
		if !errors.Is(err, state.ErrNotFound) {
			s.log.Warn("apid: parked deployment ref lookup", "app", app.ID, "err", err)
		}
		return resp
	}
	resp.ParkedDeployment = &api.ParkedDeploymentRef{
		ID:           d.ID,
		ParkedReason: d.ParkedReason,
		ParkedAt:     d.ParkedAt,
	}
	return resp
}

// statePolicyToDTO converts the state-layer `*state.ScalingPolicy`
// to the wire DTO `*api.ScalingPolicy`. Returns nil when the input
// is nil so legacy rows project as a JSON `null` (the pre-#462
// contract). Target is pointer-to-pointer so a customer-authored
// `Target: {metric: "rps", value: 0}` round-trips through the read
// path with the metric intact (the pre-fix path dropped Target when
// Value==0, which the DTO upgrade to pointer-Target preserves).
func statePolicyToDTO(p *state.ScalingPolicy) *api.ScalingPolicy {
	if p == nil {
		return nil
	}
	out := &api.ScalingPolicy{
		MinInstances:      p.MinInstances,
		MaxInstances:      p.MaxInstances,
		ScaleOutCooldownS: p.ScaleOutCooldownS,
		ScaleInCooldownS:  p.ScaleInCooldownS,
	}
	if p.Target != nil {
		out.Target = &api.ScalingTarget{
			Metric: p.Target.Metric,
			Value:  p.Target.Value,
		}
	}
	return out
}

// accountResponse builds the AccountResponse DTO, populating Limits
// (plan caps), AppCount (deployed apps), and UsageGBHours (current
// calendar month). Errors from store reads are swallowed — best
// effort; the dashboard renders the row even when the meter is
// temporarily unavailable (meterd republishes every minute).
//
// GitHubInstall is best-effort: expose the durable installation id when the
// account has completed the GitHub App handshake; omit it on a miss or
// transient read failure so account reads still succeed.
func (s *server) accountResponse(ctx context.Context, acct state.Account, r *http.Request) api.AccountResponse {
	l := api.MustLimitsFor(acct.Plan)
	resp := api.AccountResponse{
		ID:            acct.ID,
		Email:         acct.Email,
		EmailVerified: acct.EmailVerified(),
		Plan:          string(acct.Plan),
		Status:        string(acct.Status),
		Limits: api.AccountLimits{
			Plan:               string(acct.Plan),
			RAMMB:              l.RAMMB,
			MaxConcurrency:     l.MaxConcurrency,
			DeployedApps:       l.DeployedApps,
			DeveloperApps:      l.DeveloperApps,
			IncludedGBHours:    int64(l.IncludedGBHours),
			AppLayerMaxMB:      l.AppLayerMaxMB,
			EphemeralDiskMaxMB: l.EphemeralDiskMaxMB(),
		},
	}
	if !acct.EmailVerified() {
		graceEnds := acct.CreatedAt.Add(emailVerificationGrace)
		resp.EmailVerificationGraceEndsAt = &graceEnds
	}
	if inst, err := s.store.GitHubInstallForAccount(ctx, acct.ID); err == nil {
		resp.GitHubInstall = strconv.FormatInt(inst.InstallationID, 10)
	}
	if r != nil {
		if n, err := s.store.CountDeployedApps(ctx, acct.ID); err == nil {
			resp.AppCount = n
		}
		if n, err := s.store.CountDeveloperApps(ctx, acct.ID); err == nil {
			resp.DeveloperAppCount = n
		}
		month := time.Now().UTC()
		if rows, err := s.store.UsageByMonth(ctx, acct.ID, month); err == nil {
			var mbSec int64
			for _, u := range rows {
				mbSec += u.MBSeconds
			}
			resp.UsageGBHours = meter.GBHours(mbSec)
		}
	}
	return resp
}

var slugRe = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

func validSlug(s string) bool { return slugRe.MatchString(s) }

// digestPinnedRE matches a digest-pinned OCI reference end-to-end:
//
//	<host>[/<repo-path>]/<name>@sha256:<64 lowercase hex>
//
// Where:
//
//	host     = RFC 1123 hostname (alnum + '-', dot-separated labels,
//	           optional :<port>)
//	repo     = alnum + '_-' + '.' + '/' (the OCI repository path grammar)
//
// The whole-ref anchoring is load-bearing: parseImageDigest feeds
// apid.createDeployment's slog log of req.Image (CodeQL go/log-injection),
// so a substring-search validator that only verifies the digest tail would
// let any non-OCI prefix through (including control chars / whitespace /
// extra @-separators). The host charset forbids control chars and
// whitespace explicitly, so the entire accepted string is printable OCI.
var digestPinnedRE = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9-]*[A-Za-z0-9])?)*(:[0-9]+)?/[A-Za-z0-9_./-]+@sha256:[0-9a-f]{64}$`)

// parseImageDigest requires a digest-pinned reference (spec gap G1: public
// registries, digest-pinned) and returns the digest portion (sha256:...).
func parseImageDigest(ref string) (string, bool) {
	if !digestPinnedRE.MatchString(ref) {
		return "", false
	}
	return ref[strings.Index(ref, "@"):], true
}

// isDigestPinned reports whether ref is a digest-pinned reference (the form
// the deploy contract requires). Use this for input validation; consumers
// parse the full ref via oci.ParseReference so they can dial the right
// registry host.
func isDigestPinned(ref string) bool {
	return digestPinnedRE.MatchString(ref)
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// egressStringList renders a stored []netip.Prefix to its canonical
// string form ("1.2.3.0/24", "fe80::/10"). The empty case returns a
// non-nil zero-length slice so the JSON shape is `[]` (never `null`)
// regardless of the plan / pre-PATCH state. validateUpdateApp has
// already rewritten any "::ffff:" v4-mapped entry to its v4 form by
// the time it lands in the store, so we never see one here. Reused by
// the audit emit (handlers_ext.go::updateApp) so the wire shape and
// the audit row agree on the canonical form.
func egressStringList(prefixes []netip.Prefix) []string {
	out := make([]string, 0, len(prefixes))
	for _, p := range prefixes {
		out = append(out, p.String())
	}
	return out
}

// emitAppCreated fires the Phase 2 / Gate A placement-claim notify
// that schedd's PlacementClaimSubscriber consumes (migration 00084 +
// ADR-055). The subscriber filters by payload.kind=="created", reads
// apps.row.node_id == NULL (newly inserted), and runs
// Engine.ClaimUnplaced to stamp the owner. The conditional UPDATE in
// Store.SetAppNodeID serialises N schedds into exactly one winner;
// losers drop silently.
//
// Failure here is best-effort: the cold-start sweep at schedd boot
// (cmd/schedd/main.go's ListUnplacedApps + ClaimUnplaced pass)
// reconciles an unplaced app on next restart, so a transient
// Postgres notify outage is bounded to "reboot picks it up" rather
// than "app never gets owned".
//
// Both fields are server-validated before persist (validSlug regex,
// server-generated app.ID UUID), so the JSON interpolation is safe
// even without explicit escaping — the team's pattern, mirrored from
// the existing updateApp emit (handlers_ext.go).
func (s *server) emitAppCreated(ctx context.Context, created state.App) {
	if s.notif == nil {
		return
	}
	// codeql[go/log-injection] false-positive: created.Slug passes
	// validSlug's regex (^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$) at
	// buildApp() before INSERT; created.ID is a server-generated
	// UUID. The interpolation cannot reach a control character
	// or quote. Suppression placed at column 1 per the team's
	// pattern (memory: codeql-suppression-column1).
	_ = s.notif.Notify(ctx, db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"created","slug":"%s","app_id":"%s"}`, created.Slug, created.ID))
}
