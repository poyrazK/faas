package fcvm

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// OperatorRestartGeneration identifies one physical launch. The native identity
// survives daemon recovery; the non-native fallback deliberately requires the
// same Manager incarnation. An absent VM is not proof that this operator killed it.
func (m *Manager) OperatorRestartGeneration(instance string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	inst := m.live[instance]
	if inst == nil {
		return "", fmt.Errorf("operator restart: instance %s has no live generation", instance)
	}
	if inst.nativeGeneration != "" {
		return inst.nativeGeneration, nil
	}
	if inst.operatorRestartGeneration == "" {
		inst.operatorRestartGeneration = uuid.NewString()
	}
	return inst.operatorRestartGeneration, nil
}

// DestroyOperatorRestart reserves teardown against the observed live pointer
// before touching resources. The ordinary unknown-instance destroy fallback is
// intentionally unavailable here: it could target a later launch with that ID.
func (m *Manager) DestroyOperatorRestart(ctx context.Context, instance, generation string) error {
	m.mu.Lock()
	expected := m.live[instance]
	current := ""
	if expected != nil {
		current = expected.nativeGeneration
		if current == "" {
			current = expected.operatorRestartGeneration
		}
	}
	m.mu.Unlock()
	if generation == "" || current != generation {
		return fmt.Errorf("operator restart: physical VM generation changed")
	}
	flight, owner, err := m.beginLiveInstanceTeardown(ctx, instance, expected)
	if err != nil {
		return err
	}
	if !owner {
		return fmt.Errorf("operator restart: another teardown owned this physical VM")
	}
	code, destroyErr := m.destroyWithReservation(ctx, context.WithoutCancel(ctx), instance, "")
	m.finishInstanceTeardown(instance, flight, code, false, false, destroyErr)
	return destroyErr
}
