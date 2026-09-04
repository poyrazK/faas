package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/api/canary"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

// setSidecarRecipient is the host X25519 recipient apid loads once
// at startup. Mirrors the `setSecretRecipient` pattern in
// handlers_secrets.go:41 — the recipient is the SAME host age
// key (the host's age identity loads once at boot and is shared
// across the secret / sidecar / registry-credential seal sites).
// Keeping a dedicated getter for the sidecar call site means
// the seal helper is testable in isolation (tests can stub the
// getter to return a generated test recipient without touching
// the secret-handler getter).
//
// Setting this is the responsibility of cmd/apid/main.go's run
// path. The nil-default makes tests that don't seal pass without
// plumbing; a production apid that forgets to load the recipient
// surfaces a clear 503 from every deploy that carries sidecars
// (no silent accept-and-drop).
var setSidecarRecipient func() *age.X25519Recipient

// sealSidecars is the apid-side envelope-seal helper for sidecar
// env values (issue #463 / ADR-068 §Decision 3). It is the load-
// bearing gateway between the wire shape (plaintext env per sidecar)
// and the persisted shape (envelope-sealed ciphertext per env key).
//
// Wire: each sidecar carries a plaintext `env` map.
// Persist: each env VALUE is replaced with a base64-encoded
//
//	`secretbox.SealBytes(recipient, "sidecar_env", plaintext,
//	maxValueBytes)` blob. The KEY stays in plaintext (the
//	env-var name). The audit / log path NEVER sees the
//	plaintext.
//
// The helper returns a `*api.Problem` (not a typed error) so the
// caller's `api.WriteProblem` path stays branch-free:
//   - recipient == nil  → 503 ErrCapacity (recipient not loaded)
//   - per-value seal    → 413 ErrEnvVarValueTooLarge (the seal
//     itself enforces the per-value byte cap; the API gate also
//     runs before this helper, so the 413 is defence in depth)
//
// limits is the per-plan `api.Limits` for the authenticated app.
// The seal helper reads `limits.EnvValueMaxBytes` — the SAME cap
// `Sidecar.Validate(limits)` enforces at the API gate
// (pkg/api/dto.go). Re-applying it here makes the seal helper
// self-defending: a future caller that bypasses Validate (e.g. a
// migration script that hand-builds Sidecars) still trips the cap.
// A zero `limits.EnvValueMaxBytes` disables the cap and is
// explicitly NOT supported.
//
// The function returns `[]byte("[]")` (NOT nil) when `ss` is empty
// so the persistence path's `notNullEmptyJSONRaw` helper
// (pkg/state/pgstore.go) never sees a nil JSON shape. The
// `deployments.sidecars` column is `NOT NULL DEFAULT '[]'::jsonb`;
// a nil insert would 23502-fail. The empty-input branch is the
// most common path (the no-sidecar deploy case) and it's
// deliberately as cheap as possible.
func sealSidecars(ss api.Sidecars, recipient *age.X25519Recipient, limits api.Limits) ([]byte, *api.Problem) {
	if recipient == nil {
		return nil, api.ErrCapacity("host age recipient not loaded — refusing to seal sidecar env")
	}
	if len(ss) == 0 {
		return []byte("[]"), nil
	}
	type sealedSidecar struct {
		Name      string            `json:"name"`
		Image     string            `json:"image"`
		Type      api.SidecarType   `json:"type"`
		Cmd       []string          `json:"cmd,omitempty"`
		Env       map[string]string `json:"env,omitempty"`
		Port      int               `json:"port,omitempty"`
		RamMB     int               `json:"ram_mb,omitempty"`
		Essential *bool             `json:"essential,omitempty"`
	}
	out := make([]sealedSidecar, 0, len(ss))
	for _, s := range ss {
		envOut := make(map[string]string, len(s.Env))
		for k, v := range s.Env {
			// Defence-in-depth: the API gate already enforces
			// this cap via Sidecar.Validate(limits), but the
			// seal helper re-enforces it so a future caller
			// that bypasses Validate still trips the cap.
			// secretbox.SealBytes returns ErrSecretValueTooLarge
			// when len(plaintext) > max; we surface it as the
			// SAME code the API gate uses
			// (CodeEnvVarValueTooLarge) so a refactor cannot
			// drift the codes between gates.
			ct, err := secretbox.SealBytes(recipient, "sidecar_env",
				[]byte(v), limits.EnvValueMaxBytes)
			if err != nil {
				return nil, api.NewProblem(http.StatusRequestEntityTooLarge,
					api.CodeEnvVarValueTooLarge,
					"Invalid sidecar env",
					fmt.Sprintf("sidecar %q env[%q]: %v (cap is %d bytes)",
						s.Name, k, err, limits.EnvValueMaxBytes))
			}
			envOut[k] = base64.StdEncoding.EncodeToString(ct)
		}
		out = append(out, sealedSidecar{
			Name:      s.Name,
			Image:     s.Image,
			Type:      s.Type,
			Cmd:       s.Cmd,
			Env:       envOut,
			Port:      s.Port,
			RamMB:     s.RamMB,
			Essential: s.Essential,
		})
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return nil, api.NewProblem(http.StatusInternalServerError,
			api.CodeCapacity, "Failed to marshal sealed sidecars",
			fmt.Sprintf("marshal sealed sidecars: %v", err))
	}
	return raw, nil
}

