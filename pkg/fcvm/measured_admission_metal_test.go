//go:build metal

// adr: 909 — on a real node, measured admission admits more 1 GiB VMs than
// reservations allow, and every admitted VM keeps serving through a burst.
package fcvm_test

import (
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalMeasuredAdmissionDensity boots 1 GiB app VMs (FAAS_TEST_LAYER_ROOTFS
// starting an app on :8080 that answers /heavy, /alloc?mb= and /free) while a
// sched.NodeLedger with measured admission, fed by the VMs' cgroup
// memory.current, admits them against FAAS_TEST_ADMISSION_CEILING_MB (default
// 8,593 MB, a 16 GB compute node). After a burst it checks pressure
// eviction: SelectMemoryEvictions, given the VMs' real memory and a ceiling
// just under it, brings the node back under 90% of that ceiling.
func TestMetalMeasuredAdmissionDensity(t *testing.T) {
	if os.Getenv("FAAS_TEST_MEASURED_ADMISSION") != "1" {
		t.Skip("set FAAS_TEST_MEASURED_ADMISSION=1 (boots up to 16 1 GiB VMs)")
	}
	kernel, base, layer := fcvm.MetalImagesForTest(t)
	ceiling := 8593
	if raw := os.Getenv("FAAS_TEST_ADMISSION_CEILING_MB"); raw != "" {
		var err error
		if ceiling, err = strconv.Atoi(raw); err != nil {
			t.Fatalf("FAAS_TEST_ADMISSION_CEILING_MB=%q", raw)
		}
	}
	// The node is shared: stop booting before it gets this low.
	const minAvailableMB = 2048
	ctx := context.Background()
	fcVersion, err := fcvm.DetectFirecrackerVersion(ctx)
	if err != nil {
		t.Skipf("firecracker version: %v", err)
	}
	fcvm.WithCgroupRootAtForTest(t, "/sys/fs/cgroup")
	m := fcvm.NewManager(wire.ExecRunner{}, fcvm.NewMetalVMMForTest(t, 180*time.Second), fcvm.Paths{Kernel: kernel}, fcVersion, nil, nil)

	var (
		mu       sync.Mutex
		ids      []string
		sampleMB int
		sampleAt time.Time
	)
	measure := func() int {
		at := time.Now()
		total := 0
		for _, id := range ids {
			rss := math.NaN()
			if cur, err := fcvm.VMCgroupIntForTest(api.PlanScale, id, "memory.current"); err == nil {
				rss = float64(cur) / (1 << 20)
			}
			total += api.MeasuredChargeMB(1024, rss)
		}
		mu.Lock()
		sampleMB, sampleAt = total, at
		mu.Unlock()
		return total
	}
	ledger := sched.NewNodeLedger()
	ledger.EnableMeasuredAdmission(func(string) (int, time.Time, bool) {
		mu.Lock()
		defer mu.Unlock()
		return sampleMB, sampleAt, !sampleAt.IsZero()
	})

	before := fcvm.MemAvailableMBForTest(t)
	hosts := map[string]string{}
	started := map[string]time.Time{}
	var refusal error
	for i := 0; i < 40; i++ {
		measure()
		id := fmt.Sprintf("madm-%d", i)
		req := sched.Request{Instance: id, AppID: "app-" + id, Plan: api.PlanScale, RAMMB: 1024, VCPU: 2,
			MaxConcurrency: 1, NodeID: "metal", NodeCeilingMB: ceiling}
		if refusal = ledger.Admit(req); refusal != nil {
			break
		}
		if avail := fcvm.MemAvailableMBForTest(t); avail < minAvailableMB {
			ledger.Release(id)
			t.Logf("stopping at %d VMs: host MemAvailable %d MB", len(ids), avail)
			break
		}
		inst, err := m.Wake(ctx, fcvm.WakeRequest{Instance: id, AccountID: "acct", AppID: req.AppID, Plan: "scale",
			BaseKey: base, LayerKey: layer, VcpuCount: 2, MemSizeMiB: 1024})
		if err != nil {
			t.Fatalf("wake %d: %v", i, err)
		}
		t.Cleanup(func() { _ = m.Destroy(context.Background(), id) })
		started[id] = time.Now()
		h := "http://" + inst.Lease.HostIP.String() + ":8080"
		if _, err := fcvm.FirstResponseForTest(h+"/", 120*time.Second); err != nil {
			t.Fatalf("%s does not serve: %v", id, err)
		}
		mustGet(t, h+"/heavy")
		ids = append(ids, id)
		hosts[id] = h
		// Let the guest settle so the next sample sees its working set.
		time.Sleep(2 * time.Second)
	}
	reservationsAllow := ceiling / api.BillableRAMMB(1024)
	t.Logf("measured admission: %d VMs on a %d MB ceiling (reservations allow %d); reservations %d MB, measured %d MB; "+
		"host MemAvailable %d -> %d MB, memory PSI %s; refusal: %v",
		len(ids), ceiling, reservationsAllow, ledger.ResidentRAMForNode("metal"), measure(),
		before, fcvm.MemAvailableMBForTest(t), memoryPSI(t), refusal)
	if len(ids) <= reservationsAllow {
		t.Fatalf("admitted %d VMs, want more than the %d reservations allow", len(ids), reservationsAllow)
	}

	// Burst: as many VMs as the shared host can spare each allocate 320 MB,
	// and every VM keeps serving.
	burst := int((fcvm.MemAvailableMBForTest(t) - minAvailableMB) / 320)
	burst = min(burst, len(ids))
	if burst < 1 {
		t.Logf("skipping the burst: host MemAvailable %d MB", fcvm.MemAvailableMBForTest(t))
		return
	}
	for _, id := range ids[:burst] {
		mustGet(t, hosts[id]+"/alloc?mb=320")
	}
	time.Sleep(2 * time.Second)
	start := time.Now()
	lastRequest := map[string]time.Time{}
	for _, id := range ids {
		mustGet(t, hosts[id]+"/")
		lastRequest[id] = time.Now()
	}
	t.Logf("burst: %d VMs +320 MB, measured %d MB, host MemAvailable %d MB, memory PSI %s, all %d served in %v",
		burst, measure(), fcvm.MemAvailableMBForTest(t), memoryPSI(t), len(ids), time.Since(start).Round(time.Millisecond))
	bursted := map[string]bool{}
	for _, id := range ids[:burst] {
		bursted[id] = true
	}

	// Pressure eviction, with a ceiling just under the node's real use so
	// the shared host never nears its limit.
	used, resident := vmMemory(t, ids)
	evictCeiling := used - 200
	infos := make([]sched.InstanceInfo, 0, len(ids))
	for _, id := range ids {
		infos = append(infos, sched.InstanceInfo{Instance: id, AppID: "app-" + id, Plan: api.PlanScale, State: state.StateRunning,
			RAMMB: 1024, NodeID: "metal", Started: started[id], LastRequest: lastRequest[id]})
	}
	evict := sched.SelectMemoryEvictions(time.Now(), infos, map[string]int{"metal": used},
		map[string]int{"metal": evictCeiling}, resident)
	gone := map[string]bool{}
	for _, id := range evict {
		if err := m.Destroy(ctx, id); err != nil {
			t.Fatalf("evict %s: %v", id, err)
		}
		gone[id] = true
	}
	var kept []string
	for _, id := range ids {
		if !gone[id] {
			kept = append(kept, id)
		}
	}
	time.Sleep(2 * time.Second)
	after, _ := vmMemory(t, kept)
	target := evictCeiling * api.MeasuredEvictionTargetPercent / 100
	t.Logf("eviction: node at %d MB over a %d MB ceiling; evicted %d VMs %v; node now %d MB (target %d), host MemAvailable %d MB",
		used, evictCeiling, len(evict), evict, after, target, fcvm.MemAvailableMBForTest(t))
	if len(evict) == 0 || after > target+64 {
		t.Fatalf("eviction left the node at %d MB, want at most %d", after, target)
	}
	for _, id := range kept {
		mustGet(t, hosts[id]+"/")
		if bursted[id] {
			mustGet(t, hosts[id]+"/free")
		}
	}
}

// vmMemory is the VMs' cgroup memory.current, in total and per VM, in MB.
func vmMemory(t *testing.T, ids []string) (int, map[string]int) {
	t.Helper()
	total, per := 0, make(map[string]int, len(ids))
	for _, id := range ids {
		cur, err := fcvm.VMCgroupIntForTest(api.PlanScale, id, "memory.current")
		if err != nil {
			t.Fatalf("%s memory.current: %v", id, err)
		}
		per[id] = int(cur >> 20)
		total += per[id]
	}
	return total, per
}

func mustGet(t *testing.T, url string) {
	t.Helper()
	resp, err := (&http.Client{Timeout: 60 * time.Second}).Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, resp.Status)
	}
}

// memoryPSI is the "some" line of /proc/pressure/memory.
func memoryPSI(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile("/proc/pressure/memory")
	if err != nil {
		return "unavailable"
	}
	line, _, _ := strings.Cut(string(raw), "\n")
	return strings.TrimPrefix(line, "some ")
}
