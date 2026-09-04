//go:build metal

// Service-replicas metal test (M-2 commit 11, ADR-137 §Decision 3).
//
// The full service-replica state machine (desired/ready/pending,
// admission, replacement wake scheduling) lives at the sched Engine
// layer and is unit-tested in pkg/sched/engine_stop_test.go +
// engine_service_replicas_pgtest_test.go (commit 6). What this file
// pins is the SUBSTRATE assumption that one Manager can host two
// independent service-replica instances side-by-side without state
// collisions: distinct netns, distinct host IPs, distinct jailer
// uids, distinct per-VM cgroup scopes. If this regresses, the
// Engine's replica scaffold cannot function — every desired>1
// deployment would crash on the second wake.
//
// Mirrors TestMetalBoot50Concurrent's parallel-boot shape but at
// the smaller service-replica scale (2 instances) so a regression
// points squarely at the substrate, not at fleet-scale contention.

package fcvm

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
)

// TestMetalServiceReplicas_ConvergeAfterKill pins ADR-137 §Decision
// 3's "service mode keeps desired replicas alive" substrate: boot
// two instances on the same Manager (the substrate-level stand-in
// for service_replicas: {desired=2}), then Destroy one, and assert
// the other remains live and its host IP is unchanged.
//
// The Engine layer adds the wake-side convergence (a replacement
// wake is scheduled within 5s of the RUNNING→STOPPED transition);
// here we only pin that killing ONE instance does NOT collateral-
// damage its peer. A regression here would manifest as the
// surviving instance's netns or cgroup getting torn down by
// Manager.Destroy's "destroy all leases" semantics — a real
// production hazard.
func TestMetalServiceReplicas_ConvergeAfterKill(t *testing.T) {
	kernel, _, _ := metalImages(t)
	m := newMetalManager(t, kernel)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	busybox := ensureBusyboxExt4(t, t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	// Boot two replicas in parallel. Distinct instance names →
	// distinct slots → distinct netns/host IPs/jail uids.
	const (
		replicaA = "svc-replica-a"
		replicaB = "svc-replica-b"
	)
	instA, errA := m.ColdBoot(ctx, ColdBootRequest{
		Instance: replicaA, BaseKey: busybox, LayerKey: busybox,
		VcpuCount: 1, MemSizeMiB: 128,
	})
	if errA != nil {
		t.Fatalf("boot %s: %v", replicaA, errA)
	}
	instB, errB := m.ColdBoot(ctx, ColdBootRequest{
		Instance: replicaB, BaseKey: busybox, LayerKey: busybox,
		VcpuCount: 1, MemSizeMiB: 128,
	})
	if errB != nil {
		// Best-effort cleanup of A so a half-failed test doesn't
		// leave a netns behind for `make leakcheck` to trip over.
		_ = m.Destroy(ctx, replicaA)
		t.Fatalf("boot %s: %v", replicaB, errB)
	}
	if m.LiveCount() != 2 {
		t.Fatalf("LiveCount=%d after booting 2 replicas, want 2", m.LiveCount())
	}

	// Substrate assertion 1: distinct host IPs. The Engine layer
	// assumes two replicas never share a host identity (the
	// 10.100.0.x/24 alloc ranges from pkg/fcvm/alloc.go assign
	// slot 0 / slot 1 to the two instances). If this fails,
	// every load-balancing decision the Engine makes is wrong.
	ipA := instA.Lease.HostIP.String()
	ipB := instB.Lease.HostIP.String()
	if ipA == ipB {
		t.Errorf("replicas share host IP %q — alloc invariant broken", ipA)
	}
	// And both must be probeable on :8080 (DNAT-published).
	for _, c := range []struct {
		name, ip string
	}{
		{replicaA, ipA},
		{replicaB, ipB},
	} {
		conn, err := net.DialTimeout("tcp", net.JoinHostPort(c.ip, "8080"), 2*time.Second)
		if err != nil {
			t.Errorf("%s :8080 unreachable after boot: %v", c.name, err)
			continue
		}
		_ = conn.Close()
	}

	// Kill replica A. The Engine layer would schedule a replacement
	// wake here; at the fcvm layer we only assert that replica B is
	// untouched.
	if err := m.Destroy(ctx, replicaA); err != nil {
		t.Errorf("destroy %s: %v", replicaA, err)
	}
	if m.LiveCount() != 1 {
		t.Errorf("LiveCount=%d after killing A, want 1", m.LiveCount())
	}
	live := m.LiveInstances()
	survived, ok := live[replicaB]
	if !ok {
		t.Fatalf("%s not in live map after destroying %s", replicaB, replicaA)
	}
	// The surviving replica must still hold its original host IP
	// — a regression that re-allocated slots on Destroy would
	// silently break the Engine's stability assumption.
	if survived.Lease.HostIP.String() != ipB {
		t.Errorf("%s host IP changed after killing %s: %q → %q",
			replicaB, replicaA, ipB, survived.Lease.HostIP)
	}

	// Substrate assertion 2: probe B's :8080 once more — it must
	// still answer. This is the real "convergence" gate: B did not
	// merely STAY alive, it stayed READY.
	conn, err := net.DialTimeout("tcp", net.JoinHostPort(survived.Lease.HostIP.String(), "8080"), 2*time.Second)
	if err != nil {
		t.Errorf("%s :8080 unreachable after peer destroy (substrate convergence broken): %v", replicaB, err)
	} else {
		_ = conn.Close()
	}

	// Tear down B and assert zero leaks.
	if err := m.Destroy(ctx, replicaB); err != nil {
		t.Errorf("destroy %s: %v", replicaB, err)
	}
	if m.LiveCount() != 0 {
		t.Errorf("LiveCount=%d after final teardown, want 0", m.LiveCount())
	}
	leakcheck.AssertZero(t)
}