// validateAndPlanSidecars runs the per-plan gate + per-sidecar
// Validate for an incoming deploy. Returns nil when the sidecars
// pass; otherwise an *api.Problem the caller surfaces verbatim.
//
// Extracted from createDeployment (handlers.go) so the handler stays
// under the CLAUDE.md 50-line cap. The plan-tier gate is currently
// a no-op (every plan inherits the global 2-cap); the accessor
// exists so a future PR can add a per-plan matrix without a
// handler-side branch.
func validateAndPlanSidecars(req *api.CreateDeploymentRequest, acct state.Account, limits api.Limits) *api.Problem {
	if len(req.Sidecars) == 0 {
		return nil
	}
	if !acct.Plan.SidecarAllowed() {
		return api.ErrSidecarNotAllowedOnPlan(acct.Plan)
	}
	return req.Sidecars.Validate(limits)
}

// enforceSignatureGate is the pre-flight signature-enforcement
// check (issue #472 / ADR-054). Runs after the digest check
// (a missing image is a more fundamental request shape error than
// a missing signer) and before the override Validate. The flag is
// on the apps row (apps.require_signed); we do NOT trust the
// customer's req.RequireSigned opt-in to override the operator
// policy — the per-app flag wins (fail-closed). A customer attempt
// to clear an operator-on flag is rejected here:
//
//	app.require_signed=true  &  req.RequireSigned=*false   → 403
//
// The "no trusted signers configured" check is the actual fail-closed
// trip — the operator toggled the flag but never onboarded a
// publisher. We surface this immediately at accept-time so the
// customer sees 403 with a clear message, not a pending→failed
// two-step inside imaged.
//
// Returns nil when the gate passes; *api.Problem otherwise. The
// caller surfaces the problem verbatim. Extracted from
// createDeployment (handlers.go) so the handler stays under the
// CLAUDE.md 50-line cap.
func enforceSignatureGate(ctxr context.Context, s *server, acct state.Account, app state.App, req *api.CreateDeploymentRequest) *api.Problem {
	if !app.RequireSigned {
		return nil
	}
	signers, sErr := s.store.ListAppTrustedSigners(ctxr, acct.ID, app.ID)
	if sErr != nil {
		return api.ErrCapacity("could not load trusted signers")
	}
	if len(signers) == 0 {
		return api.ErrDeploySignatureInvalid(
			"apps.require_signed=true but no trusted publishers are configured for this app; ask the operator to onboard a publisher via PUT /v1/apps/{slug}/trusted_signers/{name}.")
	}
	// Customer-request override: an attempt to turn the flag off on
	// this single deploy is rejected with operator > customer. A nil
	// request field is "leave the per-app flag alone" (the apid
	// default); *true is a no-op (the flag is already on); only
	// *false collides.
	if req.RequireSigned != nil && !*req.RequireSigned {
		return api.ErrDeploySignatureInvalid(
			"apps.require_signed=true on this app; per-deploy opt-out is not permitted (operator policy wins).")
	}
	return nil
}

