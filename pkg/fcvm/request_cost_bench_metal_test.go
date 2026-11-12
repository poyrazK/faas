//go:build metal

// adr: 20261101160231

package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/wire"
)

// TestMetalRequestCostBench measures what one HTTP request costs the host
// once the guest is up: CPU per Firecracker thread group (vCPU threads, the
// VMM event loop, the rest), KVM exits per request by kind, and whole-host
// CPU. Production measured ~3.3 ms of VM CPU per request for hello-world apps
// on nested virtualization (2026-10-31); this attributes it. The guest is the
// metal fixture's, published on the host the way a wake publishes it (DNAT
// to the lease's host IP), so the path is tap, virtio-net and the guest stack
// without the platform's gateway and bridge.
//
// Opt-in: FAAS_REQUEST_COST_BENCH_REQUESTS=N (per mode). Unset, it skips.
// FAAS_REQUEST_COST_BENCH_VCPUS (default 1), FAAS_REQUEST_COST_BENCH_MEM_MIB
// (default 128) and FAAS_REQUEST_COST_BENCH_JSON (a path for the results) are
// optional. KVM exit counts need debugfs
// (/sys/kernel/debug/kvm); without it they are reported as missing.
func TestMetalRequestCostBench(t *testing.T) {
	requests, _ := strconv.Atoi(os.Getenv("FAAS_REQUEST_COST_BENCH_REQUESTS"))
	if requests <= 0 {
		t.Skip("set FAAS_REQUEST_COST_BENCH_REQUESTS to run the request cost bench")
	}
	vcpus := 1
	if v, err := strconv.Atoi(os.Getenv("FAAS_REQUEST_COST_BENCH_VCPUS")); err == nil && v > 0 {
		vcpus = v
	}
	kernel, base, layer := metalImages(t)
	vmm := newMetalVMM(t, 30*time.Second)
	m := NewManager(wire.ExecRunner{}, vmm, Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), nil, nil)
	withCgroupRootAt(t, "/sys/fs/cgroup")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	booted := time.Now()
	inst, err := m.ColdBoot(ctx, ColdBootRequest{Instance: "reqcost", Plan: "hobby", BaseKey: base, LayerKey: layer, VcpuCount: vcpus, MemSizeMiB: requestCostMemMiB(t)})
	if err != nil {
		t.Fatalf("cold boot: %v", err)
	}
	coldBootMS := time.Since(booted).Milliseconds()
	t.Cleanup(func() { _ = m.Destroy(context.Background(), "reqcost") })
	pid, ok := vmm.InstancePID("reqcost")
	if !ok {
		t.Fatal("no firecracker pid for the booted instance")
	}
	url := "http://" + inst.Lease.HostIP.String() + ":8080/"

	var results []requestCostResult
	for _, mode := range []struct {
		name      string
		keepAlive bool
	}{{"keep_alive", true}, {"new_connection", false}} {
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DisableKeepAlives: !mode.keepAlive, MaxIdleConnsPerHost: 1}}
		get := func() error {
			resp, err := client.Get(url)
			if err != nil {
				return err
			}
			_, _ = io.Copy(io.Discard, resp.Body)
			return resp.Body.Close()
		}
		for range 100 { // warm the guest, the connection and the host caches
			if err := get(); err != nil {
				t.Fatalf("%s warm-up: %v", mode.name, err)
			}
		}
		before := sampleRequestCost(t, pid)
		started := time.Now()
		for i := range requests {
			if err := get(); err != nil {
				t.Fatalf("%s request %d: %v", mode.name, i, err)
			}
		}
		elapsed := time.Since(started)
		after := sampleRequestCost(t, pid)
		result := after.perRequest(before, requests)
		result.Mode, result.VCPUs, result.Requests, result.MeanLatencyUS = mode.name, vcpus, requests, float64(elapsed.Microseconds())/float64(requests)
		result.ColdBootMS = coldBootMS
		results = append(results, result)
		client.CloseIdleConnections()
		t.Logf("%s", result)
	}
	if path := os.Getenv("FAAS_REQUEST_COST_BENCH_JSON"); path != "" {
		raw, _ := json.MarshalIndent(results, "", "  ")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Errorf("write %s: %v", path, err)
		}
	}
	if err := m.Destroy(ctx, "reqcost"); err != nil {
		t.Fatalf("destroy: %v", err)
	}
	leakcheck.AssertZero(t)
}

