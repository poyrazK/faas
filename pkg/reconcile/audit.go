// Audit emission for the reconcile package. All audit kinds are
// declared as typed constants so a stray string at a call site
// fails the build (goconst on bare strings would also catch this
// but typed constants are stronger). Every Emit* helper funnels
// through s.Audit.Emit which is best-effort per ADR-035 — no
// error path, no caller-visible rollback.

package reconcile

import (
	"context"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Audit kind constants. Stable strings; dashboards and the
// audit-event allow-list (none today — kinds are plain text per
// migrations/00001_init.sql:129-136) group on these values.
const (
	KindReconcileStarted      = "project.reconcile.started"
	KindWorkloadAdded         = "project.workload.added"
	KindWorkloadChanged       = "project.workload.changed"
	KindWorkloadRemoved       = "project.workload.removed"
	KindReconcileAlert        = "project.reconcile.alert"
	KindReconcileQuotaBlocked = "project.reconcile.quota_blocked"
	KindScanSourceChanged     = "project.scan_source_changed"
	// KindBuildEnqueued is the githubd-side audit event for a
	// per-app build that's been enqueued via the apid gRPC
	// bridge (issue #432 phase 5). Emitted by the githubd
	// dispatcher AFTER the bridge returns a non-empty
	// build_id. The linked apid-side event —
	// auth.deployment.enqueued (or the existing
	// build_queued pg_notify consumer downstream) — gives
	// the operator a single-correlated paper trail of
	// push → reconcile → build.
	KindBuildEnqueued = "project.build.enqueued"
)

// Alert kind constants. Mirrors the audit kind's `reason` field —
// dashboards distinguish alerts via the alert kind, not the audit
// kind.
const (
	AlertKindNoWorkloads         = "no_workloads"
	AlertKindFeatureBranch       = "feature_branch"
	AlertKindScanSourceDowngrade = "scan_source_downgrade"
	AlertKindQuotaBlocked        = "quota_blocked"
)

// emitReconcileStarted fires project.reconcile.started. Called
// once per Reconcile call AFTER the three guards pass; if any
// guard trips first we never reach here, so dashboards see one
// started row per non-ignored reconcile.
func (s *Service) emitReconcileStarted(
	ctx context.Context,
	project state.Project,
	scan reposcan.Result,
	commitSHA string,
	branch string,
) {
	s.Audit.Emit(ctx, KindReconcileStarted, &project.AccountID, map[string]any{
		"project_id":     project.ID,
		"branch":         branch,
		"scan_source":    string(project.ScanSource),
		"scan_tier":      scan.Tier.String(),
		"commit_sha":     commitSHA,
		"workload_count": len(scan.Workloads),
	})
}

// emitWorkloadAdded fires project.workload.added per create.
func (s *Service) emitWorkloadAdded(
	ctx context.Context,
	project state.Project,
	app state.App,
	commitSHA string,
) {
	s.Audit.Emit(ctx, KindWorkloadAdded, &project.AccountID, map[string]any{
		"project_id":    project.ID,
		"app_id":        app.ID,
		"workload_name": app.WorkloadName,
		"root_dir":      app.RootDir,
		"commit_sha":    commitSHA,
	})
}

// emitWorkloadChanged fires project.workload.changed per update
// that actually moved a column. The fields_changed slice lets
// dashboards filter on what kind of update (start_command vs
// root_dir vs workload_name).
func (s *Service) emitWorkloadChanged(
	ctx context.Context,
	project state.Project,
	app state.App,
	fieldsChanged []string,
	commitSHA string,
) {
	s.Audit.Emit(ctx, KindWorkloadChanged, &project.AccountID, map[string]any{
		"project_id":     project.ID,
		"app_id":         app.ID,
		"workload_name":  app.WorkloadName,
		"fields_changed": fieldsChanged,
		"commit_sha":     commitSHA,
	})
}

// emitWorkloadRemoved fires project.workload.removed BEFORE the
// SoftDeleteAppCascade call. ADR-035 best-effort + Emit returns
// no error, so the audit row exists in the events table even when
// the cascade SQL fails halfway.
func (s *Service) emitWorkloadRemoved(
	ctx context.Context,
	project state.Project,
	appID string,
	workloadName string,
	commitSHA string,
) {
	s.Audit.Emit(ctx, KindWorkloadRemoved, &project.AccountID, map[string]any{
		"project_id":    project.ID,
		"app_id":        appID,
		"workload_name": workloadName,
		"commit_sha":    commitSHA,
	})
}

// emitReconcileAlert fires project.reconcile.alert when a guard
// trips. reason is one of the AlertKind* constants.
func (s *Service) emitReconcileAlert(
	ctx context.Context,
	project state.Project,
	reason string,
	scanSource string,
	branch string,
	commitSHA string,
	extra map[string]any,
) {
	data := map[string]any{
		"project_id":  project.ID,
		"reason":      reason,
		"scan_source": scanSource,
		"branch":      branch,
		"commit_sha":  commitSHA,
	}
	for k, v := range extra {
		data[k] = v
	}
	s.Audit.Emit(ctx, KindReconcileAlert, &project.AccountID, data)
}

// emitReconcileQuotaBlocked fires project.reconcile.quota_blocked
// when the create set was rejected. skipped_creates carries the
// workload_name of every member that would have been created.
func (s *Service) emitReconcileQuotaBlocked(
	ctx context.Context,
	project state.Project,
	limit int,
	observed int,
	wouldbeCount int,
	skippedCreates []string,
	commitSHA string,
) {
	s.Audit.Emit(ctx, KindReconcileQuotaBlocked, &project.AccountID, map[string]any{
		"project_id":      project.ID,
		"kind":            "apps",
		"limit":           limit,
		"observed":        observed,
		"wouldbe_count":   wouldbeCount,
		"skipped_creates": skippedCreates,
		"commit_sha":      commitSHA,
	})
}

// emitScanSourceChanged fires project.scan_source_changed after
// SetProjectScanSource succeeds. The store does NOT emit audit
// (store.go:563); this is the caller's responsibility per the
// mega-PR plan.
func (s *Service) emitScanSourceChanged(
	ctx context.Context,
	project state.Project,
	from string,
	to string,
	commitSHA string,
) {
	s.Audit.Emit(ctx, KindScanSourceChanged, &project.AccountID, map[string]any{
		"project_id": project.ID,
		"from":       from,
		"to":         to,
		"commit_sha": commitSHA,
	})
}

// EmitBuildEnqueued fires project.build.enqueued (issue #432
// phase 5). Called by the githubd dispatcher AFTER the apid
// bridge returns a non-empty build_id; the durable build row
// is the source of truth, so emitting on success is what
// keeps the audit paper trail consistent with the build
// pipeline.
//
// Payload mirrors the dispatcher's BuildSpec fields so the
// operator can join this row with the build row, the
// deployment row, and the githubd-side reconcile.started
// row by (project_id, app_id, commit_sha).
//
// Public (not emitBuildEnqueued) so cmd/githubd can call it
// without a circular dependency — the githubd package imports
// reconcile (for the BuildEnqueuer interface), so calling a
// lowercase helper would not work. The constant
// KindBuildEnqueued stays lowercase (internal-shape).
func (s *Service) EmitBuildEnqueued(
	ctx context.Context,
	project state.Project,
	appID string,
	buildID string,
	deploymentID string,
	commitSHA string,
	repoFullName string,
	branch string,
	sourcePath string,
) {
	s.Audit.Emit(ctx, KindBuildEnqueued, &project.AccountID, map[string]any{
		"project_id":    project.ID,
		"app_id":        appID,
		"build_id":      buildID,
		"deployment_id": deploymentID,
		"commit_sha":    commitSHA,
		"repo":          repoFullName,
		"branch":        branch,
		"source_path":   sourcePath,
	})
}
