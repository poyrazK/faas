package vmmdgrpc

// Node CPU pressure readings for the CPU pressure backstop
// (ADR 20261030100950). vmmd reports two numbers with every Stats call and
// capacity report; schedd decides what they mean.
//
//   - tenant_cpu_pressure_some_avg10: the "some avg10" value of
//     faas-tenant.slice's cpu.pressure, the share of the last 10 s in which
//     a tenant task waited for a CPU.
//   - host_cpu_busy_percent: the host's busy share of all CPUs over the last
//     api.NodeCPUBusyWindowSeconds, from /proc/stat.

import (
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/wrapperspb"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// tenantCPUPressurePath is faas-tenant.slice's cpu.pressure, and procStatPath
// the kernel's CPU counters. Variables so tests can point them at temporary
// files.
var (
	tenantCPUPressurePath = "/sys/fs/cgroup/faas.slice/faas-tenant.slice/cpu.pressure"
	procStatPath          = "/proc/stat"
)

// tenantCPUPressureSomeAvg10 reads the tenant slice's "some avg10" CPU
// pressure, in percent. ok is false when the file is missing or malformed.
func tenantCPUPressureSomeAvg10() (float64, bool) {
	raw, err := os.ReadFile(tenantCPUPressurePath)
	if err != nil {
		return 0, false
	}
	return parsePressureSomeAvg10(string(raw))
}

// parsePressureSomeAvg10 extracts avg10 from a PSI file's "some" line:
//
//	some avg10=1.23 avg60=0.50 avg300=0.10 total=123456
func parsePressureSomeAvg10(text string) (float64, bool) {
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "some" {
			continue
		}
		for _, f := range fields[1:] {
			if v, found := strings.CutPrefix(f, "avg10="); found {
				pct, err := strconv.ParseFloat(v, 64)
				return pct, err == nil && pct >= 0
			}
		}
	}
	return 0, false
}

// cpuTimes is one /proc/stat reading of the aggregate "cpu" line, in clock
// ticks.
type cpuTimes struct {
	at          time.Time
	busy, total uint64
}

// readCPUTimes reads the aggregate "cpu" line:
//
//	cpu  user nice system idle iowait irq softirq steal guest guest_nice
//
// Busy is everything but idle and iowait. guest and guest_nice are already
// counted in user and nice, so they are left out of the total.
func readCPUTimes(at time.Time) (cpuTimes, bool) {
	raw, err := os.ReadFile(procStatPath)
	if err != nil {
		return cpuTimes{}, false
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	fields := strings.Fields(line)
	if len(fields) < 9 || fields[0] != "cpu" {
		return cpuTimes{}, false
	}
	var v [8]uint64
	for i := range v {
		n, err := strconv.ParseUint(fields[i+1], 10, 64)
		if err != nil {
			return cpuTimes{}, false
		}
		v[i] = n
	}
	t := cpuTimes{at: at}
	for _, n := range v {
		t.total += n
	}
	t.busy = t.total - v[3] - v[4]
	return t, true
}

// hostCPUSampler keeps the last api.NodeCPUBusyWindowSeconds of /proc/stat
// readings, at most one a second, so every caller of busyPercent gets the
// same window however often it calls. The zero value is ready to use.
type hostCPUSampler struct {
	mu      sync.Mutex
	samples []cpuTimes
}

// busyPercent records a reading taken at now and returns the busy share of
// all CPUs since the oldest kept reading. ok is false until the readings span
// half the window, and when /proc/stat cannot be read.
func (h *hostCPUSampler) busyPercent(now time.Time) (float64, bool) {
	cur, ok := readCPUTimes(now)
	if !ok {
		return 0, false
	}
	window := time.Duration(api.NodeCPUBusyWindowSeconds) * time.Second
	h.mu.Lock()
	defer h.mu.Unlock()
	if n := len(h.samples); n == 0 || now.Sub(h.samples[n-1].at) >= time.Second {
		h.samples = append(h.samples, cur)
	}
	drop := 0
	for drop < len(h.samples)-1 && now.Sub(h.samples[drop].at) > window {
		drop++
	}
	h.samples = h.samples[drop:]
	oldest := h.samples[0]
	if now.Sub(oldest.at) < window/2 || cur.total <= oldest.total || cur.busy < oldest.busy {
		return 0, false
	}
	return 100 * float64(cur.busy-oldest.busy) / float64(cur.total-oldest.total), true
}

// setCPUPressureReadings sets the readings a host provides on resp.
func (s *Server) setCPUPressureReadings(resp *vmmdpb.StatsResponse, now time.Time) {
	if pct, ok := tenantCPUPressureSomeAvg10(); ok {
		resp.TenantCpuPressureSomeAvg10 = wrapperspb.Double(pct)
	}
	if pct, ok := s.hostCPU.busyPercent(now); ok {
		resp.HostCpuBusyPercent = wrapperspb.Double(pct)
	}
}
