package issuetrackers

import (
	"context"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

// InboundPreview classifies a fresh observation against the last delegated
// baseline. It is explanatory evidence, never authorization for a later write.
// A future apply path must re-read the provider and atomically fence local edits.
type InboundPreview struct {
	SourceState  string
	Outcome      string
	Reason       string
	DesiredState string
}

func inboundPreview(p Prepared, r state.IssueTrackerAutoSyncRecord, observed api.IssueTrackerProviderStatus) InboundPreview {
	v := InboundPreview{SourceState: p.Issue.State, Outcome: "blocked"}
	if r.AppID != p.Issue.AppID || r.AccountID != p.Connection.AccountID || r.Policy.SourceIssueID != p.Issue.ID || r.Policy.LinkID != p.Link.ID || r.BindingFingerprint != autoSyncFingerprint(p) {
		v.Reason = "binding_changed"
		return v
	}
	if r.Policy.PendingState != "" {
		v.Reason = "outbound_pending"
		return v
	}
	if !r.Policy.Enabled {
		v.Reason = "policy_disabled"
		return v
	}
	if r.Policy.Outcome == "blocked" && r.Policy.Reason != "fixing_deployment_required" || r.Policy.Outcome == "conflict" && r.Policy.Reason != "provider_changed" {
		v.Reason = "policy_requires_review"
		return v
	}
	if !lifecycleState(p.Issue.State) || !lifecycleState(r.Policy.SourceState) || !lifecycleState(r.Baseline.State) || !lifecycleState(observed.State) {
		v.Reason = "state_unsupported"
		return v
	}
	baseline := r.Baseline
	if observed.ID == "" || observed.ID != baseline.ID || observed.Provider != baseline.Provider || observed.URL != baseline.URL || observed.TeamID != baseline.TeamID {
		v.Reason = "provider_identity_changed"
		return v
	}
	if p.Issue.State != r.Policy.SourceState {
		v.Outcome, v.Reason = "conflict", "source_changed"
		return v
	}
	if observed == baseline {
		v.Outcome, v.Reason = "unchanged", "provider_unchanged"
		return v
	}
	if observed.Provider == "linear" && observed.StateID != linearDestination(r.Policy, observed.State) {
		v.Reason = "workflow_unmapped"
		return v
	}
	if observed.State == p.Issue.State {
		v.Outcome, v.Reason = "unchanged", "lifecycle_unchanged"
		return v
	}
	v.DesiredState = observed.State
	v.Outcome, v.Reason = "candidate", "provider_reopened"
	if observed.State == "resolved" {
		v.Outcome, v.Reason = "needs_deployment", "fixing_deployment_required"
	}
	return v
}

func lifecycleState(s string) bool { return s == "open" || s == "resolved" }

// ReadInboundPreview changes neither issue lifecycle nor sync policy. Credential
// refresh may occur through Host.Token. The source lifecycle, connection and
// group must remain stable throughout the provider read.
func (s Service) ReadInboundPreview(ctx context.Context, r state.IssueTrackerAutoSyncRecord) (api.NativeIssueTrackerStatus, InboundPreview, error) {
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeIssueTrackerStatus{}, InboundPreview{}, err
	}
	snapshot, err := s.Read(ctx)
	if err != nil {
		return snapshot, InboundPreview{}, err
	}
	fresh, err := s.prepare(ctx)
	if err != nil {
		return snapshot, InboundPreview{}, err
	}
	if !same(p, fresh) {
		return snapshot, InboundPreview{}, Changed()
	}
	return snapshot, inboundPreview(p, r, snapshot.Status), nil
}

// ReadInboundPreviewWithWorkflows preserves the workflow picker's existing
// verified catalog and also assesses the inbound observation without changing
// issue lifecycle or sync policy. Native credential refresh remains available.
func (s LinearService) ReadInboundPreviewWithWorkflows(ctx context.Context, r state.IssueTrackerAutoSyncRecord) (api.NativeLinearIssueTrackerStatus, []issuetracker.LinearWorkflowState, InboundPreview, error) {
	p, err := s.prepare(ctx)
	if err != nil {
		return api.NativeLinearIssueTrackerStatus{}, nil, InboundPreview{}, err
	}
	snapshot, catalog, err := s.ReadWithWorkflows(ctx)
	if err != nil {
		return snapshot, catalog, InboundPreview{}, err
	}
	if err := s.fresh(ctx, p); err != nil {
		return snapshot, catalog, InboundPreview{}, err
	}
	return snapshot, catalog, inboundPreview(p, r, snapshot.Status), nil
}
