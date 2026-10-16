// adr: 907 — cold boots carry a never-inflated free-page-reporting balloon
// only on Firecracker releases that accept it.
package fcvm

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestFreePageReportingSupported(t *testing.T) {
	for version, want := range map[string]bool{
		"1.7.0": false, "1.13.1": false, "1.14.0": true, "1.17.0": true, "2.0.0": true,
		"": false, "garbage": false,
	} {
		if got := FreePageReportingSupported(version); got != want {
			t.Errorf("FreePageReportingSupported(%q) = %v, want %v", version, got, want)
		}
	}
}

func TestWithBalloonAttachesFreePageReporting(t *testing.T) {
	cfg := BuildColdBootConfig(ColdBootSpec{KernelKey: "k", BaseKey: "b", LayerKey: "l", VcpuCount: 2, MemSizeMiB: 256, Tap: "tap0"}, 1)
	if cfg.Balloon != nil {
		t.Fatal("BuildColdBootConfig must leave the balloon to the VMM")
	}
	if got := (&JailerVMM{}).withBalloon(cfg); got.Balloon != nil {
		t.Fatalf("balloon attached while disabled: %+v", got.Balloon)
	}
	got := (&JailerVMM{}).WithFreePageReporting(true).withBalloon(cfg)
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	const want = `"balloon":{"amount_mib":0,"deflate_on_oom":true,"stats_polling_interval_s":0,"free_page_reporting":true}`
	if !strings.Contains(string(raw), want) {
		t.Fatalf("config JSON = %s, want %s", raw, want)
	}
	custom := cfg
	custom.Balloon = &Balloon{AmountMiB: 64}
	if kept := (&JailerVMM{}).WithFreePageReporting(true).withBalloon(custom); kept.Balloon.AmountMiB != 64 || kept.Balloon.FreePageReporting {
		t.Fatalf("caller balloon replaced: %+v", kept.Balloon)
	}
}
