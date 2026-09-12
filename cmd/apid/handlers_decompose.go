package main

// handlers_decompose.go — Phase 3 HTTP routes.
//
// Two endpoints:
//
//	POST /v1/projects/scan  — dry-run; runs reposcan.Scan, returns a
//	                          plan with can_apply + plan_token; never
//	                          writes.
//	POST /v1/projects       — applies the plan in one Tx; emits
//	                          NotifyAppChanged per inserted app.
//
// Both are authed + requireMFA + requireScope(deploy:write). Apply
// additionally takes s.idempotent (via the mux wrap in server.go).
//
// Filesize: handlers stay ≤50 lines per project guideline. Anything
// thicker (multipart parse, extract, scan, quota) lives in
// scan_service.go and extract.go; this file is the
// auth/middleware/orchestration seam only.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// exclusionSyntheticAppNamespace is the UUID v5 namespace that
// derives a synthetic app_id for a deployment_scope_exclusions row
// when the operator excluded a brand-new (or otherwise unresolvable)
// workload. The schema's app_id column is `uuid NOT NULL` with NO
// FK to apps(id) (see migration 00418 soft-delete CASCADE blind
// spot), so we MUST populate it; rather than letting an empty
// string blow up the INSERT with `invalid input syntax for type
// uuid` (data-loss: the apply already succeeded but the persist
// write fails AND the audit row never fires), we hash a stable
// tuple (accountID, projectID, slug) into a deterministic UUID.
//
// Stable across process restarts and replicas because the input is
// fixed and SHA-1(v5) is deterministic; the same excluded workload
// re-applying tomorrow produces the same synthetic app_id, which
// keeps the janitor's "orphaned app_id" check idempotent (a row's
// app_id is computed once and never re-rolled).
//
// Frozen at design time; the TestExclusionSyntheticAppNamespace
// test pins the literal so any rotation forces a code change. See
// pkg/state/types.go::PersonalOrgNamespace for the same precedent.
var exclusionSyntheticAppNamespace = uuid.MustParse("b9c0d6a0-7f0c-5a18-ae00-000000000001")

// syntheticExclusionAppID returns the deterministic UUID used when
// an excluded workload has no real apps row to point at — typically
// because the workload is brand-new in the scan and was excluded
// before reconcile inserted its apps row (the apply-side scan
// runs before reconcile inserts, so Skipped[].ID is "" on the
// brand-new path). Pre-reconcile brand-new paths are the common
// case for `--exclude` against new deploys. The synthetic id is
// distinguishable from real app ids only by namespace; the audit
// log carries the source tuple alongside it so SOC 2 reviewers can
// reconstruct the brand-new origin.
func syntheticExclusionAppID(accountID, projectID, slug string) string {
	return uuid.NewSHA1(exclusionSyntheticAppNamespace,
		[]byte(accountID+":"+projectID+":"+slug)).String()
}

// scanProject is the dry-run endpoint. It never writes; the response
// is the same scanPlanResponse that the apply endpoint emits so the
// CLI's `--json` mode passes the bytes through verbatim.
func (s *server) scanProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	resp, _, _, _, _, _, prob := s.scanService(r, acct, "", false)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

