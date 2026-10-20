// adr: 20261020085706 — release approvals.
package apidserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/appaccess"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	authmw "github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) releaseApprovalStore(w http.ResponseWriter) (state.ReleaseApprovalStore, bool) {
	store, ok := s.store.(state.ReleaseApprovalStore)
	if !ok {
		api.WriteProblem(w, api.ErrCapacity("release approvals are unavailable"))
	}
	return store, ok
}

// getReleaseApprovalPolicy serves GET /v1/apps/{slug}/release-approval-policy.
func (s *server) getReleaseApprovalPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseApprovalStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	p, err := store.ReleaseApprovalPolicy(r.Context(), app.ID)
	if errors.Is(err, state.ErrNotFound) {
		api.WriteProblem(w, api.ErrReleaseApprovalPolicyNotFound(app.Slug))
		return
	}
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read release approval policy",
			"Gregale could not read this app's approval policy.", "Retry in a moment.", err)
		return
	}
	writeJSON(w, http.StatusOK, approval.PolicyView(p, app.Slug))
}

// putReleaseApprovalPolicy serves PUT /v1/apps/{slug}/release-approval-policy.
func (s *server) putReleaseApprovalPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseApprovalStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	var req api.SetReleaseApprovalPolicyRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	req, err := req.Normalize()
	if err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	if p := s.releaseApprovalPolicyEligible(r.Context(), acct, app, &req); p != nil {
		api.WriteProblem(w, p)
		return
	}
	saved, err := store.SetReleaseApprovalPolicy(r.Context(), state.ReleaseApprovalPolicy{
		AppID: app.ID, AccountID: app.AccountID, RequiredApprovals: req.RequiredApprovals, AfterStage: req.AfterStage,
		TimeoutMinutes: req.TimeoutMinutes, Approvers: req.Approvers, AllowSelfApproval: req.AllowSelfApproval,
		UpdatedBy: releaseApprovalActorLabel(r, acct),
	}, time.Now())
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "save release approval policy",
			"Gregale could not save this app's approval policy.", "Retry in a moment.", err)
		return
	}
	view := approval.PolicyView(saved, app.Slug)
	s.audit.Emit(r.Context(), "release.approval_policy_set", &acct.ID, appaccess.AuditData(r.Context(), map[string]any{
		"app": app.Slug, "required_approvals": view.RequiredApprovals, "after_stage": view.AfterStage,
		"timeout_minutes": view.TimeoutMinutes, "approvers": view.Approvers, "allow_self_approval": view.AllowSelfApproval,
		"actor": view.UpdatedBy,
	}))
	writeJSON(w, http.StatusOK, view)
}

// releaseApprovalPolicyEligible refuses a policy the app could never satisfy
// and forces self-approval for an app only one person can act on.
func (s *server) releaseApprovalPolicyEligible(ctx context.Context, acct state.Account, app state.App, req *api.SetReleaseApprovalPolicyRequest) *api.Problem {
	if !acct.Plan.TrafficSplitAllowed() {
		return api.ErrPlanTrafficSplitNotAllowed(acct.Plan)
	}
	if !approval.RequestMode(app) {
		return api.ErrValidation("release approvals wait in a canary stage; services, workers and jobs have none")
	}
	if app.Manifest.ReleasePolicy == api.ReleasePolicyImmediate {
		return api.NewProblem(http.StatusConflict, api.CodeConflict, "Release policy is immediate",
			fmt.Sprintf("%s releases without a canary, so a release has no stage to wait in for approval.", app.Slug)).
			WithHint("Set the release policy to safe or fast first: `gregale app " + app.Slug + " --release-policy safe`.")
	}
	shared, err := s.appSharedInOrg(ctx, app)
	if err != nil {
		return api.ErrInternal("Gregale could not read this app's organization.").WithHint("Retry in a moment.")
	}
	if !shared {
		req.AllowSelfApproval = true
	}
	return nil
}

// deleteReleaseApprovalPolicy serves DELETE /v1/apps/{slug}/release-approval-policy.
func (s *server) deleteReleaseApprovalPolicy(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseApprovalStore(w)
	if !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	if _, err := store.DeleteReleaseApprovalPolicy(r.Context(), app.ID); err != nil {
		if errors.Is(err, state.ErrNotFound) {
			api.WriteProblem(w, api.ErrReleaseApprovalPolicyNotFound(app.Slug))
			return
		}
		writeCustomerInternalProblem(w, r, s.log, "remove release approval policy",
			"Gregale could not remove this app's approval policy.", "Retry in a moment.", err)
		return
	}
	s.audit.Emit(r.Context(), "release.approval_policy_removed", &acct.ID, appaccess.AuditData(r.Context(), map[string]any{
		"app": app.Slug, "actor": releaseApprovalActorLabel(r, acct),
	}))
	w.WriteHeader(http.StatusNoContent)
}