// validateOverrides runs the per-plan override Validate for an
// incoming deploy (issue #460 / ADR-053). Returns the (possibly
// nil) *api.CreateDeploymentOverrides pointer and a nil problem on
// success; or (nil, problem) on validation failure.
//
// A nil req.Overrides is a no-op — the deployment carries no
// override and the helper returns (nil, nil). A non-nil
// req.Overrides that fails Validate returns (nil, problem) and the
// caller short-circuits with a 400 — the override is NEVER silently
// dropped (ADR-053 §Decision 2). Plan tier comes from the
// authenticated account via the limits arg.
//
// Extracted from createDeployment (handlers.go) so the handler stays
// under the CLAUDE.md 50-line cap.
func validateOverrides(req *api.CreateDeploymentRequest, limits api.Limits) (*api.CreateDeploymentOverrides, *api.Problem) {
	if req.Overrides == nil {
		return nil, nil
	}
	// Issue #554 / ADR-078: per-plan liveness gate. Free is
	// plan-locked off (LivenessAllowed() returns false); the
	// handler rejects with 403 plan_liveness_probe_not_allowed
	// BEFORE the override is persisted. Same gate shape as
	// streaming / warm-snapshot / require_authn (see
	// validatePatchApp or its sibling in handlers_ext.go). The
	// gate uses the same per-plan limits struct that the
	// override's own Validate consults.
	if req.Overrides.LivenessProbe != nil && limits.LivenessPeriodSeconds == 0 {
		return nil, api.NewProblem(http.StatusForbidden,
			api.CodePlanLivenessProbeNotAllowed,
			"Liveness probes are not allowed on this plan",
			"Free tier does not support per-deployment liveness probes; upgrade to Hobby or higher.")
	}
	if p := req.Overrides.Validate(limits); p != nil {
		return nil, p
	}
	return req.Overrides, nil
}

// sealSidecarsForDeploy is the dispatch helper around sealSidecars:
// it owns the recipient-getter call (so sealSidecars stays a pure
// function and is testable in isolation) and normalises the empty-
// input branch to the literal `[]` so memstore + pgstore both
// round-trip byte-for-byte.
//
// Returns (rawJSON, nilProblem) on success; rawJSON is `[]byte("[]")`
// when ss is empty and the sealed envelope otherwise. Returns
// (nil, problem) on the recipient-not-loaded / over-byte-cap
// branches.
func sealSidecarsForDeploy(ss api.Sidecars, limits api.Limits) ([]byte, *api.Problem) {
	if len(ss) == 0 {
		return []byte("[]"), nil
	}
	return sealSidecars(ss, setSidecarRecipient(), limits)
}