// applyProject is the transactional endpoint. On success it emits one
// NotifyAppChanged per inserted/updated app so schedd can pick up
// the rows without polling; on quota failure it surfaces the matching
// RFC 7807 problem with limit + observed + docs URL and zero rows
// inserted (the store Tx rolled back).
//
// PR-G (repo decomposition Phase 5): the workload-mutation path
// moved from store.ApplyProjectPlan to pkg/reconcile.Service. The
// scanService now creates the projects row + reconciles apps under
// one logical flow. This handler is responsible for:
//
//  1. Reading the reconcile Result's added + changed slices from
//     scanService's third + fourth return values.
//  2. Stamping cron app_id from the post-reconcile slug→ID map.
//  3. Emitting per-app NotifyAppChanged (kind=created for added,
//     kind=updated for changed) so schedd picks up the rows.
//  4. Emitting the project.created audit row (gated by the
//     CreateProject call inside scanService — duplicate slugs are
//     rejected with ErrConflict before reconcile runs).
//  5. Rendering per-workload appliedBuild results in the response
//     so the CLI's `faas apply` flow can show "app X: deployment Y,
//     build Z" per workload (PR-A, Phase 5 close-the-loop).
func (s *server) applyProject(w http.ResponseWriter, r *http.Request, acct state.Account) {
	planToken := r.URL.Query().Get("plan_token")
	resp, insertedProject, added, changed, removedSlugs, builds, prob := s.scanService(r, acct, planToken, true)
	if prob != nil {
		api.WriteProblem(w, prob)
		return
	}

	// The reconcile path emits its own per-action audit rows
	// (project.workload.added / project.workload.changed); the
	// apid-side audit pipeline only fires the per-project
	// project.created row (at the bottom of this function).

	// Cron replacement is now part of reconcile's atomic project mutation.
	// Keep the legacy fallback below disabled for stores that implement the
	// new seam; it remains documented here only for source compatibility with
	// older test doubles.
	if _, atomic := s.store.(state.ProjectReconcileStore); !atomic {
		// Soft-delete crons for workloads the scan dropped (H8 fix).
		// Pre-fix, the cron-stamping loop below tried to look up
		// appID by slug for every cron in resp.CronNames; a workload
		// that was removed by the scan has no entry in the Added ∪
		// Changed slug map, and the handler 500'd. Today the loop
		// only touches crons in the NEW plan; orphans (a cron for a
		// removed workload) are soft-deleted here so the cron list
		// stays in sync with the workload list.
		for _, slug := range removedSlugs {
			// Find the app_id for the removed workload. The app
			// was just soft-deleted by reconcile, so its row is
			// still readable (PR-E sets status=AppDeleted).
			apps, lerr := s.store.AppsForProject(r.Context(), acct.ID, insertedProject.ID)
			if lerr != nil {
				s.audit.Emit(r.Context(), "cron.removed_orphan", &acct.ID, map[string]any{
					"project_id": insertedProject.ID,
					"slug":       slug,
					"err":        lerr.Error(),
				})
				continue
			}
			var appID string
			for _, a := range apps {
				if a.Slug == slug {
					appID = a.ID
					break
				}
			}
			if appID == "" {
				// The removed workload's app row was hard-deleted
				// somewhere (or never existed) — best-effort,
				// log the orphan.
				s.audit.Emit(r.Context(), "cron.removed_orphan", &acct.ID, map[string]any{
					"project_id": insertedProject.ID,
					"slug":       slug,
					"reason":     "no matching app row",
				})
				continue
			}
			// Soft-delete every cron attached to the removed
			// workload's app. The Cron row has no separate
			// workload_name column (one app = one workload in the
			// scan-tied model), so every cron on this app is
			// orphaned by the removal. DeleteCron is the
			// soft-delete primitive (status moves to
			// app-deleted via the parent row already being
			// soft-deleted by reconcile).
			cs, lerr := s.store.ListCronsForApp(r.Context(), appID)
			if lerr != nil {
				s.audit.Emit(r.Context(), "cron.removed_orphan", &acct.ID, map[string]any{
					"project_id": insertedProject.ID,
					"slug":       slug,
					"err":        lerr.Error(),
				})
				continue
			}
			for _, c := range cs {
				if err := s.store.DeleteCron(r.Context(), c.ID, appID); err != nil {
					s.audit.Emit(r.Context(), "cron.removed_orphan", &acct.ID, map[string]any{
						"project_id": insertedProject.ID,
						"slug":       slug,
						"cron_id":    c.ID,
						"err":        err.Error(),
					})
					continue
				}
				s.audit.Emit(r.Context(), "cron.removed", &acct.ID, map[string]any{
					"project_id": insertedProject.ID,
					"app_id":     appID,
					"cron_id":    c.ID,
					"slug":       slug,
				})
			}
		}

		// Build the slug→ID map for cron stamping. The cron list is
		// encoded by name in resp.CronNames; both Added and Changed
		// contribute to the map because a cron can attach to either a
		// newly-created or an updated app. Removed-slug crons were
		// soft-deleted above; the loop below only stamps NEW crons.
		slugToID := make(map[string]string, len(added)+len(changed))
		for _, a := range added {
			slugToID[a.Slug] = a.ID
		}
		for _, a := range changed {
			slugToID[a.Slug] = a.ID
		}
		for i, name := range resp.CronNames {
			appID := slugToID[name]
			if appID == "" {
				// H8 fix: removed workload — the cron for it was
				// soft-deleted in the loop above. This branch is
				// now a defensive no-op rather than a 500.
				continue
			}
			// The schedule + path + enabled flags ride on the planCron
			// entry captured earlier. resp.Crons has the same length
			// + order as resp.CronNames (scan_service.go populates
			// them in the same loop).
			if i >= len(resp.Crons) {
				continue
			}
			c := resp.Crons[i]
			if _, err := s.store.CreateCron(r.Context(), appID, c.Schedule, c.Path, c.Enabled); err != nil {
				api.WriteProblem(w, api.ErrInternal(
					fmt.Sprintf("stamp cron app_id: %v", err)))
				return
			}
		}
	}

	// Notify every touched app. PostgreSQL delivers the
	// notification only on Tx commit; reconcile's CreateAppIfUnderQuota
	// + UpdateApp have already committed per-row (each call wraps
	// its own Tx), so the post-reconcile emit is safe — schedd
	// sees the rows.
	//
	// kind=created fires for Added; kind=updated fires for Changed.
	// The latter keeps schedd in sync if a workload's RootDir /
	// WorkloadName / StartCommand drifted across two applies.
	//
	// ctx is captured explicitly so the closure doesn't extend
	// the lifetime of r (contextcheck linter).
	notifyCtx := r.Context()
	notifyApp := func(a state.App, kind string) {
		_ = s.notif.Notify(notifyCtx, db.NotifyAppChanged,
			fmt.Sprintf(`{"kind":%q,"app_id":"%s","project_id":"%s"}`,
				kind, a.ID, insertedProject.ID))
	}
	for _, a := range added {
		notifyApp(a, "created")
	}
	for _, a := range changed {
		notifyApp(a, "updated")
	}

	// Audit + response. Response body mirrors the scan output but
	// carries the inserted/updated IDs so the CLI's --yes flow
	// can render "applied: <slug> → <app_id>".
	type applyResp struct {
		scanPlanResponse
		ProjectID string         `json:"project_id"`
		AppIDs    []appSummary   `json:"apps"`
		Builds    []appliedBuild `json:"builds,omitempty"`
	}
	appIDs := make([]appSummary, 0, len(added)+len(changed))
	for _, a := range added {
		appIDs = append(appIDs, appSummary{Slug: a.Slug, ID: a.ID})
	}
	for _, a := range changed {
		appIDs = append(appIDs, appSummary{Slug: a.Slug, ID: a.ID})
	}
	out := applyResp{
		scanPlanResponse: *resp,
		ProjectID:        insertedProject.ID,
		AppIDs:           appIDs,
		Builds:           builds,
	}

	s.audit.Emit(r.Context(), "project.created", &acct.ID, map[string]any{
		"project_id":  insertedProject.ID,
		"slug":        insertedProject.Slug,
		"scan_source": string(insertedProject.ScanSource),
		"app_count":   len(added) + len(changed),
	})

	// ADR-124 follow-up #3 (PR-B commit 5): write the operator's
	// persisted exclusions to deployment_scope_exclusions on a
	// successful apply when --persist-exclude was set. The
	// partition Skipped list carries every slug the operator
	// excluded (post-fallback, so persisted carry-forward slugs
	// are included if they were excluded this deploy). Idempotent
	// on duplicate (23505 → ErrConflict is treated as a no-op
	// because the row already exists from a prior deploy). Audit
	// row emitted per slug for SOC 2 CC7.2 paper trail.
	if resp.PersistExclude && len(resp.Skipped) > 0 {
		for _, skipped := range resp.Skipped {
			// Find the freshly-inserted app_id for this slug (it
			// exists because reconcile just inserted every
			// workload as a fresh app row on the apply path).
			var appID string
			for _, a := range added {
				if a.Slug == skipped.Slug {
					appID = a.ID
					break
				}
			}
			// The slug is in Skipped but didn't match an added
			// app — this can happen when the operator excluded an
			// existing app via --exclude (the audit fix at
			// TestReconcile_ExcludePreventsRemove covers the
			// apply-side contract). Lookup the existing app id.
			if appID == "" {
				if apps, lerr := s.store.AppsForProject(r.Context(), acct.ID, insertedProject.ID); lerr == nil {
					for _, a := range apps {
						if a.Slug == skipped.Slug {
							appID = a.ID
							break
						}
					}
				}
			}
			// Code-review fix #1: app_id is `uuid NOT NULL` in
			// migration 00418. Empty appID would produce
			// `invalid input syntax for type uuid` from the
			// INSERT and silently lose the persist write (the
			// audit row would never fire because perr != nil,
			// and perr is non-conflict so we'd skip the audit
			// log entirely via the `continue` branch below).
			// Brand-new excluded workloads have no apps row yet
			// because reconcile runs *after* this loop in the
			// apply path. Substitute a deterministic synthetic
			// UUID derived from (account, project, slug) — the
			// schema's no-FK posture means a synthetic id is
			// safe; the audit log carries the source tuple
			// alongside it so SOC 2 reviewers can reconstruct
			// the brand-new origin.
			if appID == "" {
				appID = syntheticExclusionAppID(acct.ID, insertedProject.ID, skipped.Slug)
			}
			row, perr := s.store.CreateDeploymentScopeExclusion(r.Context(), state.DeploymentScopeExclusion{
				AccountID: acct.ID,
				ProjectID: insertedProject.ID,
				AppID:     appID, // synthetic on brand-new excluded workload path (see fix #1)
				Slug:      skipped.Slug,
				Reason:    "persisted_via_flag",
				CreatedBy: "cli", // future: thread actor from the auth context
			})
			if perr != nil && !errors.Is(perr, state.ErrConflict) {
				// Don't fail the apply on a persist-write miss;
				// log the error via the audit log and continue.
				// The apply already succeeded — losing one
				// persist write is acceptable per the ADR-127 §1
				// audit-log-is-durable-record posture.
				s.audit.Emit(r.Context(), "project.scope.excluded", &acct.ID, map[string]any{
					"project_id":    insertedProject.ID,
					"workload_name": skipped.Slug,
					"reason":        "persist_write_failed",
					"err":           perr.Error(),
				})
				continue
			}
			// Audit row: only emit when we actually wrote the
			// row (ErrConflict means it already existed from a
			// prior deploy).
			if perr == nil && row.ID != "" {
				s.audit.Emit(r.Context(), "project.scope.excluded", &acct.ID, map[string]any{
					"project_id":    insertedProject.ID,
					"app_id":        row.AppID,
					"workload_name": row.Slug,
					"reason":        row.Reason,
				})
			}
		}
	}

	// Audit operator exclusions only after every apply-side mutation
	// above has completed successfully. The scan endpoint deliberately
	// returns the same Skipped partition without writing audit rows.
	s.emitProjectApplyExclusionAudits(r.Context(), r, acct, insertedProject, resp, added, changed)

	writeJSON(w, http.StatusOK, out)
}