// listReleaseApprovals serves GET /v1/apps/{slug}/release-approvals: the
// app's releases that wait for approval.
func (s *server) listReleaseApprovals(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if _, ok := s.releaseApprovalStore(w); !ok {
		return
	}
	app, ok := s.loadApp(w, r, acct, r.PathValue("slug"))
	if !ok {
		return
	}
	rows, err := s.store.LiveDeployments(r.Context(), app.ID)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "list release approvals",
			"Gregale could not list this app's releases.", "Retry in a moment.", err)
		return
	}
	out := api.ListReleaseApprovalsResponse{Items: []api.ReleaseApprovalStatus{}}
	for _, d := range rows {
		_, status, err := s.releaseApprovalState(r.Context(), app, d)
		if err != nil {
			writeCustomerInternalProblem(w, r, s.log, "list release approvals",
				"Gregale could not read this app's approvals.", "Retry in a moment.", err)
			return
		}
		if status.State == api.ReleaseApprovalStateWaiting || status.State == api.ReleaseApprovalStateExpired {
			out.Items = append(out.Items, status)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// getReleaseApproval serves GET /v1/deployments/{id}/approvals.
func (s *server) getReleaseApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	_, status, err := s.releaseApprovalState(r.Context(), app, d)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read release approval",
			"Gregale could not read this release's approvals.", "Retry in a moment.", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// decideReleaseApproval serves POST /v1/deployments/{id}/approvals.
func (s *server) decideReleaseApproval(w http.ResponseWriter, r *http.Request, acct state.Account) {
	store, ok := s.releaseApprovalStore(w)
	if !ok {
		return
	}
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	var req api.DecideReleaseApprovalRequest
	if err := decodeJSON(r, &req); err != nil {
		api.WriteProblem(w, api.NewProblem(http.StatusBadRequest, api.CodeValidation, "Bad request", err.Error()))
		return
	}
	if err := req.Validate(); err != nil {
		api.WriteProblem(w, api.ErrValidation(err.Error()))
		return
	}
	decision, problem := s.releaseApprovalDecision(r, acct, app, d, req)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	if _, err := store.RecordReleaseApproval(r.Context(), decision, time.Now()); err != nil {
		if errors.Is(err, state.ErrConflict) {
			api.WriteProblem(w, api.ErrReleaseApprovalDecided("You already decided on this release."))
			return
		}
		writeCustomerInternalProblem(w, r, s.log, "record release approval",
			"Gregale could not record your decision.", "Retry in a moment.", err)
		return
	}
	s.afterReleaseApprovalDecision(r, acct, app, d, decision)
	_, status, err := s.releaseApprovalState(r.Context(), app, d)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read release approval",
			"Your decision was recorded, but Gregale could not read the release's approvals.", "Retry the read in a moment.", err)
		return
	}
	writeJSON(w, http.StatusOK, status)
}

// releaseApprovalDecision checks that the caller may decide on the release
// now and builds the decision to record.
func (s *server) releaseApprovalDecision(r *http.Request, acct state.Account, app state.App, d state.Deployment, req api.DecideReleaseApprovalRequest) (state.ReleaseApproval, *api.Problem) {
	policy, status, err := s.releaseApprovalState(r.Context(), app, d)
	if err != nil {
		return state.ReleaseApproval{}, api.ErrInternal("Gregale could not read this release's approvals.").WithHint("Retry in a moment.")
	}
	switch status.State {
	case api.ReleaseApprovalStateNotRequired:
		return state.ReleaseApproval{}, api.ErrReleaseApprovalDecided("This release does not need approval.")
	case api.ReleaseApprovalStateRejected:
		return state.ReleaseApproval{}, api.ErrReleaseApprovalDecided("This release was already rejected.")
	case api.ReleaseApprovalStateExpired:
		return state.ReleaseApproval{}, api.ErrReleaseApprovalDecided("This release waited past its approval timeout.")
	}
	actor := actorAccount(r, acct)
	role, problem := s.releaseApproverRole(r, app, *policy)
	if problem != nil {
		return state.ReleaseApproval{}, problem
	}
	if req.Decision == api.ReleaseApprovalDecisionApprove && !policy.AllowSelfApproval && approval.SelfApproval(d, actor.ID) {
		return state.ReleaseApproval{}, api.ErrReleaseApprovalSelf()
	}
	if _, err := uuid.Parse(actor.ID); err != nil {
		return state.ReleaseApproval{}, api.ErrInternal("authenticated approval identity is unavailable")
	}
	return state.ReleaseApproval{
		AccountID: app.AccountID, AppID: app.ID, DeploymentID: d.ID, ApproverAccountID: actor.ID,
		Approver: releaseApprovalActorLabel(r, acct), ApproverRole: role, Decision: req.Decision, Comment: req.Comment,
	}, nil
}

