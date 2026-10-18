package fcvm

// adr: 20261018030614
// These fakes exercise host fencing, not physical SQL acceptance.

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/activity"
	"github.com/onebox-faas/faas/pkg/functionlifetime"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type admittedMigrationVMM struct {
	*capturedRuntimeVMM
	captures, recoveries int
	recover              func(context.Context) error
}

func (v *admittedMigrationVMM) SnapshotKeepAlive(ctx context.Context, lease Lease, spec SnapshotSpec) (SnapshotInfo, error) {
	v.captures++
	return v.capturedRuntimeVMM.SnapshotKeepAlive(ctx, lease, spec)
}

func (v *admittedMigrationVMM) RecoverFunctionResources(ctx context.Context, _ Lease) error {
	v.recoveries++
	if v.recover != nil {
		return v.recover(ctx)
	}
	return nil
}

func admittedMigrationFixture(t *testing.T) (*Manager, *Instance, *admittedMigrationVMM, FunctionMigrationBinding, runtimeadmission.FunctionMigrationGrant) {
	t.Helper()
	if !functionlifetime.NativeQualificationBuild {
		t.Skip("qualified Node build only")
	}
	m, original, request := consumedRuntimeFixture(t)
	v := &admittedMigrationVMM{capturedRuntimeVMM: &capturedRuntimeVMM{consumedRuntimeVMM: original}}
	m.vmm = v
	inst, parent, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	inst.Runtime, inst.wakeID = "node22", uuid.NewString()
	m.WithFunctionExecutionActivity(activity.NewWithDefaults())
	binding := FunctionMigrationBinding{InstanceID: inst.Lease.Instance, SourceNodeID: parent.Binding.NodeID, SourceWakeID: inst.wakeID, LeaseToken: runtimeadmission.FunctionMigrationLeasePrefix + uuid.NewString()}
	warm := managedSnapshotGrant(m, parent, true)
	now := time.Now()
	g := runtimeadmission.FunctionMigrationGrant{Version: 1, Token: warm.Token, CaptureToken: warm.Token, Purpose: runtimeadmission.FunctionMigrationCapture,
		Parent: parent.Clone(), LeaseToken: binding.LeaseToken, SourceWakeID: binding.SourceWakeID,
		LeaseCreatedAtUnixNano: now.Add(-time.Minute).UnixNano(), LeaseExpiresAtUnixNano: now.Add(time.Minute).UnixNano(),
		SourceStartedAtUnixNano: parent.CompletedAtUnixNano, MemoryKey: warm.MemoryKey, VMStateKey: warm.VMStateKey, PrivateDriveKey: warm.PrivateDriveKey,
		FCVersion: m.fcVersion, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: now.Add(time.Minute).UnixNano()}
	return m, inst, v, binding, g
}

func admittedMigrationRecovery(g runtimeadmission.FunctionMigrationGrant) runtimeadmission.FunctionMigrationGrant {
	r := g.Clone()
	r.Purpose, r.Token = runtimeadmission.FunctionMigrationRecover, uuid.NewString()
	r.IssuedAtUnixNano, r.ExpiresAtUnixNano = time.Now().UnixNano(), time.Now().Add(time.Minute).UnixNano()
	return r
}

func allowMigrationGrant(ctx context.Context, g runtimeadmission.FunctionMigrationGrant) error {
	return errors.Join(ctx.Err(), g.Validate(time.Now()))
}

func prepareAdmittedMigration(t *testing.T, m *Manager, binding FunctionMigrationBinding, g runtimeadmission.FunctionMigrationGrant) {
	t.Helper()
	_, err := m.PrepareAdmittedFunctionMigrationSource(t.Context(), binding, SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey},
		func(_ context.Context, parent runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error) {
			if !parent.Equal(g.Parent) {
				t.Error("issuer was not given retained physical parent")
			}
			return g.Clone(), nil
		}, allowMigrationGrant)
	if err != nil {
		t.Fatal(err)
	}
}

func TestFunctionMigrationAdmittedRecoveryACKAndCanceledRPC(t *testing.T) {
	for _, runtime := range []string{"node22", "node24"} {
		t.Run(runtime, func(t *testing.T) {
			m, inst, v, binding, g := admittedMigrationFixture(t)
			inst.Runtime = runtime
			prepareAdmittedMigration(t, m, binding, g)
			if v.captures != 1 || !v.seen.BeforeCheckpoint || v.seen.ResumeBeforePublish || !v.seen.admittedParent.Equal(g.Parent) {
				t.Fatal("capture lost purpose or physical parent")
			}
			if _, err := m.RecoverFunctionMigrationSource(t.Context(), binding, func(context.Context) error { return nil }); !errors.Is(err, runtimeadmission.ErrUnavailable) {
				t.Fatal("legacy recovery bypassed managed permission", err)
			}
			r := admittedMigrationRecovery(g)
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			defer unblock()
			v.recover = func(ctx context.Context) error {
				close(entered)
				select {
				case <-release:
					return ctx.Err()
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				retired, err := m.RecoverAdmittedFunctionMigrationSource(ctx, binding, r, allowMigrationGrant)
				if retired {
					err = errors.Join(err, errors.New("healthy recovery retired source"))
				}
				done <- err
			}()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("recovery did not start")
			}
			cancel()
			if m.receiveFunctionExecution(inst.Lease, executionRequest(functionlifetime.Begin, 1)).Status != functionlifetime.Busy {
				t.Fatal("ordinary request preceded final ACK")
			}
			if _, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, allowMigrationGrant); !errors.Is(err, ErrInstanceBusy) {
				t.Fatal("duplicate recovery entered callback", err)
			}
			unblock()
			if err := <-done; err != nil {
				t.Fatal("RPC cancellation canceled owner recovery", err)
			}
			for range 2 {
				if retired, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, allowMigrationGrant); err != nil || retired {
					t.Fatal("lost ACK retry lost completed result", retired, err)
				}
			}
			if v.recoveries != 1 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("recovery replayed callback or released source capacity")
			}
		})
	}
}

