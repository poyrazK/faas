// adr: 1356
package fcvm

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

// TerminateRehearsalWorker requires the original live recorder, never an adopted
// database row. Destroy's physical cleanup remains the only resource owner.
func (m *Manager) TerminateRehearsalWorker(ctx context.Context, instance, fault string) (err error) {
	m.mu.Lock()
	inst := m.live[instance]
	session := m.rehearsalRecorders[instance]
	m.mu.Unlock()
	store, ok := m.rehearsalNetworkPolicies.(rehearsalrecorder.WorkerFaultStore)
	if !ok || inst == nil || session == nil || inst.AppTaskOnly || inst.IsJob || inst.Net.RehearsalRunID == "" {
		return errors.New("original rehearsal worker owner is unavailable")
	}
	flight, owner, err := m.beginLiveInstanceTeardown(ctx, instance, inst)
	if err != nil {
		return err
	}
	if !owner {
		return errors.New("another teardown owner stopped the worker")
	}
	defer func() { m.finishInstanceTeardown(instance, flight, 0, false, false, err) }()
	defer func() {
		if err != nil {
			session.Fail()
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			_ = store.FailScenarioWorkerFault(cleanup, fault)
			cancel()
		}
		m.stopRehearsalRecorder(instance)
	}()
	if err = session.PrepareWorkerTermination(ctx, fault); err != nil {
		return err
	}
	if _, err = m.destroyWithReservation(ctx, context.WithoutCancel(ctx), instance, ""); err != nil {
		return err
	}
	return session.FinishWorkerTermination(ctx, fault)
}
