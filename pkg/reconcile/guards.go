// Three guards that run BEFORE any write to the state layer.
// Order matters: each guard's early-exit short-circuits the rest
// so the audit emit is always traceable to the single reason the
// reconcile did not write.
//
//  1. neverEmpty — a successful scan that finds no workloads means
//     the customer has deleted every compose/procfile/etc. file.
//     We never reconcile to zero because the previous membership
//     may be intentional (envs + crons are still referenced); an
//     explicit "deprovision" is a separate call.
//  2. productionBranchOnly — pushes to a feature branch must NOT
//     mutate project membership. Result.WasIgnored=true; the
//     githubd webhook translates that to 200-ignored.
//  3. scanSourceStable — a downgrade (compose → single) is
//     rejected via store.SetProjectScanSource which returns
//     ErrScanSourceDowngrade. We surface the error to the caller
//     AND emit the alert (best-effort) so dashboards see both
//     rows.

package reconcile

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// guardOutcome is the internal envelope for the three guards. It
// carries the early-exit decision (ok / empty / ignored /
// source-downgrade) plus the structured payload to emit in the
// audit row.
type guardOutcome struct {
	// ok is true when the guard allows the reconcile to proceed.
	ok bool
	// alert is the structured payload to emit on a trip. nil when
	// ok=true.
	alert *Alert
	// err is the caller-visible error. nil for "empty" / "ignored"
	// (alert-only), non-nil only for source-downgrade (which we
	// surface to the caller so the webhook can 4xx).
	err error
}

// runGuards evaluates the three guards in order. It stops at the
// first trip and returns the corresponding outcome. The audit
// emit for the trip happens via s.emitReconcileAlert BEFORE this
// function returns, so the alert row is observable even when the
// caller short-circuits.
//
// branch is empty for the apid interactive scan-and-apply path
// (production-branch is enforced upstream by the apid handler).
// githubd passes the actual pushed branch in PR-H.
func (s *Service) runGuards(
	ctx context.Context,
	project state.Project,
	scan reposcan.Result,
	commitSHA string,
	branch string,
) guardOutcome {
	// Guard 1 — neverEmpty.
	if len(scan.Workloads) == 0 || scan.Tier == 0 {
		alert := &Alert{
			Kind:    AlertKindNoWorkloads,
			Message: "scan produced zero workloads; reconcile refused",
			Data: map[string]any{
				"scan_warnings": scan.Warnings,
			},
		}
		s.emitReconcileAlert(
			ctx, project,
			AlertKindNoWorkloads,
			string(project.ScanSource),
			branch,
			commitSHA,
			alert.Data,
		)
		return guardOutcome{alert: alert}
	}

	// Guard 2 — productionBranchOnly. branch=="" means the apid
	// interactive path; the invariant is enforced upstream so we
	// skip the guard entirely. githubd always passes a non-empty
	// branch.
	if branch != "" && branch != project.ProductionBranch {
		alert := &Alert{
			Kind:    AlertKindFeatureBranch,
			Message: "reconcile ignored: push was not on production branch",
			Data: map[string]any{
				"production_branch": project.ProductionBranch,
			},
		}
		s.emitReconcileAlert(
			ctx, project,
			AlertKindFeatureBranch,
			string(project.ScanSource),
			branch,
			commitSHA,
			alert.Data,
		)
		return guardOutcome{
			alert: alert,
			// WasIgnored flag the caller reads from Result; we
			// attach to the Alert so emitReconcileAlert's data
			// payload aligns with the same shape.
		}
	}

	// Guard 3 — scanSourceStable. DeriveScanSource picks the
	// canonical ProjectScanSource from the scan's workloads; the
	// store-side monotonic-upgrade guard rejects a downgrade.
	desired := DeriveScanSource(scan.Workloads)
	if tierRank(desired) < tierRank(project.ScanSource) {
		// Emit the alert BEFORE the store call so dashboards see
		// the rejection even when the store call races with a
		// concurrent project delete.
		alert := &Alert{
			Kind:    AlertKindScanSourceDowngrade,
			Message: "scan_source downgrade rejected",
			Data: map[string]any{
				"from": string(project.ScanSource),
				"to":   string(desired),
			},
		}
		s.emitReconcileAlert(
			ctx, project,
			AlertKindScanSourceDowngrade,
			string(project.ScanSource),
			branch,
			commitSHA,
			alert.Data,
		)
		// Surface the error to the caller so the webhook can
		// 4xx. Use the canonical sentinel so errors.Is works.
		return guardOutcome{
			alert: alert,
			err:   errors.Join(state.ErrScanSourceDowngrade, errReconcileScanSourceDowngrade(desired)),
		}
	}

	return guardOutcome{ok: true}
}

// errReconcileScanSourceDowngrade carries the attempted target
// ProjectScanSource in the error chain. Callers can extract it
// with errors.As to surface the precise attempt in the webhook
// response. Distinct from state.ErrScanSourceDowngrade so callers
// can tell the two apart (the state-layer error is the canonical
// guard; the reconcile-layer error adds the "and a reconcile tried
// it" context).
type errReconcileScanSourceDowngrade state.ProjectScanSource

func (e errReconcileScanSourceDowngrade) Error() string {
	return "reconcile: scan_source downgrade attempted to " + string(e)
}

// errReconcileIgnored is the typed error returned when the
// production-branch-guard trips. Callers (githubd) check
// errors.Is(err, reconcile.ErrIgnored) and return 200-ignored.
var ErrIgnored = errors.New("reconcile: ignored")
