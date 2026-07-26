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
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/billing"
	"github.com/onebox-faas/faas/pkg/billing/stripe"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/logsanitize"
	"github.com/onebox-faas/faas/pkg/mail"
	"github.com/onebox-faas/faas/pkg/state"
)

// --- apps CRUD --------------------------------------------------------------

// getApp returns one app by slug.
func (s *server) getApp(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, s.appResponse(app))
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
func validateUpdateApp(req *api.UpdateAppRequest, acct state.Account, limits api.Limits) *api.Problem {
	if req.MinInstances == nil {
		// fall through to the egress allowlist branch
	} else {
		if !acct.Plan.MinInstancesAllowed() {
			return api.ErrPlanMinInstancesNotAllowed(acct.Plan)
		}
		if *req.MinInstances < 0 || *req.MinInstances > limits.MaxConcurrency {
			return api.ErrInvalidMinInstances(*req.MinInstances, limits.MaxConcurrency)
		}
	}
	if req.EgressAllowlist != nil {
		// Plan tier first: a Free/Hobby PATCH must surface 403 even
		// if the request would otherwise be a malformed 400.
		if !acct.Plan.EgressAllowlistAllowed() {
			return api.ErrPlanEgressAllowlistNotAllowed(acct.Plan)
		}
		maxSize := acct.Plan.EgressAllowlistMaxSize()
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
	if prob := validateUpdateApp(&req, acct, limits); prob != nil {
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
	updated, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{
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
	})
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"updated","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app updated", "app", updated.ID, "slug", updated.Slug, "account", acct.ID)
	writeJSON(w, http.StatusOK, s.appResponse(updated))
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
	pending, err := s.store.ListInvocationsForApp(ctx(r), app.ID,
		state.InvocationPending, state.InvocationDispatching)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("list-inv"))
		return
	}
	for _, inv := range pending {
		if err := s.store.CancelInvocation(ctx(r), inv.ID); err != nil && !errors.Is(err, state.ErrNotFound) {
			// Don't fail the delete on a per-row cancel error; the
			// drain will surface the row as failed and the customer
			// sees it in the meter. Logging at warn so it's
			// observable.
			s.log.Warn("deleteApp: cancel invocation",
				"inv", inv.ID, "app", app.ID, "err", err)
		}
	}
	if err := s.store.DeleteApp(ctx(r), app.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"deleted","slug":"%s","app_id":"%s"}`, app.Slug, app.ID))
	s.log.Info("app deleted", "app", app.ID, "slug", app.Slug, "account", acct.ID)
	w.WriteHeader(http.StatusNoContent)
}

// --- deployments -----------------------------------------------------------

// getDeployment returns one deployment by id.
func (s *server) getDeployment(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such deployment")
		return
	}
	writeJSON(w, http.StatusOK, s.deploymentResponse(d))
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
	current, err := s.store.LatestDeployment(ctx(r), app.ID)
	if err != nil {
		s.notFound(w, "no deployments")
		return
	}
	target, err := s.store.LatestSupersededDeployment(ctx(r), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrNoRollbackTarget())
		return
	}
	if err := s.store.MarkDeploymentSuperseded(ctx(r), current.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not supersede current"))
		return
	}
	if err := s.store.MarkDeploymentLive(ctx(r), target.ID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not activate rollback target"))
		return
	}
	// Re-read target so the response carries post-promotion status=Live,
	// not the pre-promotion Superseded we snapshotted into the local
	// struct above. Listeners downstream branch on this field — fix
	// surfaced by PR #117 review (finding F3).
	fresh, err := s.store.DeploymentByID(ctx(r), target.ID)
	if err == nil {
		target = fresh
	}
	// F-03: rollback emit now carries status="live" (the freshly-restored
	// deployment is live) and a deployment_id for listeners that switch on
	// the field. imaged's handleDeployment ignores this emit (the rollback
	// target already has a prepared ext4 + snap from the prior supersede),
	// but the symmetry lets future listeners branch on status without
	// a decode change.
	_ = s.notif.Notify(ctx(r), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"rollback","status":"live","app_id":"%s","deployment_id":"%s","from":"%s","to":"%s"}`,
			app.ID, target.ID, current.ID, target.ID))
	// F-03: emit the supersede transition for the deployment being
	// retired. Prior code did not announce the supersede at all, so imaged's
	// (F5) cleanupDeploymentFiles(p.To, true /* keepSnap */) branch never
	// fired. status="superseded" makes the transition observable.
	_ = s.notif.Notify(ctx(r), db.NotifyDeploymentChanged,
		fmt.Sprintf(`{"kind":"superseded","status":"superseded","app_id":"%s","deployment_id":"%s","to":"%s"}`,
			app.ID, current.ID, current.ID))
	s.log.Info("app rolled back", "app", app.ID, "from", current.ID, "to", target.ID, "account", acct.ID)
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
	if _, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not park app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged,
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
	if _, err := s.store.UpdateApp(ctx(r), app.ID, state.UpdateAppParams{Status: &st}); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not wake app"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged,
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
		writeJSON(w, http.StatusOK, s.appResponse(app))
		return
	}
	updated, err := s.store.RenameApp(ctx(r), acct.ID, oldSlug, req.NewSlug)
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
	_ = s.notif.Notify(ctx(r), db.NotifyAppChanged,
		fmt.Sprintf(`{"kind":"renamed","app_id":"%s","from":%q,"to":%q}`, app.ID, oldSlug, req.NewSlug))
	// CodeQL go/log-injection (CWE-117): oldSlug came from the
	// apps.slug column (regex-validated at create) and req.NewSlug
	// passed the same validSlug check on this request's body. Wrap
	// both so a future relax of validSlug (or a hostile migration)
	// cannot smuggle CR/LF into the audit line.
	s.log.Info("app renamed", "app", updated.ID, "from", logsanitize.Field(oldSlug), "to", logsanitize.Field(req.NewSlug), "account", acct.ID)
	writeJSON(w, http.StatusOK, s.appResponse(updated))
}

