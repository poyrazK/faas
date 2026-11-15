//go:build metal

package fcvm

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

// TestMetalPrimedInitSnapshot (ADR-20261114153000) boots one app, captures an
// unprimed init snapshot right after readiness, runs Manager.Prime with the
// production burst on the request path (as a lifecycle.warmup path would),
// captures a primed one, and times the first request after
// restoring each. Every runtime must prime 20/20 inside the budget; on the JVM
// the primed snapshot's first request must be faster.
//
//	FAAS_TEST_KERNEL=<release vmlinux> FAAS_TEST_RNG_GO_ROOTFS=/var/tmp/alpine.tar \
//	FAAS_TEST_RNG_JAVA_ROOTFS=/var/tmp/temurin21-jdk.tar \
//	make test-metal PKGS=./pkg/fcvm RUN_REGEX=TestMetalPrimedInitSnapshot
func TestMetalPrimedInitSnapshot(t *testing.T) {
	type app struct {
		name, rootfsEnv, file string
		content               func(*testing.T) string
		entrypoint            []string
		mustBeFaster          bool
	}
	apps := []app{
		{"go", "FAAS_TEST_RNG_GO_ROOTFS", "srv/probe", func(t *testing.T) string { return compatGoProbe(t, warmBenchGoApp) }, []string{"/srv/probe"}, false},
		{"node", "FAAS_TEST_RNG_NODE_ROOTFS", "srv/app.js", func(*testing.T) string { return warmBenchNodeApp }, []string{"/usr/local/bin/node", "/srv/app.js"}, false},
		{"java", "FAAS_TEST_RNG_JAVA_ROOTFS", "srv/BenchApp.java", func(*testing.T) string { return warmBenchJavaApp }, []string{"/opt/java/openjdk/bin/java", "/srv/BenchApp.java"}, true},
	}
	ran := false
	for _, a := range apps {
		tarball := os.Getenv(a.rootfsEnv)
		if tarball == "" {
			t.Logf("%s: %s unset, skipped", a.name, a.rootfsEnv)
			continue
		}
		ran = true
		t.Run(a.name, func(t *testing.T) {
			runPrimedInit(t, a.name, tarball, a.file, a.content(t), a.entrypoint, a.mustBeFaster)
		})
	}
	if !ran {
		t.Skip("no FAAS_TEST_RNG_*_ROOTFS set")
	}
}

func runPrimedInit(t *testing.T, name, tarball, file, content string, entrypoint []string, mustBeFaster bool) {
	p, _ := startCompatProbe(t, "prime-"+name, tarball, file, content, entrypoint, map[string]string{}, func(v *JailerVMM) {
		v.WithRestorePrefetch(true)
		if fc, err := DetectFirecrackerVersion(context.Background()); err == nil && ClockRealtimeSupported(fc) && HostClocksourceTSC() {
			v.WithRestoreClockRealtime(true)
		}
	})
	capture := func(tier string) *Snapshot {
		t.Helper()
		snap := &Snapshot{
			FCVersion:         p.fcVersion,
			StorageKey:        fmt.Sprintf("snap/%s/%s/mem", p.instance, tier),
			VMStateStorageKey: fmt.Sprintf("snap/%s/%s/vmstate", p.instance, tier),
			VMStatePath:       filepath.Join(t.TempDir(), "vmstate"),
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		if _, err := p.m.WarmSnapshot(ctx, p.instance, SnapshotSpec{
			VMStatePath: snap.VMStatePath, StorageKey: snap.StorageKey, VMStateStorageKey: snap.VMStateStorageKey,
		}); err != nil {
			t.Fatalf("%s capture: %v", tier, err)
		}
		return snap
	}
	unprimed := capture("unprimed")
	rep, err := p.m.Prime(context.Background(), p.instance, PrimeOptions{Path: "/work", Requests: PrimeMaxRequests, Budget: PrimeMaxBudget})
	if err != nil {
		t.Fatalf("prime: %v", err)
	}
	t.Logf("%s prime: sent=%d ok=%d in %v", name, rep.Sent, rep.OK, rep.Duration)
	if rep.Sent != PrimeMaxRequests || rep.OK != PrimeMaxRequests || rep.Duration > PrimeMaxBudget {
		t.Fatalf("prime report = %+v, want %d/%d inside %v", rep, PrimeMaxRequests, PrimeMaxRequests, PrimeMaxBudget)
	}
	primed := capture("primed")
	if err := p.m.Destroy(context.Background(), p.instance); err != nil {
		t.Fatalf("destroy after captures: %v", err)
	}

	first := map[string][]float64{}
	for i := 0; i < 5; i++ {
		for _, tier := range []struct {
			name string
			snap *Snapshot
		}{{"unprimed", unprimed}, {"primed", primed}} {
			p.snap = tier.snap
			woken := p.restore()
			d, err := timedGet(woken.Lease.HostIP)
			if err != nil {
				t.Fatalf("%s first request: %v", tier.name, err)
			}
			if err := p.m.Destroy(context.Background(), p.instance); err != nil {
				t.Fatalf("destroy: %v", err)
			}
			first[tier.name] = append(first[tier.name], msf(d))
		}
	}
	u, pr := median(first["unprimed"]), median(first["primed"])
	t.Logf("SUMMARY %-5s first request p50 unprimed=%v primed=%v (max %v / %v)", name,
		ms(u), ms(pr), ms(slices.Max(first["unprimed"])), ms(slices.Max(first["primed"])))
	if mustBeFaster && pr >= u {
		t.Fatalf("%s primed first request p50 %v not faster than unprimed %v", name, ms(pr), ms(u))
	}
}
