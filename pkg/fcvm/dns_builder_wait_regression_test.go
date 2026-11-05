// adr: 373 — DNS-gated egress; 20261104160000 — grant owner fences.
package fcvm

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// builderd waits for a build by calling vmmd's Destroy right after the
// builder boots: DestroyWithExport records its cleanup intent, then blocks
// until the guest exits. rc.324 (#6602) refused every DNS grant once cleanup
// intent existed, so a running build could open no connection and every
// build on the node failed. The guest must keep its grants until it exits;
// teardown proper still refuses them.
func TestBuilderReceivesDNSGrantsWhileDestroyWaitsForTheBuild(t *testing.T) {
	run := &fakeInputRunner{}
	waiting, exit := make(chan struct{}), make(chan struct{})
	vmm := &fakeVMM{destroyWithExportHook: func() { close(waiting); <-exit }}
	m := newTestManager(run, vmm)
	const builder, exportDir = "build-01a184ea", "/var/lib/faas/build-out/build-01a184ea"
	if _, err := m.Wake(t.Context(), WakeRequest{
		Instance: builder, BaseKey: "/base.ext4", LayerKey: "/layer.ext4", VcpuCount: 2, MemSizeMiB: 2048,
		Plan: api.PlanScale, EgressMbit: 100, ExportDir: exportDir, Snapshot: usableSnapshot(),
	}); err != nil {
		t.Fatalf("Wake: %v", err)
	}
	m.mu.Lock()
	inst := m.live[builder]
	source, gated := inst.Net.HostIP, inst.Net.DNSGated && inst.Lease.IsBuilder
	m.mu.Unlock()
	if !gated {
		t.Fatal("fixture is not a DNS-gated builder")
	}

	destroyed := make(chan error, 1)
	go func() {
		_, err := m.DestroyWithExport(context.Background(), builder, exportDir)
		destroyed <- err
	}()
	select {
	case <-waiting:
	case <-time.After(2 * time.Second):
		close(exit)
		t.Fatal("Destroy did not start waiting for the build")
	}

	// The gateway captures the owner before its upstream lookup, then grants
	// the answer (nodejs.org) to that owner.
	owner, matched := m.ResolveServiceCaller(source)
	if !matched || owner.InstanceID != builder || owner.NetworkGeneration == "" {
		close(exit)
		t.Fatalf("builder owner = %+v, %v", owner, matched)
	}
	before := run.inputRuns
	answer := []netip.Addr{netip.MustParseAddr("104.16.212.131"), netip.MustParseAddr("104.16.213.131")}
	err := m.AllowResolvedEgress(t.Context(), source, owner, answer, 5*time.Minute, 0)
	policy := string(run.input)
	granted := run.inputRuns == before+1 && strings.Contains(policy, "add element ip faas egress_resolved {") &&
		strings.Contains(policy, "104.16.212.131 timeout 600s") && strings.Contains(policy, "104.16.213.131 timeout 600s")
	close(exit)
	if err != nil || !granted {
		t.Fatalf("running build lost its DNS grant while Destroy waited for it: err=%v granted=%v", err, granted)
	}

	select {
	case err := <-destroyed:
		if err != nil {
			t.Fatalf("DestroyWithExport: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("DestroyWithExport did not finish after the build exited")
	}
	before = run.inputRuns
	if err := m.AllowResolvedEgress(t.Context(), source, owner, answer[:1], 5*time.Minute, 0); !errors.Is(err, ErrResolvedEgressNoInstance) || run.inputRuns != before {
		t.Fatalf("grant after the build exited = %v, nft runs %d", err, run.inputRuns-before)
	}
}

// Once the guest has exited, the namespace is being torn down: a grant that
// arrives then is refused, and the refusal names its reason.
func TestResolvedEgressRefusalNamesItsCheck(t *testing.T) {
	run := &fakeInputRunner{}
	m := newTestManager(run, &fakeVMM{})
	source := wakeGated(t, m, "named", "app")
	owner := resolvedOwner(t, m, source)
	address := []netip.Addr{netip.MustParseAddr("198.51.100.10")}
	for _, tc := range []struct {
		name   string
		source netip.Addr
		owner  ServiceCallerIdentity
		retain bool
		want   string
	}{
		{"generation", source, ServiceCallerIdentity{InstanceID: "named", NetworkGeneration: "stale"}, false, "network generation changed"},
		{"source", netip.MustParseAddr("10.100.9.9"), owner, false, "source address changed"},
		{"gone", source, ServiceCallerIdentity{InstanceID: "gone", NetworkGeneration: owner.NetworkGeneration}, false, "instance is not live"},
		{"teardown", source, owner, true, "teardown started"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.retain {
				inst := m.live["named"]
				m.retainCleanup(inst.Lease, inst.Net, nil)
			}
			err := m.AllowResolvedEgress(t.Context(), tc.source, tc.owner, address, time.Minute, 0)
			if !errors.Is(err, ErrResolvedEgressNoInstance) || ResolvedEgressRefusal(err) != tc.want {
				t.Fatalf("refusal = %v (%q), want %q", err, ResolvedEgressRefusal(err), tc.want)
			}
		})
	}
}
