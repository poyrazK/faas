// adr: 733
package imaged

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/storage"
)

type crashCryptoFixture struct {
	*gcFixture
	capture state.CrashCapture
	appID   string
	acctID  string
	mem     []byte
	clock   time.Time
}

// newCrashCryptoFixture seeds a ready capture whose memory, vmstate and
// private drive exist on a local backend (the backing identity is absent,
// as it is optional).
func newCrashCryptoFixture(t *testing.T, withKey bool) *crashCryptoFixture {
	t.Helper()
	ctx := context.Background()
	fx := &crashCryptoFixture{gcFixture: newGCFixture(t, 50), clock: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	fx.loop.now = func() time.Time { return fx.clock }
	if withKey {
		id, err := age.GenerateX25519Identity()
		if err != nil {
			t.Fatal(err)
		}
		fx.loop.handler.secretboxIdentities = []*age.X25519Identity{id}
	}
	appID, depID, _ := seedSnapshotWithApp(t, fx.store, 100, 100)
	app, err := fx.store.AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	fx.appID, fx.acctID = appID, app.AccountID
	if _, err := fx.store.CreateInstanceWithMode(ctx, appID, depID, string(state.StateRunning), 256, "", "wake-1", string(state.InstanceModeNormal)); err != nil {
		t.Fatal(err)
	}
	requested, err := fx.store.RequestManualCrashCapture(ctx, fx.acctID, appID, time.Minute, fx.clock)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fx.store.ClaimNextCrashCapture(ctx, fx.clock); err != nil {
		t.Fatal(err)
	}
	mem := state.SnapshotCaptureMemKey(depID, state.SnapshotTierWarm, requested.ID)
	vmstate := state.SnapshotVMStateKey(state.Snapshot{StorageKey: mem})
	fx.capture, err = fx.store.CompleteCrashCapture(ctx, state.CompleteCrashCaptureParams{
		ID: requested.ID, StorageKey: mem, VMStateStorageKey: vmstate, FCVersion: "1.7.0",
		MemBytes: 1 << 20, CapturedAt: fx.clock, ExpiresAt: fx.clock.Add(time.Hour),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Mostly zero pages plus a recognisable secret, like guest memory.
	fx.mem = append(make([]byte, 1<<20), []byte("customer-session-token-4242")...)
	keys := crashCaptureKeys(fx.capture)
	for key, body := range map[string][]byte{keys[0]: fx.mem, keys[1]: []byte("vmstate"), keys[2]: []byte("drive1")} {
		if err := fx.be.Put(ctx, key, bytes.NewReader(body)); err != nil {
			t.Fatal(err)
		}
	}
	return fx
}

func (fx *crashCryptoFixture) tend(t *testing.T, advance time.Duration) state.CrashCapture {
	t.Helper()
	fx.clock = fx.clock.Add(advance)
	fx.loop.tendCrashCaptures(context.Background(), fx.clock)
	c, err := fx.store.CrashCaptureForRestore(context.Background(), fx.capture.ID)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (fx *crashCryptoFixture) read(t *testing.T, key string) ([]byte, bool) {
	t.Helper()
	r, err := fx.be.Get(context.Background(), key)
	if errors.Is(err, storage.ErrNotFound) {
		return nil, false
	}
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = r.Close() }()
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return body, true
}

func TestCrashCaptureEncryptPurgeStageAndExpire(t *testing.T) {
	ctx := context.Background()
	fx := newCrashCryptoFixture(t, true)
	fx.loop.crashMetrics = newCrashCaptureMetrics(prometheus.NewRegistry())
	keys := crashCaptureKeys(fx.capture)

	c := fx.tend(t, time.Second)
	if n := testutil.ToFloat64(fx.loop.crashMetrics.ops.WithLabelValues("encrypt", "ok")); n != 1 {
		t.Fatalf("encrypt ok = %v, want 1", n)
	}
	if c.PlaintextState != state.CrashPlaintextAbsent || len(c.SealedKey) == 0 || c.EncryptedAt == nil {
		t.Fatalf("after encrypt = %+v, want absent with a sealed key", c)
	}
	for _, key := range keys {
		if _, ok := fx.read(t, key); ok {
			t.Errorf("plaintext %s survived encryption", key)
		}
	}
	sealedMem, ok := fx.read(t, keys[0]+crashCaptureEncryptedSuffix)
	if !ok || bytes.Contains(sealedMem, []byte("customer-session-token")) || len(sealedMem) > 64<<10 {
		t.Fatalf("encrypted memory: present=%v size=%d (want small, no plaintext)", ok, len(sealedMem))
	}
	if _, ok := fx.read(t, keys[3]+crashCaptureEncryptedSuffix); ok {
		t.Error("an absent optional object got an encrypted twin")
	}

	fork, err := fx.store.CreateAppFork(ctx, state.CreateAppForkParams{
		AccountID: fx.acctID, AppID: fx.appID, RequestedBy: "user:test", TTLSeconds: 600,
		MaxPerApp: 1, MaxPerAccount: 2, CreatedAt: fx.clock, CrashCaptureID: fx.capture.ID,
	})
	if err != nil {
		t.Fatal(err)
	}
	if c = fx.tend(t, time.Second); c.PlaintextState != state.CrashPlaintextStaged {
		t.Fatalf("with an active fork = %s, want staged", c.PlaintextState)
	}
	if mem, ok := fx.read(t, keys[0]); !ok || !bytes.Equal(mem, fx.mem) {
		t.Fatalf("staged memory differs from the capture (present=%v)", ok)
	}
	if drive, _ := fx.read(t, keys[2]); string(drive) != "drive1" {
		t.Fatalf("staged drive = %q", drive)
	}
	claimed, err := fx.store.ClaimNextAppFork(ctx, "sched", fx.clock, time.Minute)
	if err != nil || claimed.ID != fork.ID {
		t.Fatalf("claim = %+v, %v", claimed, err)
	}
	if c = fx.tend(t, time.Second); c.PlaintextState != state.CrashPlaintextStaged {
		t.Fatalf("purged under a restoring fork: %s", c.PlaintextState)
	}
	if _, err := fx.store.FinishAppFork(ctx, state.FinishAppForkParams{
		ForkID: fork.ID, LeaseToken: *claimed.LeaseToken, Status: state.AppForkCancelled, FinishedAt: fx.clock,
	}); err != nil {
		t.Fatal(err)
	}
	if c = fx.tend(t, time.Second); c.PlaintextState != state.CrashPlaintextAbsent {
		t.Fatalf("after the fork = %s, want absent", c.PlaintextState)
	}
	if _, ok := fx.read(t, keys[0]); ok {
		t.Fatal("plaintext memory outlived the fork")
	}

	if c = fx.tend(t, 2*time.Hour); c.Status != state.CrashCaptureExpired || c.SealedKey != nil {
		t.Fatalf("after expiry = %+v", c)
	}
	for _, op := range []string{"purge", "stage", "expire"} {
		if n := testutil.ToFloat64(fx.loop.crashMetrics.ops.WithLabelValues(op, "ok")); n < 1 {
			t.Errorf("%s ok = %v, want at least 1", op, n)
		}
	}
	if age := testutil.ToFloat64(fx.loop.crashMetrics.unencryptedOldest); age != 0 {
		t.Errorf("unencrypted oldest age after encryption = %v, want 0", age)
	}
	for _, key := range keys {
		for _, k := range []string{key, key + crashCaptureEncryptedSuffix} {
			if _, ok := fx.read(t, k); ok {
				t.Errorf("%s survived expiry", k)
			}
		}
	}
}

func TestCrashCaptureWithoutKeyStaysPlaintext(t *testing.T) {
	fx := newCrashCryptoFixture(t, false)
	fx.loop.crashMetrics = newCrashCaptureMetrics(prometheus.NewRegistry())
	if c := fx.tend(t, time.Second); c.PlaintextState != state.CrashPlaintextPresent || c.SealedKey != nil {
		t.Fatalf("without a key = %+v, want present and unsealed", c)
	}
	if missing := testutil.ToFloat64(fx.loop.crashMetrics.keyMissing); missing != 1 {
		t.Fatalf("key missing gauge = %v, want 1", missing)
	}
	if age := testutil.ToFloat64(fx.loop.crashMetrics.unencryptedOldest); age != 1 {
		t.Fatalf("unencrypted oldest age = %v, want 1", age)
	}
	if mem, ok := fx.read(t, crashCaptureKeys(fx.capture)[0]); !ok || !bytes.Equal(mem, fx.mem) {
		t.Fatal("plaintext was lost without a key to encrypt it")
	}
}

func TestCrashCaptureSealedKeyIsBoundToItsCapture(t *testing.T) {
	fx := newCrashCryptoFixture(t, true)
	c := fx.tend(t, time.Second)
	other := c
	other.ID = "00000000-0000-0000-0000-000000000000"
	if err := decryptCrashCapture(context.Background(), fx.be, fx.loop.handler.secretboxIdentities, other); err == nil {
		t.Fatal("a sealed key opened for another capture")
	}
}
