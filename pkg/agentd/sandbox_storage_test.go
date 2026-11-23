// adr: 20261122132055 §8 — the sweep meters each account's paused sessions
// and the bytes they keep stored, once per UTC hour, and a session's view
// shows its stored bytes.
package agentd_test

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentd"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

func TestPausedSandboxStorageIsMeteredHourly(t *testing.T) {
	pool, store := openStore(t)
	ctx := context.Background()
	id := queuePersistent(t, store, time.Hour)
	fake := &fakeSandboxSchedd{scripts: map[string]string{}}
	sbx := newSandboxes(t, store, fake, agentd.SandboxConfig{Workspaces: &memStore{blob: map[string][]byte{}}})
	done := holdSandbox(t, sbx)
	waitFor(t, "ready", func() bool { return sandboxState(t, store, id).State == agentstore.SandboxReady })
	// A running session is not metered.
	if err := sbx.SweepOnce(ctx); err != nil {
		t.Fatal(err)
	}
	var rows int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM sandbox_storage_hours`).Scan(&rows); err != nil || rows != 0 {
		t.Fatalf("%d rows for a running session (%v)", rows, err)
	}
	if _, err := store.PauseSandbox(ctx, account, id); err != nil {
		t.Fatal(err)
	}
	<-done
	// The fake vmmd's capture is 2 GiB of memory and 40,000 bytes of vmstate.
	const captureBytes = 2<<30 + 40_000
	if sb := sandboxState(t, store, id); sb.StoredBytes != captureBytes {
		t.Fatalf("paused session stores %d bytes, want %d", sb.StoredBytes, captureBytes)
	}
	for range 2 {
		if err := sbx.SweepOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}
	hours, err := store.SandboxStorageHours(ctx, account, time.Now().Add(-2*time.Hour), time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(hours) != 1 || hours[0].PausedSessions != 1 || hours[0].CaptureBytes != captureBytes || hours[0].VolumeBytes != 0 ||
		!hours[0].Hour.Equal(time.Now().UTC().Truncate(time.Hour)) {
		t.Fatalf("metered hours = %+v", hours)
	}
}
