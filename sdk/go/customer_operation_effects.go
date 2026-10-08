package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type OperationBusinessEffect = api.OperationBusinessEffect
type OperationBusinessEffectPayload = api.OperationBusinessEffectPayload
type OperationWorkflowEffectRequirement = api.OperationWorkflowEffectRequirement
type OperationWorkflowPlannedEffect = api.OperationWorkflowPlannedEffect
type OperationWorkflowUnmetEffect = api.OperationWorkflowUnmetEffect

// BusinessEffect records application-confirmed facts, not an external effect executor.
func (tx *CustomerOperationTransaction) BusinessEffect(milestone string, effect OperationBusinessEffect) error {
	if err := api.ValidateOperationBusinessEffect(effect); err != nil {
		return err
	}
	return tx.Milestone(milestone, OperationBusinessEffectPayload{Kind: api.OperationBusinessEffectKind, Effect: effect})
}
