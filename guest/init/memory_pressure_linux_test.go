//go:build linux

package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/guestmemproto"
)

func meminfoMB(totalMB, availableMB int) guestmemproto.Stats {
	return guestmemproto.Stats{MemTotal: int64(totalMB) << 20, MemAvailable: int64(availableMB) << 20}
}

func TestMemoryPressureStateFiresOncePerEpisode(t *testing.T) {
	s := memoryPressureState{armed: true}
	steps := []struct {
		availableMB int
		want        bool
	}{
		{150, false}, {40, false}, {30, true}, {20, false}, // ≤15% fires once
		{60, false}, // 30% of 200 is the re-arm boundary; 60 re-arms
		{25, true},  // a new episode fires again
		{0, false},
	}
	for i, step := range steps {
		if got := s.observe(meminfoMB(200, step.availableMB)); got != step.want {
			t.Fatalf("step %d (%d MiB available): fired=%v, want %v", i, step.availableMB, got, step.want)
		}
	}
	if (&memoryPressureState{armed: true}).observe(guestmemproto.Stats{}) {
		t.Fatal("missing MemTotal fired")
	}
}

func TestWatchMemoryPressureReportsOnlyWithCollectors(t *testing.T) {
	old := memoryPressurePollInterval
	memoryPressurePollInterval = 10 * time.Millisecond
	defer func() { memoryPressurePollInterval = old }()
	path := filepath.Join(t.TempDir(), "meminfo")
	write := func(availableKB int) {
		if err := os.WriteFile(path, []byte(fmt.Sprintf("MemTotal: 204800 kB\nMemAvailable: %d kB\nAnonPages: 153600 kB\n", availableKB)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(20480) // 10% available
	var mu sync.Mutex
	active := false
	var reports []memoryPressureWire
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		defer close(done)
		WatchMemoryPressure(ctx, path, func() bool { mu.Lock(); defer mu.Unlock(); return active }, func(r memoryPressureWire) error {
			mu.Lock()
			reports = append(reports, r)
			mu.Unlock()
			return nil
		}, slog.Default())
	}()
	time.Sleep(60 * time.Millisecond)
	mu.Lock()
	if len(reports) != 0 {
		mu.Unlock()
		t.Fatal("reported without a registered collector")
	}
	mu.Unlock()
	// The uninstrumented episode was consumed; recover, then press again.
	write(102400)
	time.Sleep(40 * time.Millisecond)
	mu.Lock()
	active = true
	mu.Unlock()
	write(20480)
	time.Sleep(80 * time.Millisecond)
	cancel()
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(reports) != 1 || reports[0].AvailableMB != 20 || reports[0].TotalMB != 200 || reports[0].AnonMB != 150 {
		t.Fatalf("reports = %+v; want one 20/200 MiB report", reports)
	}
}

func TestProfileCollectorsActiveTracksRecentPolls(t *testing.T) {
	b := newProfileBridge(nil, nil)
	guestProfiles.Lock()
	previous := guestProfiles.bridge
	guestProfiles.bridge = b
	guestProfiles.Unlock()
	defer func() { guestProfiles.Lock(); guestProfiles.bridge = previous; guestProfiles.Unlock() }()
	if profileCollectorsActive() {
		t.Fatal("no collector polled yet")
	}
	b.mu.Lock()
	b.processes["9"] = time.Now()
	b.mu.Unlock()
	if !profileCollectorsActive() {
		t.Fatal("recent poll not counted")
	}
	b.mu.Lock()
	b.processes["9"] = time.Now().Add(-time.Minute)
	b.mu.Unlock()
	if profileCollectorsActive() {
		t.Fatal("stale collector counted")
	}
}
