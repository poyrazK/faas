package fcvm

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

// WithQualificationFrameworkReadyRecorder wires the durable sink for a
// restored qualification target's framework-ready receipt.
func (m *Manager) WithQualificationFrameworkReadyRecorder(record func(context.Context, state.EnvironmentQualificationExecution, string, int64) error) *Manager {
	m.qualificationFrameworkReadyRecorder = record
	return m
}

// recordRestoredQualificationFrameworkReady reads the framework-ready state a
// restored runner kept in guest memory since capture and records it for the
// restore frame. The guest serves that state on request and never pushes it
// again after a restore. An app without a runtime runner has no such signal
// and owes no receipt, as production stamps none for it (ADR 20261023093000).
func (m *Manager) recordRestoredQualificationFrameworkReady(ctx context.Context, frame state.EnvironmentQualificationExecution, runtime string) error {
	if runtime == "" {
		return nil
	}
	if m.frameworkReadyReader == nil || m.qualificationFrameworkReadyRecorder == nil {
		return fmt.Errorf("native qualification restore: framework-ready evidence is unavailable: %w", state.ErrConflict)
	}
	ctx, cancel := context.WithTimeout(ctx, qualificationConfigReceiptWait)
	defer cancel()
	for {
		readCtx, stop := context.WithTimeout(ctx, 2*time.Second)
		status, err := m.frameworkReadyReader(readCtx, frame.InstanceID)
		stop()
		if err == nil && status.Ready {
			return m.qualificationFrameworkReadyRecorder(ctx, frame, runtime, int64(status.WarmupMs))
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("native qualification restore: restored runner reported no framework-ready state: %w", errors.Join(err, ctx.Err()))
		case <-time.After(100 * time.Millisecond):
		}
	}
}