// emitProjectApplyExclusionAudits records the exclusion decisions made by a
// completed project apply. Keeping this seam after reconcile, cron stamping,
// and persisted-scope writes ensures a read-only scan (or a failed apply)
// cannot leave durable audit history behind.
func (s *server) emitProjectApplyExclusionAudits(
	ctx context.Context,
	r *http.Request,
	acct state.Account,
	project state.Project,
	resp *scanPlanResponse,
	added, changed []state.App,
) {
	if s.audit == nil || resp == nil {
		return
	}
	actor := resolvedActorString(routeKindForRequest(r), acct.ID, "")
	appIDs := make(map[string]string, len(resp.Skipped)+len(added)+len(changed))
	for _, row := range resp.Skipped {
		if row.ID != "" {
			appIDs[strings.ToLower(row.Slug)] = row.ID
		}
	}
	for _, app := range added {
		appIDs[strings.ToLower(app.Slug)] = app.ID
	}
	for _, app := range changed {
		appIDs[strings.ToLower(app.Slug)] = app.ID
	}

	if len(resp.Skipped) > 0 {
		rows := append([]api.PlanAffectedApp(nil), resp.Skipped...)
		for i := range rows {
			if rows[i].ID == "" {
				rows[i].ID = appIDs[strings.ToLower(rows[i].Slug)]
			}
		}
		emitSkippedAuditRows(ctx, s.audit, actor, acct.ID, project.ID, resp.SourceSHA256, rows)
	}

	if len(resp.PersistedExclusions) == 0 {
		return
	}
	// Persisted rows carry the last known app snapshot, including the
	// deterministic synthetic id used for a brand-new excluded workload.
	// Resolve that snapshot after the apply so the audit event remains tied
	// to the project that actually completed.
	if persisted, err := s.store.LookupDeploymentScopeExclusions(ctx, acct.ID, project.ID); err == nil {
		for _, row := range persisted {
			if appIDs[strings.ToLower(row.Slug)] == "" {
				appIDs[strings.ToLower(row.Slug)] = row.AppID
			}
		}
	} else {
		s.log.Warn("apid: load persisted exclusions for audit", "project_id", project.ID, "err", err)
	}
	emitPersistedExcludedAuditRows(ctx, s.audit, actor, acct.ID, project.ID, resp.SourceSHA256, resp.PersistedExclusions, appIDs)
}