// applyOverridesToDeployment marshals the validated
// CreateDeploymentOverrides into the json.RawMessage columns on
// state.Deployment. Extracted from createDeployment (handlers.go)
// so the handler stays under the CLAUDE.md 50-line cap. Empty
// override fields stay nil (the store writes NULL via the
// `nullJSONRaw` helper).
func applyOverridesToDeployment(dep *state.Deployment, o *api.CreateDeploymentOverrides) {
	if len(o.Entrypoint) > 0 {
		dep.OverrideEntrypoint = o.Entrypoint
	}
	if len(o.Cmd) > 0 {
		dep.OverrideCmd = o.Cmd
	}
	if len(o.Env) > 0 {
		if b, err := json.Marshal(o.Env); err == nil {
			dep.OverrideEnv = b
		}
	}
	if len(o.EnvSecrets) > 0 {
		if b, err := json.Marshal(o.EnvSecrets); err == nil {
			dep.OverrideEnvSecrets = b
		}
	}
	if o.Port != 0 {
		dep.OverridePort = o.Port
	}
	if o.Healthcheck != nil {
		if b, err := json.Marshal(o.Healthcheck); err == nil {
			dep.OverrideHealthcheck = b
		}
	}
	// Liveness probe override (issue #554 / ADR-078). Persist
	// the JSONB body so cmd/vmmd's liveness_recv goroutine can
	// pick it up at every BringUp via the resolved struct
	// (cmd/vmmd/liveness_recv.go::livenessProbeConfig). The
	// per-plan gate (Plan.LivenessAllowed() == false for Free)
	// is enforced in the create handler BEFORE this helper is
	// called, so the only filter that can land here is the
	// per-deployment override object itself.
	if o.LivenessProbe != nil {
		if b, err := json.Marshal(o.LivenessProbe); err == nil {
			dep.OverrideLivenessProbe = b
		}
	}
}