// --- instances -------------------------------------------------------------

func (s *server) listInstances(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	instances, err := s.store.ListInstancesForApp(ctx(r), app.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list instances"))
		return
	}
	out := make([]api.InstanceResponse, 0, len(instances))
	for _, ins := range instances {
		out = append(out, instanceResponse(ins))
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
	app, err := s.store.AppByID(ctx(r), req.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such app")
		return
	}
	token := randomToken(16)
	d, err := s.store.CreateCustomDomain(ctx(r), strings.ToLower(req.Domain), app.ID, token)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusConflict, api.CodeValidation,
			"Domain taken", err.Error()))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyDomainChanged, `{"kind":"created","domain":"`+d.Domain+`"}`)
	// d.Domain came in via the HTTP body (bearer-token authenticated).
	// Sanitize at the log sink — CodeQL go/log-injection (CWE-117).
	// The notify payload above is JSON-encoded so the pg_notify channel
	// can't be tricked into parsing an attacker-supplied structure, but
	// the structured log line is the unencoded sink.
	s.log.Info("domain created", "domain", logsanitize.Field(d.Domain), "app", app.ID, "account", acct.ID)
	writeJSON(w, http.StatusAccepted, domainResponse(d))
}

func (s *server) listDomains(w http.ResponseWriter, r *http.Request, acct state.Account) {
	domains, err := s.store.ListDomainsForAccount(ctx(r), acct.ID)
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
	d, err := s.store.DomainByName(ctx(r), domain)
	if err != nil {
		s.notFound(w, "no such domain")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such domain")
		return
	}
	if err := s.store.DeleteCustomDomain(ctx(r), domain); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete domain"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyDomainChanged, `{"kind":"deleted","domain":"`+domain+`"}`)
	s.log.Info("domain deleted", "domain", domain, "account", acct.ID)
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
	app, err := s.store.AppByID(ctx(r), req.AppID)
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
	c, err := s.store.CreateCron(ctx(r), req.AppID, req.Schedule, path, enabled)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"created","app_id":"`+app.ID+`"}`)
	s.log.Info("cron created", "cron", c.ID, "app", app.ID, "account", acct.ID)
	writeJSON(w, http.StatusCreated, cronResponse(c))
}

func (s *server) listCrons(w http.ResponseWriter, r *http.Request, acct state.Account) {
	// List every cron owned by any of this account's apps.
	apps, err := s.store.ListApps(ctx(r), acct.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not list crons"))
		return
	}
	out := make([]api.CronResponse, 0)
	for _, app := range apps {
		cs, err := s.store.ListCronsForApp(ctx(r), app.ID)
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
	c, err := s.store.CronByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(ctx(r), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	updated, err := s.store.UpdateCron(ctx(r), id, req.Schedule, req.Path, req.Enabled, nil)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"updated","cron":"`+id+`"}`)
	writeJSON(w, http.StatusOK, cronResponse(updated))
}

