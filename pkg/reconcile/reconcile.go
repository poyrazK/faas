// Package reconcile drives Phase 5 repo decomposition: it observes
// a fresh reposcan.Result against an existing project and reconciles
// the project membership (apps + crons) to the scan in one Tx-safe
// pass. Three guards run BEFORE any write:
//
//  1. Never reconcile to empty — a successful scan that finds no
//     workloads means the customer removed every compose/procfile/
//     etc. file. Alert and bail.
//  2. Production branch only — pushes to a feature branch must NOT
//     change membership. Return Result.WasIgnored=true so the
//     caller can return 200-ignored.
//  3. Scan-source stability — a downgrade (compose → single) is
//     rejected via state.SetProjectScanSource + ErrScanSourceDowngrade.
//
// Each create/update/remove emits a project.workload.* audit row.
// The diff key is reposcan.Workload.Key() = (RootDir, Name). The
// package ships dormant on main: cmd/apid's scan_service.go picks
// it up in PR-G, cmd/githubd in PR-H.
package reconcile

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// Service is the reconcile orchestrator. Construct one per apid or
// githubd process; the struct is safe to call from multiple goroutines
// only when Store, Audit, and Limits are themselves goroutine-safe
// (true for *state.PgStore and *audit.Auditor in production).
type Service struct {
	// Store is the state-layer facade. PgStore / MemStore both
	// implement the interface.
	Store state.Store
	// Audit is the canonical pkg/audit.Auditor — emits best-effort
	// audit rows via pkg/audit/audit.go:110-126 (no error return).
	// Constructed once per daemon with audit.New(store, log, ops,
	// "reconcile"); the actor is "reconcile" so dashboards can
	// distinguish reconcile-driven events from apid/githubd
	// handler-driven events.
	Audit *audit.Auditor
	// Limits resolves the per-plan quota table. Defaults to
	// api.MustLimitsFor in NewService; tests inject a fake.
	Limits func(plan api.Plan) api.Limits
	// Scan is the reposcan entry point. Defaults to reposcan.Scan
	// in NewService; tests inject a fake that returns a fixed
	// Result.
	Scan func(fsys fs.FS) (reposcan.Result, error)
	// Log receives diagnostic output. Optional; defaults to
	// slog.Default().
	Log *slog.Logger
}

// Result carries the outcome of a single Reconcile call. Members
// are zero unless populated by their corresponding action; callers
// should range over the slices and switch on the boolean flags.
type Result struct {
	// Added carries the new app rows created during this reconcile.
	Added []state.App
	// Removed carries the IDs of apps that were soft-deleted (status
	// flipped to AppDeleted per PR-E user decision).
	Removed []string
	// Changed carries the updated rows. The store returns the
	// post-update App from UpdateApp; diff context is in the
	// corresponding project.workload.changed audit row.
	Changed []state.App
	// BuildIDs is always nil in this mega-PR. PR-H fills it via
	// the path-filtered fan-out (push commit → which workload
	// changed → which build to enqueue).
	BuildIDs []string
	// Alerts carries guard-tripped notifications. Each Alert has a
	// stable Kind string ("no_workloads" | "scan_source_downgrade" |
	// "quota_blocked" | "feature_branch") so dashboards can group.
	Alerts []Alert
	// WasIgnored is true when guard #2 (production-branch-only)
	// tripped. The caller returns 200-ignored to the webhook.
	WasIgnored bool
}

// Alert is one entry in Result.Alerts. Kind is the stable enum;
// Message is a human-readable summary; Data is the structured
// payload that was also written to the audit row.
type Alert struct {
	Kind    string
	Message string
	Data    map[string]any
}

// ErrPlanEmpty is the sentinel Plan returns when guard 1
// (never-empty) trips on the dry-run path. Daemons match via
// errors.Is to distinguish "scan found zero workloads" from
// other failures and emit a stable 422 problem. ErrIgnored and
// state.ErrScanSourceDowngrade are the Plan-side sentinels for
// guards 2 and 3.
var ErrPlanEmpty = errors.New("reconcile: plan scan returned zero workloads")

