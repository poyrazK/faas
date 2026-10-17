//go:build linux && metal

// adr: 20261017201012
package fcvm

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/fcvm/leakcheck"
	"github.com/onebox-faas/faas/pkg/rehearsalrecorder"
)

// Portable suites cover broker/relay, journal and retry semantics. This gate
// independently checks that the held original owner closes only after a real
// Firecracker guest and all of its physical resources have been removed.
func TestMetalRehearsalWorkerTermination(t *testing.T) {
	if os.Getenv("FAAS_TEST_REHEARSAL_WORKER_FAULTS") != "1" {
		t.Skip("run the rehearsal-worker-fault native acceptance gate")
	}
	kernel, _, _ := metalImages(t)
	withCgroupRootAt(t, "/sys/fs/cgroup")
	image := ensureEgressGuestExt4WithScript(t, t.TempDir(), `#!/bin/sh
busybox mount -t tmpfs tmpfs /tmp
busybox mkdir -p /tmp/www
printf 'worker ready' > /tmp/www/index.html
exec busybox httpd -f -p 8080 -h /tmp/www
`)
	p := &workerFaultWakePolicy{ready: make(chan struct{})}
	m := newMetalManager(t, kernel).WithLifecycleContext(t.Context()).WithRehearsalNetworkPolicies(p)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	req := WakeRequest{Instance: uuid.NewString(), AppID: uuid.NewString(), AccountID: uuid.NewString(), DeploymentID: uuid.NewString(), RehearsalRunID: rehearsalNetworkTestRun, Plan: "hobby", BaseKey: image, LayerKey: image, VcpuCount: 2, MemSizeMiB: 128}
	first, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cleanup, done := context.WithTimeout(context.Background(), 30*time.Second)
		defer done()
		_ = m.Destroy(cleanup, req.Instance)
		leakcheck.AssertZero(t)
	})
	owner, err := m.InstanceRehearsalRecorder(req.Instance)
	if err != nil {
		t.Fatal(err)
	}
	p.onComplete = func() error {
		if m.LiveCount() != 0 || m.LeasedCount() != 0 {
			return errors.New("receipt preceded physical teardown")
		}
		return nil
	}
	fault := uuid.NewString()
	joined := make(chan struct{})
	var connection *rehearsalrecorder.Connection
	// The host-side checkpoint itself is exercised over HTTP in cmd/vmmd tests.
	c, err := owner.Attach(func() { <-joined; connection.Detach(false) })
	if err != nil {
		t.Fatal(err)
	}
	connection = c
	go func() { _ = owner.HoldWorkerCheckpoint(ctx, fault); close(joined) }()
	select {
	case <-p.ready:
	case <-ctx.Done():
		t.Fatal("checkpoint was not held")
	}
	if err = m.TerminateRehearsalWorker(ctx, req.Instance, fault); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
	client := &http.Client{Timeout: time.Second}
	if response, err := client.Get("http://" + first.Lease.HostIP.String() + ":8080/"); err == nil {
		_ = response.Body.Close()
		t.Fatal("terminated guest is still reachable")
	}
	old := req.Instance
	req.Instance = uuid.NewString()
	replacement, err := m.Wake(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if replacement.Lease.Instance == old {
		t.Fatal("recovery reused old VM identity")
	}
	if err = m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
	leakcheck.AssertZero(t)
}
