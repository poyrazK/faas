package main

// reconcile_adapter.go — Phase 5 PR-G (repo decomposition):
// bridges cmd/apid's scan-and-apply flow onto pkg/reconcile.Service.
//
// PR-F (merged as part of PR #479) introduced pkg/reconcile as the
// single workload-mutation primitive. Before this commit, apid
// mutated apps rows directly via store.ApplyProjectPlan + manual
// cron AppID stamping + manual per-app NotifyAppChanged. PR-G
// routes the apid interactive path through reconcile so the diff
// /apply contract is identical regardless of caller (apid or
// githubd).
//
// The adapter is apid-side because the input shape
// (scanPlanRequest → scanPlanResponse) is apid-internal: pkg/reconcile
// stays daemon-agnostic and only takes state.Project + reposcan.Result
// + commitSHA + branch.
//
// commitSHA on the apid path is the supplied tarball SHA-256
// (SourceSHA256) because the operator has not pushed; githubd
// (PR-H) passes the actual pushed commit SHA. The reconcile package
// treats both identically.

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/onebox-faas/faas/pkg/audit"
	"github.com/onebox-faas/faas/pkg/reconcile"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildReconcileService constructs the apid-side reconcile.Service
// using the same *audit.Auditor that powers s.audit (auditActor="apid"
// per cmd/apid/audit.go:43). Reconcile rows written through this
// service therefore carry actor="apid" in events.actor — matching
// the apid audit pipeline's convention. The same store powers
// both, so reads see consistent membership.
func buildReconcileService(store state.Store, aud *audit.Auditor, log *slog.Logger) *reconcile.Service {
	return reconcile.NewService(store, aud, log)
	// Limits and Scan keep the defaults injected by NewService
	// (api.MustLimitsFor + reposcan.Scan); apid does not need a
	// custom override.
}

// reconcileInputs packages the four arguments Reconcile / Plan take.
// Centralised so the dry-run path and the apply path can't drift
// on the commitSHA / branch convention.
type reconcileInputs struct {
	Project   state.Project
	Scan      reposcan.Result
	CommitSHA string
	Branch    string
}

// toReconcileInputs translates an apid scanPlanRequest + the
// post-scan reposcan.Result into the four-argument reconcile call.
// Branch defaults to "main" on the apid path; the production-branch
// guard in reconcile is bypassed when branch=="" (Plan path) and
// enforced when branch != "" (Reconcile path). The apply handler
// (applyProject) populates req.ProdBranch verbatim from the
// multipart field, defaulting to "main" in parseScanMultipart.
func toReconcileInputs(req scanPlanRequest, project state.Project, scan reposcan.Result) reconcileInputs {
	return reconcileInputs{
		Project:   project,
		Scan:      scan,
		CommitSHA: req.SourceSHA256,
		Branch:    req.ProdBranch,
	}
}

// mapReconcileError translates the reconcile-package errors into
// the existing apid-side *api.Problem shapes. Called from
// applyProject + scanProject when reconcile returns non-nil.
//
//   - reconcile.ErrPlanEmpty (sentinel) → 422 unprocessable
//     (empty scan).
//   - state.ErrScanSourceDowngrade → 409 conflict (re-scan
//     required).
//   - reconcile.ErrIgnored → 200 ignored (only githubd exercises
//     this in production; apid's prod-branch check upstream should
//     keep it unreachable on the apply path).
//   - *state.QuotaError → 422 quota (the handler routes via
//     quotaProblem).
//   - default → 500 internal.
//
// mapReconcileError returns nil when err is nil so callers can use
// it as a guard.
func mapReconcileError(err error) *reconcileErrMapping {
	if err == nil {
		return nil
	}
	if errors.Is(err, reconcile.ErrPlanEmpty) {
		return &reconcileErrMapping{Status: 422, Code: "plan_empty", Msg: err.Error()}
	}
	if errors.Is(err, state.ErrScanSourceDowngrade) {
		return &reconcileErrMapping{Status: 409, Code: "scan_source_downgrade", Msg: err.Error()}
	}
	if errors.Is(err, state.ErrConflict) {
		return &reconcileErrMapping{Status: 409, Code: "workload_slug_conflict", Msg: err.Error()}
	}
	if errors.Is(err, reconcile.ErrIgnored) {
		return &reconcileErrMapping{Status: 200, Code: "ignored", Msg: "feature branch; reconcile is a no-op"}
	}
	var qe *state.QuotaError
	if errors.As(err, &qe) {
		return &reconcileErrMapping{Status: 422, Code: "quota_exceeded", Msg: qe.Error(), Quota: qe}
	}
	return &reconcileErrMapping{Status: 500, Code: "internal", Msg: fmt.Sprintf("reconcile: %v", err)}
}

// reconcileErrMapping is the internal shape returned by
// mapReconcileError. The handler converts it into the RFC 7807
// api.Problem via quotaProblem for the Quota branch and the
// generic ErrInternal / NewProblem constructors elsewhere.
type reconcileErrMapping struct {
	Status int
	Code   string
	Msg    string
	Quota  *state.QuotaError
}

// (intentionally empty: the dry-run + apply paths call
// svc.Plan / svc.Reconcile directly from scan_service.go now that
// PR-G wired the reconcile seam. No further adapters needed.)
