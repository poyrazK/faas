// adr: 910 — a VM's writable app layer gets its plan's block rate limiter;
// shared read-only drives and builders stay unlimited.
package fcvm

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestWithBlockRateLimit(t *testing.T) {
	legacy := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "base.ext4", LayerKey: "layer.ext4", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0"}, 1)
	sidecars := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "base.ext4", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0",
		Workloads: []WorkloadSpec{{StorageKey: "main.ext4"}, {StorageKey: "side.ext4"}}}, 1)
	for name, tc := range map[string]struct {
		lease   Lease
		cfg     VMConfig
		limited string // drive id that must carry the limiter; "" for none
	}{
		"free app":          {Lease{Plan: api.PlanFree}, legacy, DriveLayer},
		"scale app":         {Lease{Plan: api.PlanScale}, legacy, DriveLayer},
		"main with sidecar": {Lease{Plan: api.PlanPro}, sidecars, DriveLayerMain},
		"builder":           {Lease{Plan: api.PlanPro, IsBuilder: true}, legacy, ""},
		"unknown plan":      {Lease{Plan: "gold"}, legacy, ""},
	} {
		got := withBlockRateLimit(tc.lease, tc.cfg)
		for _, d := range got.Drives {
			if (d.DriveID == tc.limited) != (d.RateLimiter != nil) {
				t.Errorf("%s: drive %s limiter %+v", name, d.DriveID, d.RateLimiter)
			}
		}
		if tc.limited == "" {
			continue
		}
		want := api.BlockRateLimitPerPlan[tc.lease.Plan]
		for _, d := range got.Drives {
			if d.DriveID != tc.limited {
				continue
			}
			if d.RateLimiter.Bandwidth.Size != int64(want.BandwidthMBps)<<20 || d.RateLimiter.Ops.Size != int64(want.IOPS) {
				t.Errorf("%s: limiter %+v %+v, want %+v", name, *d.RateLimiter.Bandwidth, *d.RateLimiter.Ops, want)
			}
		}
	}
	for _, d := range legacy.Drives {
		if d.RateLimiter != nil {
			t.Fatal("withBlockRateLimit modified the caller's drives")
		}
	}
}

func TestBlockRateLimiterJSON(t *testing.T) {
	raw, err := json.Marshal(Drive{DriveID: DriveLayer, PathOnHost: "layer.ext4", RateLimiter: blockRateLimiter(api.PlanFree)})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		`"rate_limiter":{"bandwidth":{"size":52428800,"one_time_burst":268435456,"refill_time":1000}`,
		`"ops":{"size":2000,"one_time_burst":20000,"refill_time":1000}`,
	} {
		if !strings.Contains(string(raw), want) {
			t.Errorf("drive JSON %s lacks %s", raw, want)
		}
	}
	plain, _ := json.Marshal(Drive{DriveID: DriveBase})
	if strings.Contains(string(plain), "rate_limiter") {
		t.Errorf("unlimited drive JSON carries a limiter: %s", plain)
	}
}

func TestRestoredLayerDriveID(t *testing.T) {
	for _, tc := range []struct {
		workloads []WorkloadSpec
		want      string
	}{
		{nil, DriveLayer},
		{[]WorkloadSpec{{StorageKey: "m"}}, DriveLayerMain},
		{[]WorkloadSpec{{StorageKey: "m", DriveID: "custom"}}, "custom"},
	} {
		if got := restoredLayerDriveID(tc.workloads); got != tc.want {
			t.Errorf("restoredLayerDriveID(%+v) = %q, want %q", tc.workloads, got, tc.want)
		}
	}
}