// NewService builds a Service with the default Scan and Limits
// resolvers. Callers typically set Store + Audit explicitly; the
// rest is wired by the constructor.
func NewService(store state.Store, aud *audit.Auditor, log *slog.Logger) *Service {
	if log == nil {
		log = slog.Default()
	}
	return &Service{
		Store:  store,
		Audit:  aud,
		Limits: api.MustLimitsFor,
		Scan:   reposcan.Scan,
		Log:    log,
	}
}

// Reconcile is the single entry point. It is the package's only
// exported method. See package doc for the guard order and the
// audit/kind taxonomy.
//
// commitSHA + branch are part of the audit payload and the
// production-branch guard respectively. Branch is empty for
// interactive apid scan-and-apply (where the production-branch
// invariant is enforced upstream by the apid handler — every
// apid-driven reconcile hits the production branch by
// construction). githubd passes the actual pushed branch in
// PR-H.
func (s *Service) Reconcile(
	ctx context.Context,
	project state.Project,
	scan reposcan.Result,
	commitSHA string,
	branch string,
	exclude []string,
) (Result, error) {
	// Implementation lives in the reconcile_internal.go file in
	// the same package. Splitting it keeps this file readable as
	// the package's public-surface contract.
	return s.reconcile(ctx, project, scan, commitSHA, branch, exclude)
}

// Plan runs the same three guards + diff as Reconcile but never
// persists and never emits audit rows. Used by apid's dry-run
// endpoint (POST /v1/projects/scan) to project the would-be
// Added/Changed/Removed/Alerts without mutation. The caller can
// render Result.Added/Changed/Removed to the client so the user
// sees exactly what apply would do, and bail before persisting
// if a guard tripped.
//
// branch=="" skips the production-branch guard (apid enforces
// prod-branch upstream of Plan in the dry-run flow). commitSHA
// is informational only on the Plan path — there is no audit
// row to populate, and the store is only read (AppsForProject
// to compute the diff) — never written.
//
// Returned errors:
//
//   - ErrPlanEmpty when guard 1 (never-empty) trips.
//   - state.ErrScanSourceDowngrade when guard 3 trips.
//   - other errors from Store.AppsForProject bubble verbatim.
//
// The Result is always returned (even when err is non-nil) when
// the err came from a guard — the guard's Alerts populate
// Result.Alerts so the dry-run response can include the
// reason. The ErrIgnored / ErrPlanEmpty / ErrScanSourceDowngrade
// sentinels give the caller a typed switch.
func (s *Service) Plan(
	ctx context.Context,
	project state.Project,
	scan reposcan.Result,
	commitSHA string,
	branch string,
	exclude []string,
) (Result, error) {
	if s.Scan == nil {
		// Mirror NewService's panic-on-nil so test stubs that
		// forget to wire Scan fail loudly rather than segfault on
		// the first call.
		panic("reconcile: Service.Scan is nil")
	}
	if s.Log == nil {
		s.Log = slog.Default()
	}

	// 1. Guards. Same set as Reconcile but the production-branch
	// guard is bypassed when branch=="" so the dry-run endpoint
	// can render projections for any branch without rejecting.
	outcome := s.runGuardsForPlan(ctx, project, scan, commitSHA, branch)
	if outcome.err != nil {
		// Surface the alert to the caller so the dry-run response
		// can include the guard reason (matches Reconcile's
		// Aligned: when the guard trips, the Result carries the
		// Alert regardless of whether err is non-nil).
		return Result{Alerts: outcome.alerts}, outcome.err
	}
	if outcome.ignored {
		return Result{WasIgnored: true, Alerts: outcome.alerts}, nil
	}
	accountApps, err := s.Store.ListApps(ctx, project.AccountID)
	if err != nil {
		return Result{}, fmt.Errorf("reconcile: plan: validate workload admission: %w", err)
	}
	if err := validateWorkloadAdmissionWithManaged(scan.Workloads, scan.Managed, accountApps, project.ID); err != nil {
		return Result{}, err
	}

	// 2. Read existing membership (read-only on the Plan path).
	existing, err := s.Store.AppsForProject(ctx, project.AccountID, project.ID)
	if err != nil {
		return Result{}, fmt.Errorf("reconcile: plan: load apps: %w", err)
	}
	// 2b. Resolve the account plan so the projected draft apps can
	// stamp the per-plan auth defaults (issue #695 / ADR-080).
	// Reconcile applies the same stamp on the apply path; the Plan
	// path is read-only but its projections feed the same handler
	// surface, so the projected Auth values must match what the
	// apply path would actually write.
	acct, err := s.Store.AccountByID(ctx, project.AccountID)
	if err != nil {
		return Result{}, fmt.Errorf("reconcile: plan: load account: %w", err)
	}

	// 3. Diff. workloadDiff is read-only; it does not touch the
	// store and does not emit audit. The exclude set is built
	// here (lowercased for case-insensitive matching against
	// existing WorkloadName) so the dry-run projection honors
	// the operator's --exclude intent — without it, a dry-run
	// Plan would still report removes for excluded apps.
	excludeSet := make(map[string]bool, len(exclude))
	for _, name := range exclude {
		excludeSet[strings.ToLower(strings.TrimSpace(name))] = true
	}
	actions := workloadDiff(scan, project, existing, excludeSet)

	// 4. Project the would-be Result without applying. The shape
	// mirrors applyActions' return: Added = would-be creates,
	// Changed = would-be updates, Removed = would-be remove IDs,
	// BuildIDs = nil (Plan does not enqueue builds).
	var out Result
	for _, a := range actions {
		switch a.Op {
		case "create":
			draft := workloadToDraftApp(project, a.Workload, a.StartCommand, acct.Plan, workloadNameSet(scan.Workloads))
			out.Added = append(out.Added, draft)
		case "update":
			out.Changed = append(out.Changed, a.App)
		case "remove":
			out.Removed = append(out.Removed, a.App.ID)
		}
	}
	return out, nil
}

