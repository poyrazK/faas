//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Run in a private mount AND network namespace, with a private /run/netns:
// unshare --mount --net --propagation private sh -c
// 'mount -t tmpfs tmpfs /run/netns; FAAS_TEST_NETWORK_BATCH=1 ./fcvm.test ...'
func TestMetalIPSetupBatch(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("set FAAS_TEST_NETWORK_BATCH=1 in an isolated network/mount namespace")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Minute)
	defer cancel()
	run := wire.ExecRunner{}
	for _, argv := range [][]string{
		{"ip", "link", "add", netns.TenantBridge, "type", "bridge"},
		{"ip", "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge},
		{"ip", "link", "set", netns.TenantBridge, "up"},
	} {
		if err := run.Run(ctx, argv); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = run.Run(context.Background(), []string{"ip", "link", "del", netns.TenantBridge}) })
	m := newTestManager(run, &fakeVMM{})
	nc := netns.NewConfig("optbatch", "fc-optbatch", "vhoptbatch", "vpoptbatch", netip.MustParseAddr("10.100.0.2"))
	nc.TapUID = 20001
	cleanup := func() {
		for _, argv := range nc.TeardownCommands() {
			_ = run.Run(context.Background(), argv)
		}
	}
	t.Cleanup(cleanup)
	// Verify the complete production setup, including traffic shaping and
	// nft policy, before timing the changed ip-only portion independently.
	nc.EgressMbit = 100
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatal(err)
	}
	assertBatchNetwork(t, nc)
	cleanup()
	samples := map[string][]float64{"sequential": {}, "batched": {}}
	for round := 0; round < 53; round++ {
		order := []string{"sequential", "batched"}
		if round%2 != 0 {
			order[0], order[1] = order[1], order[0]
		}
		for _, mode := range order {
			start := time.Now()
			var err error
			if mode == "batched" {
				err = m.runIPSetupCommands(ctx, nc.SetupCommands())
			} else {
				err = m.runCommands(ctx, nc.SetupCommands())
			}
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			if err != nil {
				t.Fatalf("round %d %s: %v", round, mode, err)
			}
			assertBatchNetwork(t, nc)
			cleanup()
			if round >= 3 {
				samples[mode] = append(samples[mode], elapsed)
			}
		}
	}
	for _, argv := range [][]string{{"ip", "netns", "exec", nc.Netns, "true"}, {"ip", "link", "show", nc.VethHost}} {
		if err := run.Run(ctx, argv); err == nil {
			t.Fatalf("resource survived cleanup: %v", argv)
		}
	}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("network_setup_samples_ms=%s", data)
}

func assertBatchNetwork(t *testing.T, nc netns.Config) {
	t.Helper()
	checks := []struct {
		argv []string
		want string
	}{
		{[]string{"ip", "link", "show", nc.VethHost}, "master " + netns.TenantBridge},
		{[]string{"ip", "-n", nc.Netns, "addr", "show", nc.VethPeer}, nc.HostIP.String() + "/16"},
		{[]string{"ip", "-n", nc.Netns, "addr", "show", nc.Tap}, netns.TapPrefix},
		{[]string{"ip", "-n", nc.Netns, "route", "show", "default"}, "default via 10.100.0.1 dev " + nc.VethPeer},
		{[]string{"ip", "netns", "exec", nc.Netns, "ip", "tuntap", "show", nc.Tap}, fmt.Sprintf("user %d", nc.TapUID)},
		{[]string{"ip", "netns", "exec", nc.Netns, "sysctl", "-n", "net.ipv4.ip_forward"}, "1"},
	}
	for _, check := range checks {
		out, err := exec.CommandContext(t.Context(), check.argv[0], check.argv[1:]...).CombinedOutput()
		if err != nil || !strings.Contains(string(out), check.want) {
			t.Fatalf("network check %v: %v; output %q, want %q", check.argv, err, out, check.want)
		}
	}
}
