package issuetrackers

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestInboundPreviewProtectsBaselineAndOutboundIntent(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, reason string
		edit                  func(*Prepared, *state.IssueTrackerAutoSyncRecord, *api.IssueTrackerProviderStatus)
	}{
		{"unchanged", "unchanged", "provider_unchanged", func(_ *Prepared, _ *state.IssueTrackerAutoSyncRecord, o *api.IssueTrackerProviderStatus) {
			o.State = "open"
		}},
		{"resolution", "needs_deployment", "fixing_deployment_required", func(*Prepared, *state.IssueTrackerAutoSyncRecord, *api.IssueTrackerProviderStatus) {}},
		{"pending even when provider agrees", "blocked", "outbound_pending", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.PendingState = "resolved"
		}},
		{"both sides changed", "conflict", "source_changed", func(p *Prepared, _ *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			p.Issue.State = "resolved"
		}},
		{"local only", "conflict", "source_changed", func(p *Prepared, _ *state.IssueTrackerAutoSyncRecord, o *api.IssueTrackerProviderStatus) {
			p.Issue.State = "resolved"
			o.State = "open"
		}},
		{"ignored", "blocked", "state_unsupported", func(p *Prepared, _ *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			p.Issue.State = "ignored"
		}},
		{"provider unsupported", "blocked", "state_unsupported", func(_ *Prepared, _ *state.IssueTrackerAutoSyncRecord, o *api.IssueTrackerProviderStatus) {
			o.State = "canceled"
		}},
		{"disabled", "blocked", "policy_disabled", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.Enabled = false
		}},
		{"group changed", "blocked", "binding_changed", func(p *Prepared, _ *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			p.GroupFingerprint += "changed"
		}},
		{"owner changed", "blocked", "binding_changed", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.AccountID = "other"
		}},
		{"source group changed", "blocked", "binding_changed", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.SourceIssueID = "other"
		}},
		{"reference changed", "blocked", "binding_changed", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.LinkID = "other"
		}},
		{"ticket changed", "blocked", "provider_identity_changed", func(_ *Prepared, _ *state.IssueTrackerAutoSyncRecord, o *api.IssueTrackerProviderStatus) { o.ID = "43" }},
		{"provider conflict can be assessed", "needs_deployment", "fixing_deployment_required", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.Outcome = "conflict"
			r.Policy.Reason = "provider_changed"
		}},
		{"other conflict remains paused", "blocked", "policy_requires_review", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.Outcome = "conflict"
			r.Policy.Reason = "source_changed_after_dispatch"
		}},
		{"access block remains paused", "blocked", "policy_requires_review", func(_ *Prepared, r *state.IssueTrackerAutoSyncRecord, _ *api.IssueTrackerProviderStatus) {
			r.Policy.Outcome = "blocked"
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, host, store, _ := autoFixture()
			p, r, observed := host.p, store.r, store.r.Baseline
			observed.State = "resolved"
			tc.edit(&p, &r, &observed)
			out := inboundPreview(p, r, observed)
			if out.Outcome != tc.outcome || out.Reason != tc.reason {
				t.Fatalf("%+v", out)
			}
			if out.Outcome != "candidate" && out.Outcome != "needs_deployment" && out.DesiredState != "" {
				t.Fatalf("blocked preview has desired state: %+v", out)
			}
		})
	}
}

func TestInboundPreviewReopenAndReadAreReadOnly(t *testing.T) {
	s, host, store, provider := autoFixture()
	host.p.Issue.State = "resolved"
	store.r.Policy.SourceState = "resolved"
	store.r.Baseline.State = "resolved"
	before := store.r
	snapshot, out, err := s.ReadInboundPreview(t.Context(), store.r)
	if err != nil || out.Outcome != "candidate" || out.DesiredState != "open" || snapshot.Status.State != "open" {
		t.Fatal(snapshot, out, err)
	}
	if provider.writes != 0 || provider.reads != 1 || store.pending != 0 || store.r != before {
		t.Fatal("preview mutated provider or policy")
	}
}

func TestInboundPreviewLinearMapping(t *testing.T) {
	_, host, store, _ := linearAutoFixture()
	p, r := host.p, store.r
	observed := r.Baseline
	observed.State, observed.StateID = "resolved", r.Policy.LinearResolvedStateID
	if out := inboundPreview(p, r, observed); out.Outcome != "needs_deployment" {
		t.Fatal(out)
	}
	observed.StateID = "other-done"
	if out := inboundPreview(p, r, observed); out.Reason != "workflow_unmapped" {
		t.Fatal(out)
	}
	observed.State, observed.StateID = "open", "other-open"
	if out := inboundPreview(p, r, observed); out.Reason != "workflow_unmapped" {
		t.Fatal(out)
	}
	observed.TeamID = "other-team"
	if out := inboundPreview(p, r, observed); out.Reason != "provider_identity_changed" {
		t.Fatal(out)
	}
}

func TestInboundPreviewRejectsSourceChangesDuringRead(t *testing.T) {
	for _, kind := range []string{"lifecycle", "group", "connection"} {
		t.Run(kind, func(t *testing.T) {
			s, host, store, provider := autoFixture()
			host.tokenHook = func() {
				switch kind {
				case "lifecycle":
					host.p.Issue.State = "resolved"
				case "group":
					host.p.GroupFingerprint += "changed"
				case "connection":
					host.p.Connection.RepositoryID++
				}
			}
			_, _, err := s.ReadInboundPreview(t.Context(), store.r)
			if err == nil || provider.writes != 0 {
				t.Fatal("stale preview accepted", err)
			}
		})
	}
}

func TestLinearInboundPreviewPinsCatalogAndRead(t *testing.T) {
	for _, kind := range []string{"valid", "catalog race", "read race", "revision race"} {
		t.Run(kind, func(t *testing.T) {
			s, host, store, original := linearAutoFixture()
			status := original.linearStatusFake
			provider := &workflowCatalogFake{linearStatusFake: status, catalog: []issuetracker.LinearWorkflowState{{ID: status.status.StateID, TeamID: status.status.TeamID, Name: "Doing", Type: "started", State: "open"}}}
			s.Provider = provider
			switch kind {
			case "catalog race":
				provider.hook = func() { host.p.Issue.State = "resolved" }
			case "read race":
				status.readHook = func() { host.p.GroupFingerprint += "changed" }
			case "revision race":
				status.readHook = func() { host.p.Connection.Linear.Revision++ }
			}
			_, catalog, out, err := s.ReadInboundPreviewWithWorkflows(t.Context(), store.r)
			if kind == "valid" {
				if err != nil || len(catalog) != 1 || out.Reason != "provider_unchanged" {
					t.Fatal(out, err)
				}
			} else if err == nil {
				t.Fatal("changed source accepted")
			}
			if status.writes != 0 || store.pending != 0 {
				t.Fatal("preview wrote state")
			}
		})
	}
}