// buildDeploymentForInsert assembles the state.Deployment row from
// the validated request + overrides + sealed sidecars. Returns the
// row + a nil problem on success; or a zero Deployment + problem on
// the seal-failure branch. The function is the single entry point
// for "given everything we validated above, what row lands in the
// store?" so a future schema change touches one helper, not the
// handler.
//
// Extracted from createDeployment (handlers.go) so the handler stays
// under the CLAUDE.md 50-line cap.
func buildDeploymentForInsert(app state.App, req *api.CreateDeploymentRequest, overrides *api.CreateDeploymentOverrides, limits api.Limits) (state.Deployment, *api.Problem) {
	dep := state.Deployment{
		AppID: app.ID, ImageDigest: req.Image, Kind: state.DeploymentKindImage, Status: state.DeployPending,
	}
	// Issue #556 PR-A: thread the optional TrafficPercent pointer
	// through. Omitted (nil) → server-side default 100 (matches
	// schema NOT NULL DEFAULT 100 and the pre-#556 behaviour of
	// "100% to the most-recent live row"). Explicit 0..100 → write
	// that value. The handler's plan-gate + range-check above ran
	// before this point, so a non-nil value here is in [0, 100] and
	// the account's plan allows traffic splitting.
	if req.TrafficPercent != nil {
		dep.TrafficPercent = *req.TrafficPercent
	}
	// Issue #976 / ADR-122 / SAFE-RELEASES-A: stamp the canary
	// ladder at deploy time. nil req.Canary → fast-default zero
	// ('none', 0, 0, NULL) — preserved exactly matches today's
	// behaviour. Non-nil → resolve against pkg/api/canary's
	// closed-set catalog; on 'none' we stamp the disable row
	// (zero ladder), on any other preset we stamp total_steps +
	// step=0 + step_started_at=now() so the canary_progression
	// meterd tick has a starting position to walk from.
	if req.Canary != nil {
		// SAFE-RELEASES production-leveling Stream F: the custom
		// preset's stages come from the wire (req.Canary.Stages),
		// not the catalog. LookupCustomPreset runs the same
		// Validate() rules the catalog presets use, so a buggy
		// custom ladder (negative percent, terminal-stage-not-100%)
		// surfaces as a 422 here instead of an orchestrator panic
		// later.
		var preset canary.Preset
		if req.Canary.Preset == "custom" {
			custom, cerr := canary.LookupCustomPreset(req.Canary.Stages)
			if cerr != nil {
				return state.Deployment{}, api.ErrInvalidCanaryPreset(cerr.Error())
			}
			preset = custom
			// Serialise the resolved stages to jsonb so the
			// orchestrator's per-row resolve can rehydrate the
			// Preset without going back through the wire. The
			// jsonb shape mirrors OverrideEnv / Sidecars — the
			// pkg/state type carries json.RawMessage.
			raw, merr := json.Marshal(req.Canary.Stages)
			if merr != nil {
				return state.Deployment{}, api.ErrInvalidCanaryPreset(merr.Error())
			}
			dep.CanaryStages = raw
		} else {
			p, ok := canary.LookupPreset(req.Canary.Preset)
			if !ok {
				return state.Deployment{}, api.ErrInvalidCanaryPreset(req.Canary.Preset)
			}
			preset = p
		}
		dep.CanaryPreset = preset.Name
		dep.CanaryTotalSteps = preset.TotalSteps()
		if first, ok := preset.StageAt(0); ok {
			// The first stage is applied when imaged marks the row live;
			// keeping it on the row lets the live transition rebalance
			// the previous revision before meterd starts the timer.
			dep.TrafficPercent = first.Percent
		}
		// SAFE-RELEASES code-review hardening (migration 00517):
		// canary_step_started_at is NOT NULL post-00517, so the
		// apid Create path must stamp it on every row. The
		// readers (pkg/canary.Once line 226, pkg/safedeploy.
		// Orchestrator line 207) only consult the timestamp when
		// CanaryTotalSteps > 0; the no-canary (preset=none) row
		// gets the deployment's created_at as a placeholder that
		// the readers will skip via the surrounding predicate.
		now := time.Now().UTC()
		dep.CanaryStepStartedAt = &now
	}
	// ADR-091 / PR-D: per-deployment env scope. The handler ran
	// api.ValidateScope above — non-empty here means well-formed
	// (passes EnvScopePattern, is not __all__). Empty → default
	// (the migration's NOT NULL DEFAULT 'default' would catch
	// this, but we stamp it explicitly so the wire is consistent
	// with the column and SerializeDeployment doesn't have to
	// branch on "is the row pre-PR-D").
	if req.Scope != "" {
		dep.Scope = req.Scope
	} else {
		dep.Scope = api.DefaultEnvScope
	}
	// Issue #977 / ADR-116: stamp the annotation surface from the
	// JSON wire. Pointer fields let a customer omit a field
	// (treats as NULL on the row) vs supply an empty string
	// (would be stored as "" — unusual but legal). pgstore
	// collapses dep.PRNumber=0 to NULL via nullif(0).
	if req.Reason != nil {
		dep.Reason = *req.Reason
	}
	if req.Tag != nil {
		dep.Tag = *req.Tag
	}
	if req.DeployedBy != nil {
		dep.DeployedBy = *req.DeployedBy
	}
	if req.PRNumber != nil {
		dep.PRNumber = *req.PRNumber
	}
	if overrides != nil {
		applyOverridesToDeployment(&dep, overrides)
	}
	sealed, sErr := sealSidecarsForDeploy(req.Sidecars, limits)
	if sErr != nil {
		return state.Deployment{}, sErr
	}
	dep.Sidecars = sealed
	return dep, nil
}

// emitSidecarSetAudit fires the sidecar.set audit row when the
// deployment carries sidecars. No-op when ss is empty — the
// regular app.deployed event already covers that path. The
// payload carries metadata only (names, count); NEVER the env
// plaintext, NEVER the sealed ciphertext, NEVER the image
// digest. Extracted from createDeployment (handlers.go) so the
// handler stays under the CLAUDE.md 50-line cap.
//
// ctxr is the request-scoped ctx helper, audit is the server's
// *auditor, acct + app + d are the just-created deployment's
// triple. A future PR-C observability addendum may surface
// per-sidecar cardinality here (names → sidecar_restart_total
// {app,sidecar} metric).
func emitSidecarSetAudit(ctxr context.Context, audit *auditor, acct state.Account, app state.App, d state.Deployment, ss api.Sidecars) {
	if len(ss) == 0 {
		return
	}
	names := make([]string, 0, len(ss))
	for _, sc := range ss {
		names = append(names, sc.Name)
	}
	audit.Emit(ctxr, "sidecar.set", &acct.ID, map[string]any{
		"app_id":        app.ID,
		"deployment_id": d.ID,
		"sidecar_count": len(ss),
		"sidecar_names": names,
	})
}

