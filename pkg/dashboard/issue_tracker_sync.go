package dashboard

import (
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issuetracker"
)

// IssueTrackerSyncPreview is an owner-requested provider observation. ExpectedJSON
// contains only the credential-free native snapshot and is escaped by templates.
type IssueTrackerSyncPreview struct {
	AppSlug, IssueID, LinkID, CSRF, Provider, SourceState, ExpectedJSON string
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
		if state.State == "open" {
			v.OpenWorkflows = append(v.OpenWorkflows, state)
		} else if state.State == "resolved" {
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
