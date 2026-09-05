//go:build linux && metal

package fcvm

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

func TestMetalPreparedBridgeMAC(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private network namespace")
	}
	ctx := t.Context()
	runner := wire.ExecRunner{}
	run := func(argv ...string) {
		t.Helper()
		if err := runner.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	run("ip", "link", "add", netns.TenantBridge, "type", "bridge")
	t.Cleanup(func() { _ = runner.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	for _, p := range []struct{ name, peer, mac string }{{"optmac1", "optpeer1", "02:00:00:00:00:02"}, {"optmac2", "optpeer2", "02:00:00:00:00:01"}} {
		run("ip", "link", "add", p.name, "type", "veth", "peer", "name", p.peer)
		t.Cleanup(func() { _ = runner.Run(context.Background(), []string{"ip", "link", "del", p.name}) })
		run("ip", "link", "set", p.name, "address", p.mac)
		if p.name == "optmac2" {
			if err := pinPreparedNetworkBridge(ctx, runner); err != nil {
				t.Fatal(err)
			}
		}
		run("ip", "link", "set", p.name, "master", netns.TenantBridge)
		link, err := net.InterfaceByName(netns.TenantBridge)
		if err != nil || link.HardwareAddr.String() != "02:00:00:00:00:02" {
			t.Fatalf("bridge MAC changed when adding %s: %v, %v", p.name, link, err)
		}
	}
}

func TestMetalPreparedNetworkOwnership(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private mount and network namespaces")
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, argv := range [][]string{{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"}} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	m, p := testPreparedPool(t, 2)
	m.run = run
	p.move, p.removed = movePreparedNetns, preparedNetworkRemoved
	policy := fillTestPreparedPool(t, m, p, 100)
	if len(p.ready) != 2 {
		t.Fatal("cache did not fill")
	}
	old := p.ready[0]
	oldFD, err := os.Open("/run/netns/" + old.config.Netns)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oldFD.Close() }()
	before, err := oldFD.Stat()
	if err != nil {
		t.Fatal(err)
	}
	e := p.claim("optprepared", policy)
	if e == nil {
		t.Fatal("claim failed")
	}
	claimed := true
	t.Cleanup(func() {
		if claimed {
			p.discard(*e)
		}
	})
	after, err := os.Stat("/run/netns/" + e.config.Netns)
	if err != nil || !os.SameFile(before, after) {
		t.Fatalf("namespace identity changed during transfer: %v", err)
	}
	if _, err := os.Stat("/run/netns/" + old.config.Netns); !os.IsNotExist(err) {
		t.Fatalf("old namespace alias survived: %v", err)
	}
	assertBatchNetwork(t, e.config)
	if m.LeasedCount() != 1 || len(m.alloc.reserved) != 1 {
		t.Fatal("admission count includes unused reservation")
	}
	// Teardown removes the host veth even while the test pins the old netns FD.
	p.discard(*e)
	claimed = false
	if m.LeasedCount() != 0 {
		t.Fatal("claimed network did not tear down")
	}
	// Normal teardown deleted the host veth even while the old FD was open.
	if !preparedNetworkRemoved(e.config) {
		t.Fatal("claimed resources survived teardown")
	}
	// An existing destination must never be overwritten by the optimization.
	remaining := p.ready[0]
	marker := "/run/netns/fc-optcollision"
	if err := os.WriteFile(marker, []byte("owned"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Remove(marker) })
	if err := movePreparedNetns(remaining.config.Netns, "fc-optcollision"); err == nil {
		t.Fatal("overwrote existing destination")
	}
	b, err := os.ReadFile(marker)
	if err != nil || string(b) != "owned" {
		t.Fatal("existing destination modified")
	}
	if err := ReapPreparedNetworks(ctx, run); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat("/run/netns/" + remaining.config.Netns); !os.IsNotExist(err) {
		t.Fatal("startup recovery left unused namespace")
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatal("startup recovery touched another namespace")
	}
}