// appSummary is the per-app line in the apply response. Declared at
// package scope so the applyResp literal in applyProject can name it
// (Go forbids type declarations inside a function from being
// referenced by another type literal in the same scope).
type appSummary struct {
	Slug string `json:"slug"`
	ID   string `json:"id"`
}

// deleteDeploymentScopeExclusion handles
// DELETE /v1/projects/{slug}/exclusions/{slug2}. ADR-124 code-review
// fix #2 — operator escape hatch for stale persisted exclusions
// (workload renamed or deleted in a future commit, blocking
// subsequent deploys via exclude_unknown_slug).
//
// The route resolves (account, project, slug) before delegating to
// store.DeleteDeploymentScopeExclusion so the audit row carries
// the operator's identity and a human-readable reason. Idempotent
// at the store level — DELETE on a non-existent row returns
// state.ErrNotFound, which the handler translates to
// 404 scope_exclusion_not_found (the CLI's
// `gregale deployments exclude clear` branches on this code via
// errors.As to render "already clear" without surfacing a hard
// error).
//
// Auth: authLimited + requireMFA + ScopesDeployWriteSurface
// (wiring in the route table at server.go:1251). Mutating the
// persisted exclusion set changes which workloads auto-exclude on
// subsequent deploys — same write-class as the apply path.
func (s *server) deleteDeploymentScopeExclusion(w http.ResponseWriter, r *http.Request, acct state.Account) {
	projectSlug := r.PathValue("slug")
	excludedSlug := r.PathValue("slug2")
	if projectSlug == "" || excludedSlug == "" {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, "bad_request",
			"Missing path parameters", "both project slug and excluded slug are required"))
		return
	}
	projectSlug = strings.ToLower(strings.TrimSpace(projectSlug))
	excludedSlug = strings.ToLower(strings.TrimSpace(excludedSlug))
	// Resolve the project so the store call keys off the right
	// (account_id, project_id) tuple. ErrNotFound on the project
	// surfaces as scope_exclusion_not_found too — the operator
	// can't tell the difference between "no project" and "no
	// exclusion" from the CLI's perspective and shouldn't be able
	// (project existence is already leaked via the apply path).
	proj, projErr := s.store.ProjectBySlug(r.Context(), acct.ID, projectSlug)
	if projErr != nil {
		if errors.Is(projErr, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "scope_exclusion_not_found",
				"no persisted exclusion matches",
				fmt.Sprintf("project=%q slug=%q", projectSlug, excludedSlug)))
			return
		}
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load project for exclusion delete: %v", projErr)))
		return
	}
	if err := s.store.DeleteDeploymentScopeExclusion(r.Context(), acct.ID, proj.ID, excludedSlug); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.NewProblem(http.StatusNotFound, "scope_exclusion_not_found",
				"no persisted exclusion matches",
				fmt.Sprintf("project=%q slug=%q", projectSlug, excludedSlug)))
			return
		}
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError, "internal",
			"Delete failed", err.Error()))
		return
	}
	// Audit the manual override so SOC 2 reviewers can tell the
	// difference between an exclusion removed because the project
	// is brand-new vs an exclusion explicitly cleared by an
	// operator (data-loss review).
	s.audit.Emit(r.Context(), "project.scope.excluded", &acct.ID, map[string]any{
		"project_id":    proj.ID,
		"workload_name": excludedSlug,
		"reason":        "cleared_via_cli",
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// quotaProblem maps a *state.QuotaError into the right RFC 7807
// problem. Phase 3 only emits two quota shapes:
//
//   - apps:    403 plan_limit_apps        (with limit + observed)
//   - crons:   402 plan_crons_not_allowed (NotAllowed=true)
//     403 plan_cron_quota        (Limit > 0 but exceeded)
//
// Both already have helper constructors in pkg/api/errors.go; this
// function just translates the state's Kind enum into the helper call
// so the handler body stays a single switch.
func quotaProblem(plan api.Plan, l api.Limits, qe *state.QuotaError) *api.Problem {
	if qe == nil {
		return api.ErrInternal("quota error missing body")
	}
	switch qe.Kind {
	case state.QuotaErrorKindCrons:
		if qe.NotAllowed {
			return api.ErrPlanCronsNotAllowed(plan)
		}
		return api.ErrPlanCronQuota(plan, "account", qe.Limit, qe.Observed)
	case state.QuotaErrorKindApps:
		return api.ErrPlanLimitApps(l, qe.Observed)
	default:
		return api.ErrInternal("unknown quota kind")
	}
}

// Ensure unused imports stay bound so future edits can pull them in
// without a separate import pass. The error types below are
// re-exported by the file for callers (e.g. cmd/gregale) and should
// always be present.
var (
	_ = json.Marshal
)