func TestFunctionMigrationAdmittedRefusesStaleCaptureBeforeEffects(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*runtimeadmission.FunctionMigrationGrant)
	}{
		{"wake", func(g *runtimeadmission.FunctionMigrationGrant) { g.SourceWakeID = uuid.NewString() }},
		{"lease", func(g *runtimeadmission.FunctionMigrationGrant) {
			g.LeaseToken = runtimeadmission.FunctionMigrationLeasePrefix + uuid.NewString()
		}},
		{"parent", func(g *runtimeadmission.FunctionMigrationGrant) { g.Parent.LeaseUID++ }},
		{"incarnation", func(g *runtimeadmission.FunctionMigrationGrant) { g.Parent.Binding.Incarnation = uuid.NewString() }},
		{"FC version", func(g *runtimeadmission.FunctionMigrationGrant) { g.FCVersion = "other" }},
		{"different capture", func(g *runtimeadmission.FunctionMigrationGrant) {
			old, token := g.CaptureToken, uuid.NewString()
			g.Token, g.CaptureToken = token, token
			g.MemoryKey = strings.ReplaceAll(g.MemoryKey, old, token)
			g.VMStateKey = strings.ReplaceAll(g.VMStateKey, old, token)
			g.PrivateDriveKey = strings.ReplaceAll(g.PrivateDriveKey, old, token)
		}},
		{"expiry", func(g *runtimeadmission.FunctionMigrationGrant) { g.ExpiresAtUnixNano = g.IssuedAtUnixNano }},
		{"purpose", func(g *runtimeadmission.FunctionMigrationGrant) { *g = admittedMigrationRecovery(*g) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _, v, binding, g := admittedMigrationFixture(t)
			bad := g.Clone()
			tc.edit(&bad)
			_, err := m.PrepareAdmittedFunctionMigrationSource(t.Context(), binding, SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey},
				func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error) {
					return bad, nil
				}, allowMigrationGrant)
			if err == nil || v.captures != 0 || v.recoveries != 0 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("stale authority reached effects", err)
			}
		})
	}
}

func TestFunctionMigrationAdmittedRecoveryFailureRetiresWithoutReplay(t *testing.T) {
	for _, kind := range []string{"probe", "permission expiry", "post-ACK drift"} {
		t.Run(kind, func(t *testing.T) {
			m, _, v, binding, g := admittedMigrationFixture(t)
			prepareAdmittedMigration(t, m, binding, g)
			r := admittedMigrationRecovery(g)
			checks := 0
			check := func(ctx context.Context, grant runtimeadmission.FunctionMigrationGrant) error {
				checks++
				if kind == "post-ACK drift" && checks > 1 {
					return runtimeadmission.ErrStale
				}
				return allowMigrationGrant(ctx, grant)
			}
			switch kind {
			case "probe":
				v.recover = func(context.Context) error { return io.EOF }
			case "permission expiry":
				r.ExpiresAtUnixNano = time.Now().Add(100 * time.Millisecond).UnixNano()
				v.recover = func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }
			}
			retired, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, check)
			if err != nil || !retired || m.LiveCount() != 0 || m.LeasedCount() != 0 || v.recoveries != 1 {
				t.Fatal("unconfirmed recovery retained or reopened source", retired, err)
			}
			r = admittedMigrationRecovery(g)
			if retired, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, allowMigrationGrant); err != nil || !retired || v.recoveries != 1 {
				t.Fatal("retry replayed uncertain recovery", retired, err)
			}
		})
	}
}

func TestFunctionMigrationAdmittedLostRegistrationACKHasNoEffects(t *testing.T) {
	m, _, v, binding, g := admittedMigrationFixture(t)
	_, err := m.PrepareAdmittedFunctionMigrationSource(t.Context(), binding, SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey},
		func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error) {
			return runtimeadmission.FunctionMigrationGrant{}, io.EOF
		}, allowMigrationGrant)
	if !errors.Is(err, io.EOF) || v.captures != 0 {
		t.Fatal("lost registration reply reached capture", err)
	}
	r := admittedMigrationRecovery(g)
	if retired, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, allowMigrationGrant); err != nil || retired || v.recoveries != 0 || m.LiveCount() != 1 {
		t.Fatal("known no-effects result replayed application recovery", retired, err)
	}
}

func TestFunctionMigrationAdmittedRejectedCaptureRetiresWithoutRecovery(t *testing.T) {
	m, _, v, binding, g := admittedMigrationFixture(t)
	checks := 0
	_, err := m.PrepareAdmittedFunctionMigrationSource(t.Context(), binding, SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey},
		func(context.Context, runtimeadmission.Receipt) (runtimeadmission.FunctionMigrationGrant, error) {
			return g.Clone(), nil
		},
		func(ctx context.Context, g runtimeadmission.FunctionMigrationGrant) error {
			checks++
			if checks == 2 {
				return runtimeadmission.ErrStale
			}
			return allowMigrationGrant(ctx, g)
		})
	if !errors.Is(err, runtimeadmission.ErrStale) || v.captures != 1 || m.LiveCount() != 1 || m.LeasedCount() != 1 {
		t.Fatal("rejected capture lost pending physical source", err)
	}
	r := admittedMigrationRecovery(g)
	if retired, err := m.RecoverAdmittedFunctionMigrationSource(t.Context(), binding, r, allowMigrationGrant); err != nil || !retired || v.recoveries != 0 || m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("uncertain checkpoint replayed recovery instead of retiring", retired, err)
	}
}
