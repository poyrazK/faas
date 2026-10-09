package state

import (
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

func validateCompensationEffectPayload(payload []byte) error {
	effect, err := api.ParseOperationBusinessEffect(payload)
	if err != nil || effect == nil || effect.Status != "confirmed" {
		return fmt.Errorf("%w: compensation source must be a retained confirmed effect in the same customer, app, and environment", ErrInvalidArgument)
	}
	return nil
}
func (m *MemStore) validateCompensationSourceLocked(op Operation, report api.OperationMilestoneRequest) error {
	compensation, err := api.ParseOperationBusinessCompensation(report.Payload)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidArgument, err)
	}
	if compensation == nil {
		return nil
	}
	data := m.operationMemoryLocked()
	source, exists := data.operations[compensation.SourceEffect.OperationID]
	if !exists || source.AccountID != op.AccountID || source.AppID != op.AppID || source.PlatformTenantID != op.PlatformTenantID || source.Scope != op.Scope || !operationRetained(source, time.Now().UTC()) {
		return fmt.Errorf("%w: compensation source must be a retained confirmed effect in the same customer, app, and environment", ErrInvalidArgument)
	}
	receipt, exists := data.milestones[source.ID][compensation.SourceEffect.MilestoneID]
	if !exists {
		return fmt.Errorf("%w: compensation source must be a retained confirmed effect in the same customer, app, and environment", ErrInvalidArgument)
	}
	return validateCompensationEffectPayload(receipt.Milestone.Payload)
}
