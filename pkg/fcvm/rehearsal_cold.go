package fcvm

import (
	"context"
	"errors"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
	"time"
)

func (m *Manager) RetireRehearsalCold(ctx context.Context, instance, preparation string) (err error) {
	m.mu.Lock()
	inst, session := m.live[instance], m.rehearsalRecorders[instance]
	m.mu.Unlock()
	store, ok := m.rehearsalNetworkPolicies.(rehearsalrecorder.ColdStore)
	if !ok || inst == nil || session == nil || inst.AppTaskOnly || inst.IsJob || inst.Net.RehearsalRunID == "" {
		return errors.New("original cold-preparation VM owner is unavailable")
	}
	flight, owner, err := m.beginLiveInstanceTeardown(ctx, instance, inst)
	if err != nil {
		return err
	}
	if !owner {
		return errors.New("another teardown owner stopped the cold-preparation VM")
	}
	defer func() { m.finishInstanceTeardown(instance, flight, 0, false, false, err) }()
	defer func() {
		if err != nil {
			session.Fail()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = store.FailScenarioColdPreparation(cleanup, inst.AppID)
			cancel()
		}
		m.stopRehearsalRecorder(instance)
	}()
	if err = session.PrepareColdRetirement(ctx, preparation); err != nil {
		return err
	}
	if _, err = m.destroyWithReservation(ctx, context.WithoutCancel(ctx), instance, ""); err != nil {
		return err
	}
	return session.FinishColdRetirement(ctx, preparation)
}