// releaseApproverRole is the caller's role on the app, refused when the
// policy does not let that role decide.
func (s *server) releaseApproverRole(r *http.Request, app state.App, policy state.ReleaseApprovalPolicy) (string, *api.Problem) {
	shared, err := s.appSharedInOrg(r.Context(), app)
	if err != nil {
		return "", api.ErrInternal("Gregale could not read this app's organization.").WithHint("Retry in a moment.")
	}
	if !shared {
		return state.ReleaseApproverRolePersonal, nil
	}
	_, _, membership, _ := authmw.PrincipalFrom(r)
	if membership == nil || !approval.RoleAllowed(policy.Approvers, membership.Role) {
		return "", api.ErrOrgRoleForbidden("decide on releases of this app")
	}
	return string(membership.Role), nil
}

// afterReleaseApprovalDecision audits a decision and aborts a rejected
// release that is still rolling out.
func (s *server) afterReleaseApprovalDecision(r *http.Request, acct state.Account, app state.App, d state.Deployment, decision state.ReleaseApproval) {
	kind := "release.approved"
	if decision.Decision == state.ReleaseApprovalReject {
		kind = "release.rejected"
	}
	s.audit.Emit(r.Context(), kind, &acct.ID, appaccess.AuditData(r.Context(), map[string]any{
		"app": app.Slug, "deployment": d.ID, "approver": decision.Approver, "role": decision.ApproverRole,
		"comment": decision.Comment,
	}))
	if decision.Decision == state.ReleaseApprovalReject {
		s.abortForApproval(r.Context(), app, d, api.RolloutRuleAbortApprovalReject, "rejected by "+decision.Approver)
	}
}

// releaseApprovalState loads d's approval status. A store without approvals
// or an app without a policy needs none.
func (s *server) releaseApprovalState(ctx context.Context, app state.App, d state.Deployment) (*state.ReleaseApprovalPolicy, api.ReleaseApprovalStatus, error) {
	now := time.Now()
	store, ok := s.store.(state.ReleaseApprovalStore)
	if !ok {
		return nil, approval.Status(nil, app, d, nil, now), nil
	}
	policy, err := store.ReleaseApprovalPolicy(ctx, app.ID)
	if errors.Is(err, state.ErrNotFound) {
		return nil, approval.Status(nil, app, d, nil, now), nil
	}
	if err != nil {
		return nil, api.ReleaseApprovalStatus{}, fmt.Errorf("read release approval policy: %w", err)
	}
	var decisions []state.ReleaseApproval
	if approval.Gated(&policy, app, d) {
		if decisions, err = store.ListReleaseApprovals(ctx, d.ID); err != nil {
			return nil, api.ReleaseApprovalStatus{}, fmt.Errorf("list release approvals: %w", err)
		}
	}
	return &policy, approval.Status(&policy, app, d, decisions, now), nil
}

// releaseApprovalTrafficProblem refuses more traffic for a release that
// waits for approval or was rejected (ADR-20261020085706).
func (s *server) releaseApprovalTrafficProblem(r *http.Request, app state.App, d state.Deployment) *api.Problem {
	_, status, err := s.releaseApprovalState(r.Context(), app, d)
	if err != nil {
		logCustomerFailure(s.log, "read release approvals", err)
		return api.ErrInternal("Gregale could not read this release's approvals.").WithHint("Retry in a moment.")
	}
	if approval.Blocks(status) {
		return approval.Problem(status)
	}
	return nil
}

// recoverRolloutApprovalProblem refuses the advance and promote recoveries
// of an in-flight release that waits for approval.
func (s *server) recoverRolloutApprovalProblem(r *http.Request, app state.App, action string) *api.Problem {
	if action != "advance" && action != "promote" {
		return nil
	}
	rows, err := s.store.LiveDeployments(r.Context(), app.ID)
	if err != nil {
		logCustomerFailure(s.log, "read rollout for approvals", err)
		return api.ErrInternal("Gregale could not read this app's rollout.").WithHint("Retry in a moment.")
	}
	for _, d := range rows {
		if rollout := state.NormalizeRolloutState(d.RolloutState); rollout != "pending" && rollout != "rolling_out" {
			continue
		}
		policy, status, err := s.releaseApprovalState(r.Context(), app, d)
		if err != nil {
			logCustomerFailure(s.log, "read release approvals", err)
			return api.ErrInternal("Gregale could not read this release's approvals.").WithHint("Retry in a moment.")
		}
		if action == "promote" && approval.Blocks(status) || action == "advance" && approval.AdvanceBlocked(policy, d, d.CanaryStep, status) {
			return approval.Problem(status)
		}
	}
	return nil
}