func requestCostMemMiB(t *testing.T) int {
	t.Helper()
	raw := os.Getenv("FAAS_REQUEST_COST_BENCH_MEM_MIB")
	if raw == "" {
		return 128
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 64 || n > 4096 {
		t.Fatalf("FAAS_REQUEST_COST_BENCH_MEM_MIB=%q must be an integer in [64,4096]", raw)
	}
	return n
}

type requestCostSample struct {
	threadTicks map[string]uint64 // per thread group and user/sys, in clock ticks
	hostBusy    uint64            // busy jiffies across all CPUs
	kvm         map[string]uint64 // debugfs counters for this VM
}

type requestCostResult struct {
	Mode          string             `json:"mode"`
	VCPUs         int                `json:"vcpus"`
	Requests      int                `json:"requests"`
	MeanLatencyUS float64            `json:"mean_latency_us"`
	ColdBootMS    int64              `json:"cold_boot_ms"`
	ThreadCPUUS   map[string]float64 `json:"thread_cpu_us_per_request"`
	HostCPUUS     float64            `json:"host_cpu_us_per_request"`
	KVMPerRequest map[string]float64 `json:"kvm_per_request,omitempty"`
}

func (r requestCostResult) String() string {
	threads := make([]string, 0, len(r.ThreadCPUUS))
	for name, us := range r.ThreadCPUUS {
		threads = append(threads, fmt.Sprintf("%s=%.0fµs", name, us))
	}
	sort.Strings(threads)
	kvm := make([]string, 0, len(r.KVMPerRequest))
	for name, n := range r.KVMPerRequest {
		if n >= 0.05 {
			kvm = append(kvm, fmt.Sprintf("%s=%.2f", name, n))
		}
	}
	sort.Strings(kvm)
	return fmt.Sprintf("%s vcpus=%d n=%d cold_boot=%dms latency=%.0fµs host=%.0fµs threads[%s] kvm[%s]",
		r.Mode, r.VCPUs, r.Requests, r.ColdBootMS, r.MeanLatencyUS, r.HostCPUUS, strings.Join(threads, " "), strings.Join(kvm, " "))
}

// clockTicksPerSecond is USER_HZ, 100 on every Linux the metal hosts run.
const clockTicksPerSecond = 100

func (after requestCostSample) perRequest(before requestCostSample, requests int) requestCostResult {
	usPerTick := 1e6 / clockTicksPerSecond / float64(requests)
	out := requestCostResult{ThreadCPUUS: make(map[string]float64), KVMPerRequest: make(map[string]float64)}
	for group, ticks := range after.threadTicks {
		out.ThreadCPUUS[group] = float64(ticks-before.threadTicks[group]) * usPerTick
	}
	out.HostCPUUS = float64(after.hostBusy-before.hostBusy) * usPerTick
	for name, n := range after.kvm {
		out.KVMPerRequest[name] = float64(n-before.kvm[name]) / float64(requests)
	}
	return out
}

func sampleRequestCost(t *testing.T, pid int) requestCostSample {
	t.Helper()
	sample := requestCostSample{threadTicks: make(map[string]uint64), kvm: make(map[string]uint64)}
	tasks, err := os.ReadDir(fmt.Sprintf("/proc/%d/task", pid))
	if err != nil {
		t.Fatalf("list firecracker threads: %v", err)
	}
	for _, task := range tasks {
		dir := fmt.Sprintf("/proc/%d/task/%s", pid, task.Name())
		comm, _ := os.ReadFile(dir + "/comm")
		stat, err := os.ReadFile(dir + "/stat")
		if err != nil {
			continue // the thread exited
		}
		// utime and stime are fields 14 and 15; comm (field 2) can hold
		// spaces, so split after its closing parenthesis.
		fields := strings.Fields(string(stat[strings.LastIndexByte(string(stat), ')')+1:]))
		if len(fields) < 13 {
			continue
		}
		// A vCPU thread's user time includes the time the guest ran; its
		// system time is KVM handling exits in the host kernel.
		utime, _ := strconv.ParseUint(fields[11], 10, 64)
		stime, _ := strconv.ParseUint(fields[12], 10, 64)
		group := threadGroup(strings.TrimSpace(string(comm)))
		sample.threadTicks[group+"/user"] += utime
		sample.threadTicks[group+"/sys"] += stime
	}
	if raw, err := os.ReadFile("/proc/stat"); err == nil {
		fields := strings.Fields(strings.SplitN(string(raw), "\n", 2)[0])
		// user, nice, system, irq and softirq. Guest time is already inside
		// user and nice, and steal is time the hypervisor below took.
		for _, i := range []int{1, 2, 3, 6, 7} {
			if i < len(fields) {
				v, _ := strconv.ParseUint(fields[i], 10, 64)
				sample.hostBusy += v
			}
		}
	}
	// KVM debugfs names each VM's directory "<pid>-<fd>".
	dirs, _ := filepath.Glob(fmt.Sprintf("/sys/kernel/debug/kvm/%d-*", pid))
	for _, dir := range dirs {
		files, _ := os.ReadDir(dir)
		for _, file := range files {
			if file.IsDir() {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, file.Name()))
			if err != nil {
				continue
			}
			if v, err := strconv.ParseUint(strings.TrimSpace(string(raw)), 10, 64); err == nil {
				sample.kvm[file.Name()] += v
			}
		}
	}
	return sample
}

// threadGroup folds Firecracker's thread names: fc_vcpu N, fc_vmm (the event
// loop that emulates virtio devices) and the rest (API server, signal).
func threadGroup(comm string) string {
	switch {
	case strings.HasPrefix(comm, "fc_vcpu"):
		return "vcpu"
	case comm == "fc_vmm":
		return "vmm"
	default:
		return "other:" + comm
	}
}