// notifyAndAuditDeployment is the post-CreateDeployment side-effect
// fan-out: deployment_changed notifies, "deployment created" log,
// IAM-4 audit, optional signature-accepted audit, optional
// sidecar.set audit. Extracted from createDeployment (handlers.go)
// so the handler stays under the CLAUDE.md 50-line cap.
//
// The notify emits are fire-and-forget (the `_ =` is intentional;
// listener crash would block deploy acceptance, which is the wrong
// failure mode for a customer-facing API). The audit emits block
// the goroutine briefly (audit.Emit is sync; pkg/audit batches
// async-flush). The log line sanitises req.Image at the sink
// (CodeQL go/log-injection CWE-117).
func notifyAndAuditDeployment(ctxr context.Context, s *server, acct state.Account, app state.App, d state.Deployment, prev state.Deployment, req *api.CreateDeploymentRequest) {
	// F-03: deployment_changed emits now carry status + deployment_id.
	// status="pending" tells listeners this row is still in-flight
	// (builderd will eventually stamp rootfs_path → imaged converts to
	// ext4); later transitions re-emit with status="live"|"failed"|
	// "superseded". deployment_id==to here, but imaged switches on
	// deployment_id in handleDeployment. Apid does not synthesise
	// every transition — the state machine walks pending→building→
	// imaging→snapshotting→live and each row write is followed by a
	// NotifyDeploymentChanged. The image branch below covers the
	// first hop (submitted); later hops land in cmd/apid/deploy_steps.go.
	_ = s.notif.Notify(ctxr, db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"image","status":"pending","app_id":"%s","deployment_id":"%s","to":"%s"}`, app.ID, d.ID, d.ID))
	// PR-B: if a prior row was just superseded inside the same tx,
	// fire a second NotifyDeploymentChanged so imaged's F5 cleanup
	// handler (handleDeploymentChanged) can drop the prior snapshot.
	// The notify carries status="superseded" + to=prev.ID; if no prev
	// existed (first deploy on this app), skip the second notify. A
	// canary deliberately keeps its prior live revision as the
	// residual traffic bucket, so it must not emit this cleanup signal.
	if prev.ID != "" && d.CanaryTotalSteps <= 0 {
		_ = s.notif.Notify(ctxr, db.NotifyDeploymentChanged,
			fmt.Sprintf(`{"kind":"image","status":"superseded","app_id":"%s","deployment_id":"%s","to":"%s"}`, app.ID, prev.ID, prev.ID))
	}
	// Sanitize req.Image at the log sink — CodeQL go/log-injection
	// (CWE-117). isDigestPinned already rejects malformed refs with
	// 400 before this line, but a future field/wrapper change would
	// break that invariant. Sanitizing here means the log statement
	// stays safe regardless of upstream changes. d.ID and app.ID are
	// server-generated UUIDs — no sanitize needed.
	s.log.Info("deployment created", "deployment", d.ID, "app", app.ID, "ref", logsanitize.Field(req.Image))
	// IAM-4 (issue #291): record the deployment. data.supersedes is
	// the previous deployment_id (PR-B: read before the CreateDeployment
	// tx via LatestDeployment). Empty when this is the first deploy on
	// the app — dashboards can distinguish "first deploy" from
	// "supersede" without inspecting app history.
	//
	// Issue #460 / ADR-053: data.has_overrides is the audit-side mirror
	// of the HasOverrides response field. Set true when the deployment
	// carried any override_* column. The override values themselves are
	// NEVER in the audit payload — only the boolean (ADR-053 §Decision 4
	// + ADR-045 §Decision 6 mirror: env values never cross the audit sink).
	hasOverrides := req.Overrides != nil
	// Issue #606 / SAFE-RELEASES-E.1: stamp the per-call actor
	// ("dashboard:<id>" / "cli:<id>" / "api:<id>") onto the
	// audit row's actor column instead of the constructor-baked
	// "apid". The resolved actor derives from the same source
	// that stamps d.DeployedVia at handler entry — see
	// cmd/apid/deploy_actor.resolvedActorString. The actor
	// columns are ALSO folded into the payload via
	// mergeActorAudit so downstream grep-on-payload queries
	// (independent of the events.actor column) keep working
	// without a schema migration. Omit-when-zero rule matches
	// the PR #984 annotation-merge helper.
	resolvedActor := resolvedActorString(d.DeployedVia, d.DeployedByUserID, d.PusherLogin)
	supersedes := prev.ID
	if d.CanaryTotalSteps > 0 {
		// The prior revision remains live until the terminal canary
		// transition; it is not superseded at deployment creation.
		supersedes = ""
	}
	appDeployedData := map[string]any{
		"app_id":        app.ID,
		"deployment_id": d.ID,
		"ref":           req.Image,
		"supersedes":    supersedes,
		"has_overrides": hasOverrides,
	}
	// Issue #977 / ADR-116: mirror the annotation surface into the
	// app.deployed audit row. Reuses mergeAnnotationAudit so the
	// three deploy paths (image JSON, source-tarball multipart,
	// source-ref JSON) all stamp the same keys with the same
	// "omit when zero" rule. Pre-feature rows stay byte-identical.
	mergeAnnotationAudit(appDeployedData, annotationForm{
		Reason:     d.Reason,
		Tag:        d.Tag,
		DeployedBy: d.DeployedBy,
		PRNumber:   d.PRNumber,
	})
	s.audit.EmitAs(ctxr, resolvedActor, "app.deployed", &acct.ID, mergeActorAudit(appDeployedData, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin))
	// Issue #472 / ADR-054: emit app.signed_image_accepted here ONLY
	// when require_signed is on for this deploy. imaged will later emit
	// app.signature_invalid / app.signature_missing from its verify hook
	// (Bucket 4), but the "request passed the operator gate" event is
	// apid's surface — the deploy is acked before imaged even runs the
	// verify. The audit row answers "which signature-gated deploy was
	// accepted on what app" without a follow-up GET. Empty ref column
	// keeps the row distinct from the plain app.deployed event
	// (different `kind`).
	if app.RequireSigned {
		s.audit.EmitAs(ctxr, resolvedActor, "app.signed_image_accepted", &acct.ID, mergeActorAudit(map[string]any{
			"app_id":        app.ID,
			"deployment_id": d.ID,
			"ref":           req.Image,
		}, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin))
	}
	// Issue #463 / ADR-068: sidecar audit event (delegated to its
	// own helper so the sidecar surface is grep-able from one place).
	emitSidecarSetAudit(ctxr, s.audit, acct, app, d, req.Sidecars)
	// Issue #556 PR-A: emit a distinct audit row when the caller
	// supplied an explicit traffic_percent (i.e. opted into canary
	// mode on this deploy). The omitted case (server default 100)
	// is the pre-#556 behaviour and is already covered by
	// app.deployed; emitting a separate row keeps the canary path
	// greppable for operators reviewing the rollout timeline. The
	// supersede branch above zeroed the prior row's traffic_percent
	// — operators can correlate by matching deployment_id across
	// app.deployed + deployment.traffic_percent_set_on_create +
	// deployment.traffic_percent_changed (PATCH path).
	if req.TrafficPercent != nil {
		s.audit.EmitAs(ctxr, resolvedActor, "deployment.traffic_percent_set_on_create", &acct.ID, mergeActorAudit(map[string]any{
			"app_id":          app.ID,
			"deployment_id":   d.ID,
			"traffic_percent": *req.TrafficPercent,
		}, d.DeployedByUserID, d.DeployedVia, d.DeployedFromIP, d.PusherLogin))
	}
}
