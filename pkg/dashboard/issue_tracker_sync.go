package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

// IssueTrackerSyncPreview is an owner-requested provider observation. ExpectedJSON
// contains only the credential-free native snapshot and is escaped by templates.
type IssueTrackerSyncPreview struct {
	AppSlug, IssueID, LinkID, CSRF, Provider, SourceState, ExpectedJSON string
	InboundOutcome, InboundReason                                       string
	InboundGuardRevision                                                int64
	CanPull                                                             bool
	FixingDeployments                                                   []IssueTrackerDeploymentSuggestion
	Status                                                              api.IssueTrackerProviderStatus
	Policy                                                              api.IssueTrackerAutoSync
	Aligned                                                             bool
	CanPush                                                             bool
	CanEnable                                                           bool
	OpenWorkflows, ResolvedWorkflows, PushWorkflows                     []issuetracker.LinearWorkflowState
	OpenWorkflowName, ResolvedWorkflowName                              string
}

func IssueTrackerSyncReason(reason string) string {
	switch reason {
	case "source_intent_changed":
		return "Local issue intent changed since delegation. Review both lifecycles before re-enabling."
	case "source_revision_required":
		return "Review and re-enable this direction to establish a fresh source baseline."
	case "fixing_deployment_required":
		return "The provider resolved this ticket. Review the inbound change and select its fixing deployment to resolve Gregale."
	case "provider_workflow_unmapped":
		return "The provider workflow is outside the selected mapping. Review it before re-enabling."
	case "provider_changed":
		return "The linked ticket was edited. Review and align its lifecycle before re-enabling."
	case "source_connection_or_provider_changed":
		return "The connection, issue group or provider snapshot changed. Review before re-enabling."
	case "pending_outcome_unconfirmed":
		return "A write is still unconfirmed. Gregale is observing it without sending it again."
	case "source_changed_after_dispatch":
		return "The Gregale lifecycle changed while a write was unconfirmed. Review both issues."
	case "source_state_unsupported":
		return "This Gregale lifecycle cannot be synchronized automatically."
	case "provider_state_unsupported":
		return "The provider lifecycle needs manual triage."
	case "connection_required":
		return "Restore provider access, then review and re-enable."
	case "owner_or_entitlement_unavailable":
		return "The owner or Issues subscription is unavailable."
	case "reference_or_policy_changed":
		return "The reference or policy changed. Reload before continuing."
	case "provider_unavailable":
		return "The provider could not confirm the operation. Inspect any pending write before continuing."
	case "app_unavailable", "account_unavailable":
		return "Gregale could not load the app or owner."
	case "":
		return ""
	default:
		return "Sync needs attention. Review both issues before continuing."
	}
}

// SetLinearWorkflowChoices maps a freshly verified catalog to the stored IDs.
// Names describe the current provider state; they never authorize a mutation.
func (v *IssueTrackerSyncPreview) SetLinearWorkflowChoices(catalog []issuetracker.LinearWorkflowState) {
	for _, state := range catalog {
		switch state.State {
		case "open":
			v.OpenWorkflows = append(v.OpenWorkflows, state)
		case "resolved":
			v.ResolvedWorkflows = append(v.ResolvedWorkflows, state)
		}
		if state.State == v.SourceState {
			v.PushWorkflows = append(v.PushWorkflows, state)
		}
		if state.ID == v.Policy.LinearOpenStateID && state.State == "open" {
			v.OpenWorkflowName = state.Name
		}
		if state.ID == v.Policy.LinearResolvedStateID && state.State == "resolved" {
			v.ResolvedWorkflowName = state.Name
		}
	}
	v.CanEnable = v.Aligned && len(v.OpenWorkflows) > 0 && len(v.ResolvedWorkflows) > 0
	v.CanPush = v.CanPush && len(v.PushWorkflows) > 0
}

// IssueTrackerInboundReason describes a read-only assessment, not permission to apply it.
func IssueTrackerInboundReason(reason string) string {
	switch reason {
	case "binding_changed":
		return "The saved baseline belongs to a different connection or issue group, or no baseline exists. Align and review automatic sync first."
	case "outbound_pending":
		return "A Gregale write is unconfirmed. Resolve its outcome before considering an inbound change."
	case "policy_disabled":
		return "Automatic sync is disabled. Align and review a baseline before considering inbound changes."
	case "policy_requires_review":
		return "The policy is paused for another conflict or access problem. Resolve it before considering inbound changes."
	case "state_unsupported":
		return "Ignored or unsupported lifecycles require manual triage."
	case "provider_identity_changed":
		return "The provider ticket identity differs from the saved baseline. Review the reference and connection."
	case "source_changed":
		return "Gregale changed since the saved baseline. Review both sides; the provider must not overwrite this local change."
	case "provider_unchanged":
		return "The provider still matches the saved baseline. There is no inbound change."
	case "workflow_unmapped":
		return "The Linear workflow is outside this policy's selected mapping. Review the workflow before synchronizing."
	case "lifecycle_unchanged":
		return "The provider workflow changed within the same lifecycle. No Gregale lifecycle change is needed."
	case "provider_reopened":
		return "The provider reopened the ticket while Gregale still matches the saved baseline. Reopening is an inbound candidate."
	case "fixing_deployment_required":
		return "The provider resolved the ticket while Gregale still matches the saved baseline. Resolution requires an explicitly selected fixing deployment in this app and environment."
	default:
		return "Review both lifecycles before considering an inbound change."
	}
}
