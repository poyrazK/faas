//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/guestmemproto"
)

// memoryPressureEmitType is the guest event discriminator for a memory
// pressure report (ADR-1155). It must match
// cmd/vmmd/framework_ready_recv.go::VsockFrameworkReadyHostTypeMemoryPressure.
const memoryPressureEmitType byte = 0x0A

// memoryPressurePollInterval is a variable so tests can sample quickly.
var memoryPressurePollInterval = api.ProfileMemoryPressurePollInterval

// memoryPressureWire mirrors the host-side memoryPressureWire envelope.
type memoryPressureWire struct {
	AvailableMB int `json:"available_mb"`
	TotalMB     int `json:"total_mb"`
	AnonMB      int `json:"anon_mb"`
}

// memoryPressureState decides when a sample is a new pressure episode:
// MemAvailable at or below the threshold share of MemTotal fires once, and
// another report needs a recovery above the re-arm share first.
type memoryPressureState struct {
	armed bool
}

func (s *memoryPressureState) observe(stats guestmemproto.Stats) bool {
	if stats.MemTotal <= 0 {
		return false
	}
	availablePct := 100 * stats.MemAvailable / stats.MemTotal
	if !s.armed {
		if availablePct >= api.ProfileMemoryPressureRearmAvailablePercent {
			s.armed = true
		}
		return false
	}
	if availablePct <= api.ProfileMemoryPressureAvailablePercent {
		s.armed = false
		return true
	}
	return false
}

// WatchMemoryPressure samples /proc/meminfo while the main workload runs
// and reports pressure episodes so the control plane can capture the heap
// before an OOM kill (ADR-1155). It only reports while a profile collector
// is registered: without one a capture has nothing to collect.
func WatchMemoryPressure(ctx context.Context, meminfo string, collectorsActive func() bool, emit func(memoryPressureWire) error, log *slog.Logger) {
	state := memoryPressureState{armed: true}
	ticker := time.NewTicker(memoryPressurePollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		stats, err := readMeminfo(meminfo)
		if err != nil || !state.observe(stats) || !collectorsActive() {
			continue
		}
		report := memoryPressureWire{AvailableMB: int(stats.MemAvailable >> 20), TotalMB: int(stats.MemTotal >> 20), AnonMB: int(stats.AnonPages >> 20)}
		if err := emit(report); err != nil {
			log.Warn("memory pressure report failed", "err", err)
		}
	}
}

func readMeminfo(path string) (guestmemproto.Stats, error) {
	//nolint:forbidigo // fixed procfs path inside the guest.
	f, err := os.Open(path)
	if err != nil {
		return guestmemproto.Stats{}, err
	}
	defer func() { _ = f.Close() }()
	return guestmemproto.Parse(f)
}

// EmitMemoryPressure sends one best-effort report over the guest event stream.
func EmitMemoryPressure(report memoryPressureWire) error {
	body, err := json.Marshal(report)
	if err != nil {
		return fmt.Errorf("EmitMemoryPressure: marshal: %w", err)
	}
	return sendGuestEventFrame(append([]byte{memoryPressureEmitType}, body...), time.Second)
}

// profileCollectorsActive reports whether any collector polled the local
// profile bridge recently. Dormant collectors poll once per second; the pprof
// scraper counts once its endpoint has answered.
func profileCollectorsActive() bool {
	b := activeProfileBridge()
	if b == nil {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if time.Since(b.pprofSeen) < api.ProfileProcessStaleAfter {
		return true
	}
	for _, seen := range b.processes {
		if time.Since(seen) < api.ProfileProcessStaleAfter {
			return true
		}
	}
	return false
}
