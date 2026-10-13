package sched

// adr: 171 — runtime snapshot capture fills catalog misses without tenant data.

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type fakeCaptureVMM struct {
	mu        sync.Mutex
	restores  []ExecutionRestoreRequest
	snapshots []string
	destroyed []string
	instance  func(ExecutionRestoreRequest) string
	snapErr   error
	backend   storage.StorageBackend
}

func (f *fakeCaptureVMM) RestoreExecution(_ context.Context, _ string, req ExecutionRestoreRequest) (*ExecutionRestoreOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restores = append(f.restores, req)
	instance := req.ID
	if f.instance != nil {
		instance = f.instance(req)
	}
	return &ExecutionRestoreOutcome{Instance: instance}, nil
}

func (f *fakeCaptureVMM) PauseAndSnapshot(ctx context.Context, _, instance, _, storageKey, vmstateKey string, _ bool) (SnapshotBytes, error) {
	f.mu.Lock()
	f.snapshots = append(f.snapshots, instance)
	f.mu.Unlock()
	if f.snapErr != nil {
		return SnapshotBytes{}, f.snapErr
	}
	if err := f.backend.Put(ctx, storageKey, strings.NewReader("guest-memory")); err != nil {
		return SnapshotBytes{}, err
	}
	if err := f.backend.Put(ctx, vmstateKey, strings.NewReader("vm-state")); err != nil {
		return SnapshotBytes{}, err
	}
	return SnapshotBytes{MemBytes: int64(len("guest-memory")), VMStateBytes: int64(len("vm-state"))}, nil
}

func (f *fakeCaptureVMM) Destroy(_ context.Context, _, instance string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.destroyed = append(f.destroyed, instance)
	return nil
}

func captureTestIdentity() RuntimeSnapshotIdentity {
	return RuntimeSnapshotIdentity{
		Runtime: api.ExecutionRuntimeNode22, Architecture: archAMD64,
		KernelDigest: strings.Repeat("1", 64), GuestExecutorDigest: strings.Repeat("2", 64),
		BaseImageDigest: strings.Repeat("3", 64), MemoryMB: 128, EphemeralDiskMB: 64,
		FormatVersion: CurrentRuntimeSnapshotFormatVersion,
	}
}

func captureTestTemplate() ExecutionRestoreRequest {
	return ExecutionRestoreRequest{
		ID: "tenant-run-id", AccountID: "acct-1", NodeID: "node-a", Plan: api.PlanPro,
		Runtime: api.ExecutionRuntimeNode22, NetworkMode: api.ExecutionNetworkNone,
		LeaseToken: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa", OutboundIntegrationIDs: []string{"11111111-1111-4111-8111-111111111111"},
		KernelKey: "kernel/1.10.1", BaseKey: "base/runner-node22-amd64.ext4", LayerKey: "execution/layer-v1-standard-amd64.ext4",
		VcpuCount: api.ExecutionVCPUCount, MemSizeMiB: 128, CPUMillicores: 250,
	}
}

func newCaptureFixture(t *testing.T) (*RuntimeSnapshotCapturer, *fakeCaptureVMM, *state.MemStore) {
	t.Helper()
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	store := state.NewMemStore()
	vmm := &fakeCaptureVMM{backend: backend}
	capturer := NewRuntimeSnapshotCapturer(vmm, NewRuntimeSnapshotPublisher(store, backend), nil)
	ids := 0
	capturer.newID = func() string {
		ids++
		return "capture-" + strings.Repeat("0", ids)
	}
	return capturer, vmm, store
}

func runOneCapture(t *testing.T, capturer *RuntimeSnapshotCapturer) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		capturer.Run(ctx)
	}()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		capturer.mu.Lock()
		idle := len(capturer.pending) == 0 && len(capturer.jobs) == 0
		capturer.mu.Unlock()
		if idle {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
}

func TestRuntimeSnapshotCapturerPublishesPayloadFreeCapture(t *testing.T) {
	capturer, vmm, store := newCaptureFixture(t)
	identity := captureTestIdentity()
	if !capturer.Request(identity, captureTestTemplate()) {
		t.Fatal("first capture request was not queued")
	}
	if capturer.Request(identity, captureTestTemplate()) {
		t.Fatal("duplicate request for a pending identity was queued")
	}
	runOneCapture(t, capturer)

	if len(vmm.restores) != 1 {
		t.Fatalf("capture boots = %d, want 1", len(vmm.restores))
	}
	boot := vmm.restores[0]
	if boot.ID == "tenant-run-id" || boot.LeaseToken != "" || len(boot.OutboundIntegrationIDs) != 0 || boot.Snapshot.StorageKey != "" {
		t.Fatalf("capture boot carried tenant run state: %+v", boot)
	}
	if boot.BaseKey != "base/runner-node22-amd64.ext4" || boot.MemSizeMiB != 128 || boot.VcpuCount != api.ExecutionVCPUCount {
		t.Fatalf("capture boot machine = %+v", boot)
	}
	key, _ := identity.Key()
	record, err := store.LookupRuntimeSnapshot(context.Background(), key)
	if err != nil {
		t.Fatalf("catalog row missing: %v", err)
	}
	if record.SnapshotDigest != RuntimeSnapshotDigest([]byte("guest-memory"), []byte("vm-state")) || !record.Sanitized || !record.PayloadFree {
		t.Fatalf("catalog row = %+v", record)
	}
	// A published identity is no longer a miss: the resolver restores it.
	catalog := NewRuntimeSnapshotCatalog(NewStateRuntimeSnapshotIndex(store), NewCachingRuntimeSnapshotVerifier(NewStorageRuntimeSnapshotVerifier(vmm.backend)))
	plan, err := catalog.Resolve(context.Background(), RuntimeSnapshotRequest{
		Shape:        api.ExecutionSnapshotShape{Profile: api.ExecutionProfileStandard, Runtime: identity.Runtime, MemoryMB: identity.MemoryMB, EphemeralDiskMB: identity.EphemeralDiskMB},
		Architecture: identity.Architecture, KernelDigest: identity.KernelDigest, GuestExecutorDigest: identity.GuestExecutorDigest,
		BaseImageDigest: identity.BaseImageDigest, FormatVersion: identity.FormatVersion,
	})
	if err != nil || plan.Mode != RuntimeSnapshotRestore {
		t.Fatalf("resolve after capture = %+v, %v; want restore", plan, err)
	}
}

