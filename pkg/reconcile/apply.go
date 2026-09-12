// Apply operations. Three concerns:
//   - quota re-check (per-project membership cap) BEFORE the
//     create set hits the store. The store re-checks under each
//     per-app Tx; the pre-check here is best-effort and emits the
//     audit row.
//   - StartCommand emission. The PR-E widening accepts *string;
//     the empty-string-means-NULL convention is enforced by the
//     store's nullString helper, so apply.go just passes the
//     resolved string.
//   - removed BEFORE SoftDeleteAppCascade. ADR-035 best-effort +
//     Audit.Emit returns no error, so the audit row exists even
//     when the cascade SQL fails halfway. Order matters for the
//     audit-ordering test.
//
// Implementation note: creates run per-app via
// CreateAppIfUnderQuota (not ApplyProjectPlan — that method is
// project-create + apps + crons in one Tx, and the reconcile path
// already has an existing project and no crons to insert).

package reconcile

import (
	"context"
	"errors"
	"fmt"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Audit-row payload keys for quota_blocked alerts. Declared as
// constants so the pre-check path and the inner-QuotaError path
// can't drift on key names (goconst would also catch this, but
// typed constants are stronger — a typo at the call site fails
// the build).
const (
	auditQuotaLimit          = "limit"
	auditQuotaObserved       = "observed"
	auditQuotaWouldbeCount   = "wouldbe_count"
	auditQuotaSkippedCreates = "skipped_creates"
)

func fieldsChangedForAction(actions []Action, appID string) []string {
	for _, action := range actions {
		if action.Op == "update" && action.App.ID == appID {
			return action.fieldsChanged
		}
	}
	return nil
}

// applyActions executes the diff. Quota pre-check is done here so
// the audit row is emitted before any SQL runs. Updates and
// removes always run; creates run per-app via
// CreateAppIfUnderQuota, which re-checks quota under its own Tx.
//
// existing is the same slice reconcile_internal.go passed to
// workloadDiff — already filtered to status <> 'deleted' by
// store.AppsForProject. Re-using it for the quota pre-check
// avoids a second round-trip and keeps the count consistent
// with the diff.
func (s *Service) applyActions(
	ctx context.Context,
	project state.Project,
	actions []Action,
	existing []state.App,
	commitSHA string,
	scan reposcan.Result,
	cronSpecs []CronSpec,
) (Result, error) {
	availableServices := workloadNameSet(scan.Workloads)
	var out Result

	// Partition by op so we can run them in the documented order
	// (updates, removes, creates) and so the per-op quota + audit
	// logic is independent.
	var creates, updates, removes []Action
	for _, a := range actions {
		switch a.Op {
		case "create":
			creates = append(creates, a)
		case "update":
			updates = append(updates, a)
		case "remove":
			removes = append(removes, a)
		default:
			// Defensive: a typo in Op would otherwise be silently
			// dropped. Reconcile's internal Action.Op set is closed
			// (workloadDiff only emits create/update/remove), so
			// hitting this is a programming error.
			panic(fmt.Sprintf("reconcile: unknown action op %q", a.Op))
		}
	}

	// 1. Resolve the authoritative plan from the account. The
	// project does not carry a plan today; the limits table is
	// keyed on Account.Plan (single source of truth for quotas).
	acct, err := s.Store.AccountByID(ctx, project.AccountID)
	if err != nil {
		return out, err
	}
	limits := s.Limits(acct.Plan)
	planCap := limits.DeployedApps
	if planCap == 0 {
		// Defensive fallback — the limits table always has a
		// row for a known plan; anything else is a programmer
		// error. The per-app CreateAppIfUnderQuota calls still
		// enforce the authoritative cap under their own Tx.
		planCap = api.MustLimitsFor(api.PlanScale).DeployedApps
	}

	// PgStore and MemStore expose one transaction for project apply. Use it
	// whenever available so app updates/removals/creates, cron replacement,
	// quota checks, and scan-source upgrades commit or roll back together.
	if txStore, ok := s.Store.(state.ProjectReconcileStore); ok {
		// Preserve the legacy ordering: updates, removes, then creates. A
		// replacement create must not collide with the row it supersedes.
		orderedActions := make([]Action, 0, len(actions))
		orderedActions = append(orderedActions, updates...)
		orderedActions = append(orderedActions, removes...)
		orderedActions = append(orderedActions, creates...)
		mutations := make([]state.ProjectReconcileMutation, 0, len(orderedActions))
		for _, action := range orderedActions {
			app := action.App
			switch action.Op {
			case "create":
				app = workloadToDraftApp(project, action.Workload, action.StartCommand, acct.Plan, availableServices)
			case "update":
				manifest := action.App.Manifest
				manifest.Env = serviceEnvForWorkloadWithAvailable(manifest.Env, action.Workload, availableServices)
				app.RootDir = action.Workload.RootDir
				app.WorkloadName = action.Workload.Name
				app.StartCommand = action.StartCommand
				app.Manifest = manifest
			}
			mutations = append(mutations, state.ProjectReconcileMutation{Op: action.Op, App: app})
		}
		var desiredCrons []state.ProjectReconcileCron
		if cronSpecs != nil {
			desiredCrons = make([]state.ProjectReconcileCron, 0, len(cronSpecs))
			for _, cron := range cronSpecs {
				desiredCrons = append(desiredCrons, state.ProjectReconcileCron{
					WorkloadName: cron.WorkloadName,
					Schedule:     cron.Schedule,
					Path:         cron.Path,
					Enabled:      cron.Enabled,
				})
			}
		}
		scanSource := DeriveScanSource(scan.Workloads)
		if len(mutations) > 0 || cronSpecs != nil || tierRank(scanSource) > tierRank(project.ScanSource) {
			applied, err := txStore.ApplyProjectReconcile(ctx, project, mutations, desiredCrons, scanSource, limits)
			if err != nil {
				var qe *state.QuotaError
				if errors.As(err, &qe) {
					skipped := make([]string, 0, len(creates))
					for _, create := range creates {
						skipped = append(skipped, create.Workload.Name)
					}
					s.emitReconcileQuotaBlocked(ctx, project, qe.Limit, qe.Observed, len(creates), skipped, commitSHA)
					out.Alerts = append(out.Alerts, Alert{Kind: AlertKindQuotaBlocked, Message: "project apply rejected by store quota", Data: map[string]any{
						auditQuotaLimit: qe.Limit, auditQuotaObserved: qe.Observed, auditQuotaWouldbeCount: len(creates), auditQuotaSkippedCreates: skipped,
					}})
				}
				return out, err
			}
			for _, changed := range applied.Changed {
				s.emitWorkloadChanged(ctx, project, changed, fieldsChangedForAction(actions, changed.ID), commitSHA)
				out.Changed = append(out.Changed, changed)
			}
			for _, removed := range applied.Removed {
				s.emitWorkloadRemoved(ctx, project, removed.ID, removed.WorkloadName, commitSHA)
				out.Removed = append(out.Removed, removed.ID)
			}
			for _, added := range applied.Added {
				s.emitWorkloadAdded(ctx, project, added, commitSHA)
				out.Added = append(out.Added, added)
			}
			out.scanSourceApplied = tierRank(scanSource) > tierRank(project.ScanSource)
			return out, nil
		}
	}

	// 2. Updates first (no quota concern).
	for _, u := range updates {
		changed, err := s.applyUpdate(ctx, project, u, commitSHA, availableServices)
		if err != nil {
			return out, err
		}
		out.Changed = append(out.Changed, changed)
	}

	// 3. Removes next. Emit the audit row BEFORE the cascade so
	// the audit-ordering test pins the order.
	for _, r := range removes {
		if err := s.applyRemove(ctx, project, r, commitSHA); err != nil {
			return out, err
		}
		out.Removed = append(out.Removed, r.App.ID)
	}

	// 4. Creates last. Empty create set is a no-op.
	if len(creates) == 0 {
		return out, nil
	}
	// Member-of quota pre-check. DeployedApps is the per-plan
	// cap. The pre-check is best-effort; each per-app
	// CreateAppIfUnderQuota call re-checks under its own Tx and
	// returns *QuotaError if it loses the race.
	projected := len(existing) + len(creates)
	if projected > planCap {
		skipped := make([]string, 0, len(creates))
		for _, c := range creates {
			skipped = append(skipped, c.Workload.Name)
		}
		s.emitReconcileQuotaBlocked(
			ctx, project,
			planCap,
			len(existing),
			len(creates),
			skipped,
			commitSHA,
		)
		out.Alerts = append(out.Alerts, Alert{
			Kind:    AlertKindQuotaBlocked,
			Message: "create set would exceed deployed_apps quota",
			Data: map[string]any{
				auditQuotaLimit:          planCap,
				auditQuotaObserved:       len(existing),
				auditQuotaWouldbeCount:   len(creates),
				auditQuotaSkippedCreates: skipped,
			},
		})
		// An incomplete apply is an error, never a successful response
		// carrying only an alert.
		return out, &state.QuotaError{Kind: state.QuotaErrorKindApps, Limit: planCap, Observed: projected}
	}

	// Build the store.App slice for the create set. CreateAppIfUnderQuota
	// is the per-app quota-aware insert path (memstore.go:1286 /
	// pgstore.go:1090). It does NOT touch the project row — the
	// project already exists when reconcile runs (the create-project
	// Tx is the apid path, not the reconcile path).
	newApps := make([]state.App, 0, len(creates))
	for _, c := range creates {
		app := workloadToDraftApp(project, c.Workload, c.StartCommand, acct.Plan, availableServices)
		newApps = append(newApps, app)
	}
	added := make([]state.App, 0, len(newApps))
	for _, a := range newApps {
		created, err := s.Store.CreateAppIfUnderQuota(ctx, a, limits)
		if err != nil {
			// If the inner call raised a QuotaError, convert to
			// an alert and return (so the caller's behavioral
			// contract is consistent with the pre-check path).
			var qe *state.QuotaError
			if errors.As(err, &qe) {
				// Audit covers the FULL create set — but the
				// skipped_creates list must reflect only the
				// names that did NOT make it into `added` (i.e.,
				// the ones still pending a quota lift).
				skipped := namesFromActionsNotIn(creates, added)
				s.emitReconcileQuotaBlocked(
					ctx, project,
					qe.Limit,
					qe.Observed,
					len(creates),
					skipped,
					commitSHA,
				)
				out.Alerts = append(out.Alerts, Alert{
					Kind:    AlertKindQuotaBlocked,
					Message: "create set rejected by store quota",
					Data: map[string]any{
						auditQuotaLimit:          qe.Limit,
						auditQuotaObserved:       qe.Observed,
						auditQuotaWouldbeCount:   len(creates),
						auditQuotaSkippedCreates: skipped,
					},
				})
				out.Added = added
				return out, err
			}
			return out, err
		}
		// Emit the workload.added audit row per-app as it lands.
		// Doing this inside the loop (rather than after the full
		// create set) means a QuotaError that fires mid-loop
		// still leaves the partial-success rows in the audit log.
		s.emitWorkloadAdded(ctx, project, created, commitSHA)
		added = append(added, created)
	}
	out.Added = added
	return out, nil
}

// applyUpdate runs a single UpdateApp and emits the
// workload.changed audit row with the commit SHA. Returns the
// post-update App and any error. An empty App is never returned
// on success — errors propagate to the caller.
func (s *Service) applyUpdate(
	ctx context.Context,
	project state.Project,
	a Action,
	commitSHA string,
	available ...map[string]struct{},
) (state.App, error) {
	rootDir := a.Workload.RootDir
	workloadName := a.Workload.Name
	manifest := a.App.Manifest
	var serviceNames map[string]struct{}
	if len(available) > 0 {
		serviceNames = available[0]
	}
	manifest.Env = serviceEnvForWorkloadWithAvailable(manifest.Env, a.Workload, serviceNames)
	workloadClass := workloadClassFromScan(a.Workload)
	params := state.UpdateAppParams{
		RootDir:       &rootDir,
		WorkloadName:  &workloadName,
		WorkloadClass: &workloadClass,
		StartCommand:  &a.StartCommand,
		Manifest:      &manifest,
	}
	updated, err := s.Store.UpdateApp(ctx, a.App.ID, params)
	if err != nil {
		return state.App{}, err
	}
	s.emitWorkloadChanged(ctx, project, updated, a.fieldsChanged, commitSHA)
	return updated, nil
}

// applyRemove runs a single SoftDeleteAppCascade. The audit row
// is emitted BEFORE the SQL so the audit-ordering test pins the
// order. The cascading behaviour is status-only per the PR-E
// user decision; envs, crons, custom_domains survive.
func (s *Service) applyRemove(
	ctx context.Context,
	project state.Project,
	a Action,
	commitSHA string,
) error {
	s.emitWorkloadRemoved(ctx, project, a.App.ID, a.App.WorkloadName, commitSHA)
	_, err := s.Store.SoftDeleteAppCascade(ctx, a.App.ID)
	return err
}

// workloadToDraftApp replaces the legacy cmd/apid
// `wlToApp` helper (retired in PR-GH.2). The Slug is w.Name
// verbatim — apps.slug is unconstrained in the schema
// (migrations/00074_…:141-145 documents the historical
// allowance of uppercase, dots, longer
// strings), so the apid path and the reconcile path must agree
// byte-for-byte to keep customer URLs stable. Any future
// normalisation must land in BOTH paths at the same time, with
// a slug migration. The store stamps id, created_at, status, and
// backfills project_id on insert.
//
// plan is the owning account's plan — used to stamp the per-plan
// default for require_authn + public_auth_mode (issue #695 / ADR-080).
// Without this stamp the project-sync path bypasses apid's buildApp
// and a Pro customer gets a public-by-default app via `faas project
// sync` while direct POST /v1/apps on the same plan returns
// bearer-by-default — same plan, two different defaults.
func workloadToDraftApp(project state.Project, w reposcan.Workload, startCmd string, plan api.Plan, available ...map[string]struct{}) state.App {
	class := workloadClassFromScan(w)
	var serviceNames map[string]struct{}
	if len(available) > 0 {
		serviceNames = available[0]
	}
	return state.App{
		AccountID:      project.AccountID,
		ProjectID:      project.ID,
		Slug:           w.Name,
		RootDir:        w.RootDir,
		WorkloadName:   w.Name,
		WorkloadClass:  class,
		StartCommand:   startCmd,
		Manifest:       state.AppManifest{Env: serviceEnvForWorkloadWithAvailable(nil, w, serviceNames)},
		RequireAuthn:   plan.RequireAuthnDefault(),
		PublicAuthMode: plan.PublicAuthModeDefault(),
	}
}

// workloadClassFromScan converts the reposcan hint into the closed set that
// apps.workload_class accepts. Server/unknown/empty hints are intentionally
// conservative and fall back to HTTP; characterization can refine the class
// after the workload has booted. Keeping this normalization in one helper
// ensures create and update paths persist the same value (issue #2162).
func workloadClassFromScan(w reposcan.Workload) state.WorkloadClass {
	switch w.Class {
	case reposcan.ClassGraphQL:
		return state.WorkloadClassGraphQL
	case reposcan.ClassGRPC:
		return state.WorkloadClassGRPC
	case reposcan.ClassJob:
		return state.WorkloadClassJob
	case reposcan.ClassWorker:
		return state.WorkloadClassWorker
	case reposcan.ClassHTTP:
		return state.WorkloadClassHTTP
	default:
		// ClassServer, ClassUnknown, empty, and any future scanner
		// value must not trip the database CHECK on the first apply.
		return state.WorkloadClassHTTP
	}
}

// namesFromActionsNotIn returns the workload_names of `actions`
// whose Workload.Name is not in `added`. Used when an inner
// CreateAppIfUnderQuota call returned *QuotaError partway through
// the create loop: `added` already contains the apps that DID
// land, and the skipped set must reflect only what is still
// pending a quota lift (rather than the full create set, which
// would mislabel the partial successes as skipped).
//
// Stable order: the returned slice preserves the input order of
// `actions`, so dashboards see a deterministic skipped list.
func namesFromActionsNotIn(actions []Action, added []state.App) []string {
	addedNames := make(map[string]struct{}, len(added))
	for _, a := range added {
		addedNames[a.WorkloadName] = struct{}{}
	}
	out := make([]string, 0, len(actions))
	for _, a := range actions {
		if _, ok := addedNames[a.Workload.Name]; ok {
			continue
		}
		out = append(out, a.Workload.Name)
	}
	return out
}
