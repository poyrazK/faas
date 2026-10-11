package faas

import "github.com/poyrazK/faas/sdk/go/internal/api"

type OperationBusinessEffectReference = api.OperationBusinessEffectReference
type OperationBusinessCompensation = api.OperationBusinessCompensation
type OperationBusinessCompensationPayload = api.OperationBusinessCompensationPayload

// BusinessCompensation records a reversal observation; it never executes one.
func (tx *CustomerOperationTransaction) BusinessCompensation(milestone string, compensation OperationBusinessCompensation) error {
	if err := api.ValidateOperationBusinessCompensation(compensation); err != nil {
		return err
	}
	return tx.Milestone(milestone, OperationBusinessCompensationPayload{Kind: api.OperationBusinessCompensationKind, Compensation: compensation})
}