// releaseApprovalAdvanceProblem refuses a canary step past the waiting stage
// until the release is approved. On the worker's request it records why the
// release waits, or aborts it once it waited past the timeout.
func (s *server) releaseApprovalAdvanceProblem(r *http.Request, app state.App, d state.Deployment, expectedStep int, worker bool) *api.Problem {
	policy, status, err := s.releaseApprovalState(r.Context(), app, d)
	if err != nil {
		logCustomerFailure(s.log, "read release approvals", err)
		return api.ErrInternal("Gregale could not read this release's approvals.").WithHint("Retry in a moment.")
	}
	if !approval.AdvanceBlocked(policy, d, expectedStep, status) {
		return nil
	}
	if worker {
		s.recordApprovalWait(r.Context(), app, d, status)
	}
	return approval.Problem(status)
}

// recordApprovalWait records a hold_awaiting_approval decision, or aborts a
// release that was rejected or waited past its timeout.
func (s *server) recordApprovalWait(ctx context.Context, app state.App, d state.Deployment, status api.ReleaseApprovalStatus) {
	switch status.State {
	case api.ReleaseApprovalStateExpired:
		s.abortForApproval(ctx, app, d, api.RolloutRuleAbortApprovalTimeout, "waited past its approval timeout")
		return
	case api.ReleaseApprovalStateRejected:
		s.abortForApproval(ctx, app, d, api.RolloutRuleAbortApprovalReject, "rejected")
		return
	}
	holds, ok := s.store.(state.RolloutDecisionStore)
	if !ok {
		return
	}
	var accountID *uuid.UUID
	if parsed, err := uuid.Parse(app.AccountID); err == nil {
		accountID = &parsed
	}
	now := time.Now().UTC()
	if _, _, err := holds.RecordRolloutHold(ctx, state.RolloutHoldParams{
		DeploymentID: d.ID, AccountID: accountID, ExpectedStep: d.CanaryStep, Rule: api.RolloutRuleHoldAwaitingApproval,
		Decision: approval.HoldDecision(status, d, now), Actor: canaryProgressionActor, At: now,
	}); err != nil {
		s.log.Warn("apid: record approval hold failed", "deployment", d.ID, "err", err)
	}
}

// abortForApproval aborts a rejected or timed-out release that is still
// rolling out, restoring its predecessor through the manual-abort path.
func (s *server) abortForApproval(ctx context.Context, app state.App, d state.Deployment, rule, summary string) {
	rollout := state.NormalizeRolloutState(d.RolloutState)
	if rollout != "pending" && rollout != "rolling_out" {
		return
	}
	decisions, ok := s.store.(state.RolloutDecisionStore)
	if !ok {
		return
	}
	reason := "release approval: " + summary
	if _, _, err := decisions.RecoverRolloutWithDecision(ctx, app.ID, "abort", reason,
		approval.AbortDecision(rule, summary, d, time.Now())); err != nil {
		s.log.Warn("apid: abort release for approval failed", "deployment", d.ID, "rule", rule, "err", err)
	}
}

// appSharedInOrg reports whether more than one person can act on app.
func (s *server) appSharedInOrg(ctx context.Context, app state.App) (bool, error) {
	if app.OrgID == "" {
		return false, nil
	}
	org, err := s.store.OrgByID(ctx, app.OrgID)
	if err != nil {
		return false, err
	}
	return !org.Personal, nil
}

// releaseApprovalActorLabel names the person or key behind a request.
func releaseApprovalActorLabel(r *http.Request, acct state.Account) string {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.ID != "" {
		return "api_key:" + p.Key.ID
	}
	actor := actorAccount(r, acct)
	if actor.Email != "" {
		return actor.Email
	}
	return "account:" + actor.ID
}

// releaseApprovalCanaryProblem refuses a production release that would take
// traffic without a canary while the app requires approvals.
func (s *server) releaseApprovalCanaryProblem(ctx context.Context, app state.App, req *api.CreateDeploymentRequest) *api.Problem {
	if !approval.TakesTrafficWithoutCanary(req) {
		return nil
	}
	required, err := approval.CanaryRequired(ctx, s.store, app, req.Scope, req.Environment)
	if err != nil {
		logCustomerFailure(s.log, "read release approval policy", err)
		return api.ErrInternal("Gregale could not read this app's release approval policy.").WithHint("Retry in a moment.")
	}
	if required {
		return api.ErrReleaseApprovalCanaryRequired(app.Slug)
	}
	return nil
}
