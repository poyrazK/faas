//go:build linux

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// resourceSlotEmitType is the guest event discriminator for one resource
// slot (ADR-20261121170000). It must match
// cmd/vmmd/framework_ready_recv.go::VsockFrameworkReadyHostTypeResourceSlot.
const resourceSlotEmitType byte = 0x0C

// resourceSlotWire mirrors the host-side resourceSlotWire. Memory is MiB
// of the app cgroup's memory.current; CPU comes from its cpu.stat. PeakAgeMS
// is how long before the send the memory peak was sampled, so vmmd can stamp
// the slot with its own clock instead of trusting the guest's.
type resourceSlotWire struct {
	Samples           int   `json:"samples"`
	MemStartMB        int   `json:"mem_start_mb"`
	MemPeakMB         int   `json:"mem_peak_mb"`
	MemEndMB          int   `json:"mem_end_mb"`
	MemLimitMB        int   `json:"mem_limit_mb,omitempty"`
	CPUUsec           int64 `json:"cpu_usec"`
	CPUPeakMillicores int   `json:"cpu_peak_millicores"`
	CPULimitMillis    int   `json:"cpu_limit_millicores,omitempty"`
	PeakAgeMS         int64 `json:"peak_age_ms"`
}

// resourceSlot folds one-second samples of the app cgroup.
type resourceSlot struct {
	wire    resourceSlotWire
	peakAt  time.Time
	lastCPU int64
	lastAt  time.Time
	primed  bool
}

// observe adds one sample. The first sample of the sampler only primes the
// CPU counter; every later one also yields a rate.
func (s *resourceSlot) observe(at time.Time, memBytes uint64, cpuUsec int64) {
	mb := int(memBytes >> 20)
	w := &s.wire
	if w.Samples == 0 {
		w.MemStartMB, w.MemPeakMB, s.peakAt = mb, mb, at
	}
	if mb > w.MemPeakMB {
		w.MemPeakMB, s.peakAt = mb, at
	}
	w.MemEndMB = mb
	w.Samples++
	if s.primed && cpuUsec >= s.lastCPU {
		delta := cpuUsec - s.lastCPU
		w.CPUUsec += delta
		if elapsed := at.Sub(s.lastAt).Microseconds(); elapsed > 0 {
			w.CPUPeakMillicores = max(w.CPUPeakMillicores, int(delta*1000/elapsed))
		}
	}
	s.lastCPU, s.lastAt, s.primed = cpuUsec, at, true
}

// take returns the slot to send and starts the next one; ok is false when
// the slot saw nothing worth sending (no sample, or an idle app).
func (s *resourceSlot) take(now time.Time) (resourceSlotWire, bool) {
	w := s.wire
	s.wire = resourceSlotWire{}
	if w.Samples == 0 {
		return w, false
	}
	if w.CPUUsec == 0 && w.MemPeakMB == w.MemStartMB && w.MemEndMB == w.MemStartMB {
		return w, false
	}
	w.PeakAgeMS = max(now.Sub(s.peakAt).Milliseconds(), 0)
	return w, true
}

// startResourceSlots samples leaf every second and sends a slot every
// RouteResourceSlotInterval for as long as the workload runs. It is
// best-effort: a read or send failure drops that sample or slot.
func startResourceSlots(leaf string, log *slog.Logger) (stop func()) {
	if leaf == "" {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runResourceSlots(ctx, leaf, api.RouteResourceSampleInterval, api.RouteResourceSlotInterval, time.Now, EmitResourceSlot, log)
	}()
	return func() {
		cancel()
		<-done
	}
}

func runResourceSlots(ctx context.Context, leaf string, sampleEvery, slotEvery time.Duration, now func() time.Time, emit func(resourceSlotWire) error, log *slog.Logger) {
	sample := time.NewTicker(sampleEvery)
	defer sample.Stop()
	slotEnd := now().Add(slotEvery)
	var slot resourceSlot
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-sample.C:
		}
		at := now()
		mem, merr := readCgroupUint(filepath.Join(leaf, "memory.current"))
		cpu, cerr := readCgroupCPUUsec(filepath.Join(leaf, "cpu.stat"))
		if merr == nil && cerr == nil {
			slot.observe(at, mem, cpu)
		}
		if at.Before(slotEnd) {
			continue
		}
		slotEnd = at.Add(slotEvery)
		w, ok := slot.take(at)
		if !ok {
			continue
		}
		w.MemLimitMB, w.CPULimitMillis = resourceSlotLimits(leaf)
		if err := emit(w); err != nil {
			failures++
			// One line per hundred failures: a guest whose host does not
			// listen must not flood the console.
			if failures%100 == 1 {
				log.Debug("resource slot not sent", "err", err, "failures", failures)
			}
		}
	}
}

// resourceSlotMeminfo is a variable so tests can point it at a fixture.
var resourceSlotMeminfo = "/proc/meminfo"

// resourceSlotLimits is the app's memory and CPU limit: the leaf's own
// memory.max and cpu.max when set (a partitioned VM with sidecars), else the
// VM itself, whose memory and vCPUs are the plan's (native acceptance
// 2026-11-21: a single-process app's leaf has neither).
func resourceSlotLimits(leaf string) (memMB, cpuMillicores int) {
	memMB = readCgroupMemoryLimitMB(filepath.Join(leaf, "memory.max"))
	if memMB == 0 {
		if stats, err := readMeminfo(resourceSlotMeminfo); err == nil {
			memMB = int(stats.MemTotal >> 20)
		}
	}
	cpuMillicores = readCgroupCPULimitMillicores(filepath.Join(leaf, "cpu.max"))
	if cpuMillicores == 0 {
		cpuMillicores = runtime.NumCPU() * 1000
	}
	return memMB, cpuMillicores
}

// EmitResourceSlot sends one slot over the guest event stream.
func EmitResourceSlot(w resourceSlotWire) error {
	body, err := json.Marshal(w)
	if err != nil {
		return fmt.Errorf("EmitResourceSlot: marshal: %w", err)
	}
	if len(body) > api.RouteResourceGuestFrameMaxBytes {
		return fmt.Errorf("EmitResourceSlot: body too large: %d", len(body))
	}
	return sendGuestEventFrame(append([]byte{resourceSlotEmitType}, body...), time.Second)
}

func readCgroupUint(path string) (uint64, error) {
	//nolint:forbidigo // fixed cgroup path inside the guest.
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	return strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
}

// readCgroupCPUUsec reads usage_usec from cpu.stat.
func readCgroupCPUUsec(path string) (int64, error) {
	//nolint:forbidigo // fixed cgroup path inside the guest.
	b, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "usage_usec "); ok {
			return strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		}
	}
	return 0, fmt.Errorf("%s: no usage_usec", path)
}

// readCgroupMemoryLimitMB is memory.max in MiB; 0 for "max" or unreadable.
func readCgroupMemoryLimitMB(path string) int {
	v, err := readCgroupUint(path)
	if err != nil {
		return 0
	}
	return int(v >> 20)
}

// readCgroupCPULimitMillicores is cpu.max as millicores; 0 for "max" or
// unreadable.
func readCgroupCPULimitMillicores(path string) int {
	//nolint:forbidigo // fixed cgroup path inside the guest.
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	fields := strings.Fields(string(b))
	if len(fields) != 2 || fields[0] == "max" {
		return 0
	}
	quota, qerr := strconv.ParseInt(fields[0], 10, 64)
	period, perr := strconv.ParseInt(fields[1], 10, 64)
	if qerr != nil || perr != nil || quota <= 0 || period <= 0 {
		return 0
	}
	return int(quota * 1000 / period)
}