// guardOutcome is the internal shape returned by runGuards /
// runGuardsForPlan. Defined here (next to Plan) so the Plan path
// can share guard plumbing without exporting the production-only
// fields.
type planGuardOutcome struct {
	err     error
	ignored bool
	alerts  []Alert
}

// runGuardsForPlan is the Plan-side sibling of runGuards. It
// honors guards 1 (never-empty) and 3 (scan-source stability)
// verbatim; guard 2 (production-branch-only) is gated on branch
// being non-empty so the dry-run can render projections for any
// branch.
func (s *Service) runGuardsForPlan(
	_ context.Context,
	project state.Project,
	scan reposcan.Result,
	_ string,
	branch string,
) planGuardOutcome {
	if len(scan.Workloads) == 0 || scan.Tier == 0 {
		return planGuardOutcome{
			err: fmt.Errorf("%w", ErrPlanEmpty),
			alerts: []Alert{{
				Kind:    AlertKindNoWorkloads,
				Message: "scan returned zero workloads",
			}},
		}
	}
	if branch != "" && branch != project.ProductionBranch {
		return planGuardOutcome{
			ignored: true,
			alerts: []Alert{{
				Kind:    AlertKindFeatureBranch,
				Message: "non-production branch; reconcile is a no-op",
				Data: map[string]any{
					"branch":            branch,
					"production_branch": project.ProductionBranch,
				},
			}},
		}
	}
	desired := DeriveScanSource(scan.Workloads)
	if tierRank(desired) < tierRank(project.ScanSource) {
		return planGuardOutcome{
			err: errors.Join(state.ErrScanSourceDowngrade,
				fmt.Errorf("%s→%s", project.ScanSource, desired)),
			alerts: []Alert{{
				Kind:    AlertKindScanSourceDowngrade,
				Message: "scan tier dropped below stored tier",
				Data: map[string]any{
					"current": string(project.ScanSource),
					"desired": string(desired),
				},
			}},
		}
	}
	return planGuardOutcome{}
}

// (no remaining types in this file)
