// adr: 468 — check durable admission inside a destruction-joined flight.
package fcvm

import (
	"context"
	"errors"
	"fmt"
)

var (
	ErrAppAdmissionFenced      = errors.New("fcvm: app admission fenced for database cutover")
	ErrAppAdmissionUnavailable = errors.New("fcvm: app admission check unavailable")
)

// WithAppAdmissionGuard installs a read-only control-plane check before app
// boot/resume. Configure it before serving RPCs. Unconfigured embedders preserve
// legacy behavior and cannot participate in a managed PostgreSQL cutover drain.
// The callback must read current durable state, without a replica or TTL cache.
func (m *Manager) WithAppAdmissionGuard(guard func(context.Context, string) error) *Manager {
	m.appAdmissionGuard = guard
	return m
}

func (m *Manager) checkAppAdmission(ctx context.Context, appID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if appID == "" || m.appAdmissionGuard == nil {
		return nil
	}
	if err := m.appAdmissionGuard(ctx, appID); err != nil {
		return fmt.Errorf("manager: check app admission: %w", err)
	}
	// A guard can finish concurrently with Destroy's cancellation.
	return ctx.Err()
}

func (m *Manager) checkWakeAdmission(ctx context.Context, req WakeRequest) error {
	if m.appAdmissionGuard != nil && req.AppID == "" && !req.ExecutionOnly && req.ExportDir == "" {
		return ErrAppAdmissionUnavailable
	}
	return m.checkAppAdmission(ctx, req.AppID)
}

func (m *Manager) checkLiveAdmission(ctx context.Context, inst *Instance) error {
	if m.appAdmissionGuard != nil && inst.AppID == "" && !inst.ExecutionOnly && !inst.IsJob && !inst.Lease.IsBuilder {
		return ErrAppAdmissionUnavailable
	}
	return m.checkAppAdmission(ctx, inst.AppID)
}
