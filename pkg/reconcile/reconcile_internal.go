// Internal reconcile implementation. The exported entry point
// lives in reconcile.go (Reconcile) and delegates here. Splits
// the "public surface contract" from the "implementation"
// so reviewers can read the entry point without scrolling 200
// lines of code.

package reconcile

import (
	"context"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// reconcile is the orchestrator. Order of operations:
//
//  1. Three guards (runGuards). Tripped guards set Result
//     partially (WasIgnored, Alerts) and short-circuit the
//     rest of the function. The scan-source-downgrade guard
//     errors out so the caller can 4xx.
//  2. emitReconcileStarted (one audit row for the entire pass).
//  3. Resolve the existing app set (AppsForProject). Failure
//     here is a caller-visible error.
//  4. workloadDiff → []Action.
//  5. applyActions (quota pre-check, updates, removes, creates).
//  6. Upgrade SetProjectScanSource if the scan tier trumps the
//     stored project.ScanSource AND tierRank is strictly greater
//     (same-tier is a no-op per the store).
//
// The function is the package's only public-method-shaped
// surface; everything else is package-private.
func (s *Service) reconcile(
	ctx context.Context,
	project state.Project,
	scan reposcan.Result,
	commitSHA string,
	branch string,
	exclude []string,
	crons []CronSpec,
) (Result, error) {
	var out Result

	// 1. Guards.
	outcome := s.runGuards(ctx, project, scan, commitSHA, branch)
	if outcome.err != nil {
		// scan-source-downgrade: visible to the caller as an
		// error AND recorded as an alert.
		out.Alerts = append(out.Alerts, *outcome.alert)
		return out, outcome.err
	}
	if outcome.alert != nil {
		// neverEmpty or productionBranchOnly.
		out.Alerts = append(out.Alerts, *outcome.alert)
		if outcome.alert.Kind == AlertKindFeatureBranch {
			out.WasIgnored = true
		}
		return out, nil
	}
	accountApps, err := s.Store.ListApps(ctx, project.AccountID)
	if err != nil {
		return out, fmt.Errorf("reconcile: validate workload admission: %w", err)
	}
	if err := validateWorkloadAdmissionWithManaged(scan.Workloads, scan.Managed, accountApps, project.ID); err != nil {
		return out, err
	}

	// 2. Audit "started".
	s.emitReconcileStarted(ctx, project, scan, commitSHA, branch)

	// 3. Existing membership.
	existing, err := s.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return out, err
	}

	// 4. Diff. exclude is the operator --exclude set (already
	// lowercased + trimmed at the handler boundary, but we
	// defensively re-normalize here so a direct pkg caller
	// can't smuggle mixed-case names past the filter). The
	// workloadDiff filter drops existing apps whose lowercased
	// WorkloadName is in excludeSet so the removes loop never
	// emits a remove Action for an operator-excluded app —
	// closing the code-review finding #1 contradiction where
	// `--exclude=foo` silently soft-deleted existing app foo
	// via applyActions.applyRemove → SoftDeleteAppCascade.
	excludeSet := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		excludeSet[strings.ToLower(strings.TrimSpace(name))] = true
	}
	actions := workloadDiff(scan, project, existing, excludeSet)

	// 5. Apply. applyActions may emit a quota_blocked alert and
	// return out with no error; it returns a real error only on
	// store failures (FK, conflict, etc.). Pass `existing` through
	// so the quota pre-check inside applyActions reuses the same
	// slice the diff just consumed — no second round-trip, no
	// race window between the count and the create Tx.
	applied, err := s.applyActions(ctx, project, actions, existing, commitSHA, scan, crons)
	if err != nil {
		out.Alerts = append(out.Alerts, applied.Alerts...)
		return out, err
	}
	out.Added = applied.Added
	out.Changed = applied.Changed
	out.Removed = applied.Removed
	out.Alerts = append(out.Alerts, applied.Alerts...)
	out.scanSourceApplied = applied.scanSourceApplied

	// 6. ScanSource upgrade (strictly greater; same-tier is a
	// no-op per the store). The audit row is emitted ONLY on a
	// real upgrade via emitScanSourceChanged.
	desired := DeriveScanSource(scan.Workloads)
	if !applied.scanSourceApplied && tierRank(desired) > tierRank(project.ScanSource) {
		prev := project.ScanSource
		updated, err := s.Store.SetProjectScanSource(ctx, project.ID, desired)
		if err != nil {
			// A downgrade rejection at this point is a
			// concurrent-spec violation — surface to the
			// caller. The guards already passed so this is
			// unexpected.
			return out, err
		}
		s.emitScanSourceChanged(ctx, updated, string(prev), string(desired), commitSHA)
	}

	return out, nil
}

// reposcan import guard — keep the package imported so future
// phases (PR-H path-filtered fan-out) can call helpers in
// pkg/reposcan directly from the reconcile layer.
var _ = reposcan.Result{}