func (s *server) deleteCron(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	c, err := s.store.CronByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such cron")
		return
	}
	app, err := s.store.AppByID(ctx(r), c.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such cron")
		return
	}
	if err := s.store.DeleteCron(ctx(r), id, c.AppID); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not delete cron"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyCronChanged, `{"kind":"deleted","cron":"`+id+`"}`)
	w.WriteHeader(http.StatusNoContent)
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
	plaintext, hash, err := api.GenerateAPIKey()
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not generate key"))
		return
	}
	k, err := s.store.CreateAPIKey(ctx(r), acct.ID, hash, req.Label, scopes)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not create key"))
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyKeyChanged, `{"kind":"created","account":"`+acct.ID+`"}`)
	s.log.Info("key created", "key", k.ID, "account", acct.ID)
	// IAM-4 (ADR-035): record the key mint. subject = account_id (the
	// owner); data.scopes is the per-key permission set so the
	// audit row can answer "who minted which scopes today?".
	s.audit.Emit(ctx(r), "key.created", &acct.ID, map[string]any{
		"key_id": k.ID,
		"scopes": scopes,
	})
	writeJSON(w, http.StatusCreated, api.APIKeyResponse{
		ID:        k.ID,
		Prefix:    keyPrefix(plaintext),
		Label:     k.Label,
		Scopes:    k.Scopes,
		CreatedAt: k.CreatedAt.UTC().Format(time.RFC3339),
		Plaintext: plaintext,
	})
}

func (s *server) listKeys(w http.ResponseWriter, r *http.Request, acct state.Account) {
	keys, err := s.store.ListAPIKeys(ctx(r), acct.ID)
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
		}
		if !k.LastUsedAt.IsZero() {
			resp.LastUsedAt = k.LastUsedAt.UTC().Format(time.RFC3339)
		}
		out = append(out, resp)
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *server) deleteKey(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	deleted, err := s.store.DeleteAPIKeyReturning(ctx(r), acct.ID, id)
	if err != nil {
		s.notFound(w, "no such key")
		return
	}
	_ = s.notif.Notify(ctx(r), db.NotifyKeyChanged, `{"kind":"deleted","account":"`+acct.ID+`"}`)
	// IAM-4 + IAM-1 (ADR-034 rev2 / ADR-035): record the key
	// revocation carrying the dismissed scopes so an operator can
	// answer "what did this key allow before it died?" without
	// re-deriving it from logs. Single DELETE…RETURNING gives us
	// the row shape without a pre-fetch race.
	s.audit.Emit(ctx(r), "key.deleted", &acct.ID, map[string]any{
		"key_id": deleted.ID,
		"scopes": deleted.Scopes,
	})
	w.WriteHeader(http.StatusNoContent)
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
	rows, err := s.store.UsageByMonth(ctx(r), acct.ID, month)
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
			Detail: "plan upgrades to " + string(plan) + " require an active subscription; complete checkout to upgrade",
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
		if s.billingProvider != nil {
			txID, checkoutURL, err := s.billingProvider.CreateUpgradeTransaction(ctx(r), acct, plan)
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
	if err := s.store.UpdateAccountPlan(ctx(r), acct.ID, plan); err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not update plan"))
		return
	}
	updated, _ := s.store.AccountByID(ctx(r), acct.ID)
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
	s.audit.Emit(ctx(r), "account.plan_changed", &acct.ID, map[string]any{
		"from": string(acct.Plan),
		"to":   string(plan),
	})
	writeJSON(w, http.StatusOK, api.AccountResponse{
		ID: updated.ID, Email: updated.Email, Plan: string(updated.Plan), Status: string(updated.Status),
	})
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
	ev, err := s.billingProvider.VerifyWebhook(body, headers, 5*time.Minute)
	if err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad signature", err.Error()))
		return
	}
	acct, err := s.lookupAccountByPaddleID(r.Context(), ev.CustomerID)
	if err != nil {
		// Unknown customer: 200 so Paddle stops retrying. New
		// customers land via CreateCustomer; we don't auto-provision
		// on a webhook.
		w.WriteHeader(http.StatusOK)
		return
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
	return s.store.AccountByPaddleCustomerID(ctx, paddleID)
}

// --- response helpers ------------------------------------------------------