func TestRuntimeSnapshotCapturerBacksOffAfterFailure(t *testing.T) {
	capturer, vmm, _ := newCaptureFixture(t)
	vmm.snapErr = errors.New("guest never acknowledged the resume hook")
	now := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	capturer.now = func() time.Time { return now }
	identity := captureTestIdentity()
	capturer.Request(identity, captureTestTemplate())
	runOneCapture(t, capturer)
	if capturer.Request(identity, captureTestTemplate()) {
		t.Fatal("failed identity was retried immediately")
	}
	now = now.Add(runtimeSnapshotCaptureRetryAfter)
	if !capturer.Request(identity, captureTestTemplate()) {
		t.Fatal("failed identity was not retried after the back-off")
	}
}

func TestRuntimeSnapshotCapturerDestroysUnexpectedInstance(t *testing.T) {
	capturer, vmm, _ := newCaptureFixture(t)
	vmm.instance = func(ExecutionRestoreRequest) string { return "someone-else" }
	capturer.Request(captureTestIdentity(), captureTestTemplate())
	runOneCapture(t, capturer)
	if len(vmm.snapshots) != 0 || len(vmm.destroyed) != 1 || vmm.destroyed[0] != vmm.restores[0].ID {
		t.Fatalf("snapshots=%v destroyed=%v, want the booted capture destroyed without a snapshot", vmm.snapshots, vmm.destroyed)
	}
}

type countingVerifier struct {
	calls int
	err   error
}

func (v *countingVerifier) VerifyRuntimeSnapshot(context.Context, RuntimeSnapshot) error {
	v.calls++
	return v.err
}

func TestCachingRuntimeSnapshotVerifierCachesOnlySuccess(t *testing.T) {
	inner := &countingVerifier{}
	verifier := NewCachingRuntimeSnapshotVerifier(inner)
	snapshot := RuntimeSnapshot{StorageKey: "snap/x/captures/a/mem", SnapshotDigest: strings.Repeat("a", 64)}
	for range 3 {
		if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); err != nil {
			t.Fatalf("verify: %v", err)
		}
	}
	if inner.calls != 1 {
		t.Fatalf("inner verifications = %d, want 1", inner.calls)
	}
	failing := &countingVerifier{err: ErrRuntimeSnapshotCorrupt}
	verifier = NewCachingRuntimeSnapshotVerifier(failing)
	for range 2 {
		if err := verifier.VerifyRuntimeSnapshot(context.Background(), snapshot); !errors.Is(err, ErrRuntimeSnapshotCorrupt) {
			t.Fatalf("verify error = %v", err)
		}
	}
	if failing.calls != 2 {
		t.Fatalf("failed verifications cached: calls = %d, want 2", failing.calls)
	}
}

func TestRegisterStoredRuntimeSnapshotRemovesObjectsOnFailure(t *testing.T) {
	backend, err := storage.NewLocalStorageBackend(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocalStorageBackend: %v", err)
	}
	publisher := NewRuntimeSnapshotPublisher(state.NewMemStore(), backend)
	storageKey, vmstateKey := NewRuntimeSnapshotCaptureKey()
	if err := backend.Put(context.Background(), storageKey, strings.NewReader("short")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	if err := backend.Put(context.Background(), vmstateKey, strings.NewReader("state")); err != nil {
		t.Fatalf("Put: %v", err)
	}
	// The recorded size disagrees with the stored object: registration must
	// fail and remove both objects instead of cataloguing a bad pair.
	if _, err := publisher.RegisterStoredRuntimeSnapshot(context.Background(), captureTestIdentity(), storageKey, 999, 5, time.Time{}); !errors.Is(err, ErrRuntimeSnapshotCorrupt) {
		t.Fatalf("register error = %v, want ErrRuntimeSnapshotCorrupt", err)
	}
	for _, key := range []string{storageKey, vmstateKey} {
		if rc, err := backend.Get(context.Background(), key); err == nil {
			_ = rc.Close()
			t.Fatalf("object %s survived a failed registration", key)
		}
	}
}
