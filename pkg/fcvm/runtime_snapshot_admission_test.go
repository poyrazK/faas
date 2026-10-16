// adr: 595
package fcvm

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

func admittedSnapshotNativeFixture(t *testing.T) (*Manager, *capturedRuntimeVMM, runtimeadmission.SnapshotGrant) {
	t.Helper()
	m, original, request := consumedRuntimeFixture(t)
	v := &capturedRuntimeVMM{consumedRuntimeVMM: original}
	m.vmm = v
	_, parent, err := m.WakeAdmitted(t.Context(), request, nil)
	if err != nil {
		t.Fatal(err)
	}
	return m, v, managedSnapshotGrant(m, parent, true)
}

func TestCaptureAdmittedRefusesStaleGrantWithoutPausingHealthyRuntime(t *testing.T) {
	for _, tc := range []struct {
		name string
		edit func(*Manager, *runtimeadmission.SnapshotGrant)
	}{
		{"node", func(_ *Manager, g *runtimeadmission.SnapshotGrant) { g.Parent.Binding.NodeID = uuid.NewString() }},
		{"incarnation", func(_ *Manager, g *runtimeadmission.SnapshotGrant) { g.Parent.Binding.Incarnation = uuid.NewString() }},
		{"parent", func(_ *Manager, g *runtimeadmission.SnapshotGrant) { g.Parent.Netns = "other" }},
		{"Firecracker version", func(_ *Manager, g *runtimeadmission.SnapshotGrant) { g.FCVersion += "-different" }},
		{"expired", func(_ *Manager, g *runtimeadmission.SnapshotGrant) {
			g.IssuedAtUnixNano = time.Now().Add(-time.Minute).UnixNano()
			g.ExpiresAtUnixNano = time.Now().Add(-time.Second).UnixNano()
		}},
		{"egress revision", func(m *Manager, g *runtimeadmission.SnapshotGrant) {
			m.appEgressPolicies[g.Parent.Binding.AppID] = appEgressPolicy{revision: g.Parent.Binding.EgressRevision + 1}
		}},
		{"changed scope", func(m *Manager, g *runtimeadmission.SnapshotGrant) {
			m.live[g.Parent.Binding.InstanceID].AccountID = uuid.NewString()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, v, g := admittedSnapshotNativeFixture(t)
			tc.edit(m, &g)
			i, a, err := m.CaptureAdmitted(t.Context(), g)
			if err == nil || !i.Capture.IsZero() || a.Grant.Token != "" || v.seen.StorageKey != "" || m.LiveCount() != 1 || m.LeasedCount() != 1 {
				t.Fatal("invalid grant paused or destroyed the healthy runtime", err)
			}
		})
	}
}

func TestMeasuredRuntimeCannotUseLegacySnapshotEntryPoint(t *testing.T) {
	m, v, g := admittedSnapshotNativeFixture(t)
	for _, warm := range []bool{true, false} {
		spec := SnapshotSpec{StorageKey: g.MemoryKey, VMStateStorageKey: g.VMStateKey, admittedParent: g.Parent.Clone()}
		var err error
		if warm {
			_, err = m.WarmSnapshot(t.Context(), g.Parent.Binding.InstanceID, spec)
		} else {
			_, err = m.Park(t.Context(), g.Parent.Binding.InstanceID, spec)
		}
		if !errors.Is(err, runtimeadmission.ErrUnavailable) || v.seen.StorageKey != "" || m.LiveCount() != 1 {
			t.Fatal("legacy capture bypassed fresh authority", err)
		}
	}
	if _, _, err := m.CaptureAdmitted(t.Context(), g); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.CaptureAdmitted(t.Context(), g); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("grant replay accepted", err)
	}
}

func TestCaptureAdmittedDestroyCancelsAndJoinsNativeFlight(t *testing.T) {
	m, v, g := admittedSnapshotNativeFixture(t)
	entered := make(chan struct{})
	v.before = func(ctx context.Context) error { close(entered); <-ctx.Done(); return ctx.Err() }
	result := make(chan error, 1)
	go func() {
		i, a, err := m.CaptureAdmitted(t.Context(), g)
		if !i.Capture.IsZero() || a.Grant.Token != "" {
			result <- errors.New("canceled capture returned evidence")
			return
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not reach backend")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if err := m.Destroy(ctx, g.Parent.Binding.InstanceID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("destroy failed to join capture")
	}
	if m.LiveCount() != 0 || m.LeasedCount() != 0 {
		t.Fatal("destroy retained native resources")
	}
}

func TestCaptureAdmittedFencesOutboundWriterAndOverlappingCapture(t *testing.T) {
	m, v, g := admittedSnapshotNativeFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	v.before = func(ctx context.Context) error {
		close(entered)
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	result := make(chan error, 1)
	go func() { _, _, err := m.CaptureAdmitted(t.Context(), g); result <- err }()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not reach backend")
	}
	if _, _, err := m.CaptureAdmitted(t.Context(), managedSnapshotGrant(m, g.Parent, true)); !errors.Is(err, runtimeadmission.ErrReplay) {
		t.Fatal("overlapping capture accepted", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := m.UpdateAppEgressPolicy(ctx, g.Parent.Binding.AppID, g.Parent.Binding.EgressRevision+1, nil, nil); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("outbound policy raced capture publication", err)
	}
	close(release)
	select {
	case err := <-result:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("capture did not finish")
	}
	if err := m.UpdateAppEgressPolicy(t.Context(), g.Parent.Binding.AppID, g.Parent.Binding.EgressRevision+1, nil, nil); err != nil {
		t.Fatal("capture retained outbound read gate", err)
	}
}