func (s *server) deploymentResponse(d state.Deployment) api.DeploymentResponse {
	return api.DeploymentResponse{
		ID:          d.ID,
		AppID:       d.AppID,
		BuildID:     d.BuildID,
		ImageDigest: d.ImageDigest,
		Kind:        string(d.Kind),
		Status:      string(d.Status),
		Error:       d.Error,
		ErrorCode:   d.ErrorCode,
		CreatedAt:   d.CreatedAt.UTC().Format(time.RFC3339),
	}
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
	}
}

func instanceResponse(ins state.Instance) api.InstanceResponse {
	r := api.InstanceResponse{
		ID:           ins.ID,
		AppID:        ins.AppID,
		DeploymentID: ins.DeploymentID,
		State:        ins.State,
		HostIP:       ins.HostIP,
		RAMMB:        ins.RAMMB,
		WakeID:       ins.WakeID,
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
	rows, err := s.store.ListDeploymentsForAccount(ctx(r), acct.ID, before, limit)
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
	rows, err := s.store.UsageByMonth(ctx(r), acct.ID, month)
	if err != nil {
		api.WriteProblem(w, api.ErrCapacity("could not load usage"))
		return
	}
	var mbSec int64
	for _, u := range rows {
		mbSec += u.MBSeconds
	}
	usedGB := float64(mbSec) / 3_600_000.0
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
	})
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
//nolint:contextcheck // ctx(r) === r.Context(); suppressed per-call to avoid line-by-line noise in a long SSE handler.
func (s *server) streamDeploymentLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such deployment")
		return
	}
	app, err := s.store.AppByID(ctx(r), d.AppID)
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

	startSSE(w)
	flusher, _ := w.(http.Flusher)

	// Walk backwards: the table returns DESC by seq, the SSE stream
	// wants chronological. MemStore + PgStore both order DESC.
	//nolint:contextcheck // Long SSE handler; ctx(r) == r.Context() but the linter loses the alias across the function's many statements.
	page, _, err := s.store.ListDeploymentLogs(ctx(r), id, beforeSeq, limit)
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
			if d2, err := s.store.DeploymentByID(ctx(r), id); err == nil &&
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

// streamAppLogs streams the running instance's stdout/stderr ring buffer.
// Move 3 stub: the vmmd Logs(req) gRPC stream is the real implementation
// (pkg/fcvm/vmmd exposes a per-instance ring buffer). Until that lands,
// emit a `not_implemented` frame and a terminal `end` so the SDK
// contract (pkg/api/logs.go::StreamAppLogs) keeps returning a 200 with
// parseable frames and the dashboard's per-app log button doesn't 404.
// Move 4 replaces the body.
func (s *server) streamAppLogs(w http.ResponseWriter, r *http.Request, acct state.Account) {
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if _, err := s.store.ListInstancesForApp(ctx(r), app.ID); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusNotFound, api.CodeNotFound,
			"No running instance", "the app is parked; wake it first"))
		return
	}
	startSSE(w)
	flusher, _ := w.(http.Flusher)
	// Two frames then close: a not_implemented signal so a client can
	// distinguish "not built yet" from "empty stream" + a terminal
	// `end` so consumers with a structured-frame loop exit cleanly
	// (the build-tailing streamDeployLogs pattern at
	// cmd/faas/commands2.go uses the same `end` sentinel).
	_, _ = fmt.Fprint(w, "event: not_implemented\ndata: {\"reason\":\"vmmd Logs(req) gRPC pending — Move 4\"}\n\n")
	_, _ = fmt.Fprint(w, "event: end\ndata: {}\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

// startSSE sets the SSE response headers and disables write timeouts for the
// lifetime of the request.
func startSSE(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	w.(http.Flusher).Flush()
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
//
// Per ADR-034 rev2 the route is gated by the same `build:read`
// scope the rest of the build surface uses.
func (s *server) getBuildProvenance(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	build, err := s.store.BuildByID(ctx(r), id)
	if err != nil {
		s.notFound(w, "no such build")
		return
	}
	dep, err := s.store.DeploymentByID(ctx(r), build.DeploymentID)
	if err != nil {
		s.notFound(w, "no such build")
		return
	}
	app, err := s.store.AppByID(ctx(r), dep.AppID)
	if err != nil || app.AccountID != acct.ID {
		s.notFound(w, "no such build")
		return
	}
	prov, err := s.store.BuildProvenanceByBuildID(ctx(r), build.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrBuildProvenanceNotFound())
		return
	}
	writeJSON(w, http.StatusOK, s.buildProvenanceResponse(prov))
}
