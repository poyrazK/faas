package fcvm

// spec: §6.3 — bound competing restore work so burst wakes remain within the
// platform snapshot-wake latency budget; gate wait stays inside that interval.

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRestoreConcurrencyBlocksUntilSlotReleased(t *testing.T) {
	v := (&JailerVMM{}).WithRestoreConcurrency(3)
	releases := make([]func(), 0, 3)
	for i := 0; i < 3; i++ {
		release, err := v.acquireRestoreSlot(context.Background())
		if err != nil {
			t.Fatalf("acquire slot %d: %v", i+1, err)
		}
		releases = append(releases, release)
	}

	acquired := make(chan func(), 1)
	go func() {
		release, err := v.acquireRestoreSlot(context.Background())
		if err != nil {
			return
		}
		acquired <- release
	}()

	select {
	case release := <-acquired:
		release()
		t.Fatal("fourth restore acquired while all three slots were occupied")
	case <-time.After(30 * time.Millisecond):
	}

	releases[0]()
	select {
	case release := <-acquired:
		release()
	case <-time.After(time.Second):
		t.Fatal("fourth restore did not acquire the released slot")
	}
	for _, release := range releases[1:] {
		release()
	}
}

func TestRestoreConcurrencyWaitHonorsContext(t *testing.T) {
	v := (&JailerVMM{}).WithRestoreConcurrency(1)
	release, err := v.acquireRestoreSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire first slot: %v", err)
	}
	defer release()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = v.acquireRestoreSlot(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("acquire with canceled context error = %v, want context.Canceled", err)
	}
}

func TestRestoreConcurrencyCanBeDisabled(t *testing.T) {
	v := (&JailerVMM{}).WithRestoreConcurrency(0)
	release, err := v.acquireRestoreSlot(context.Background())
	if err != nil {
		t.Fatalf("acquire disabled gate: %v", err)
	}
	release()
}
