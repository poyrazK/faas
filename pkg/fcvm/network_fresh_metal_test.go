//go:build linux && metal

package fcvm

import (
	"context"
	"encoding/json"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/wire"
)

// Reconstruct the former resets for an interleaved comparison. All strict
// creation commands, batching, and policy are identical in both variants.
type redundantResetRunner struct {
	wire.ExecRunner
	nc netns.Config
}

func (r redundantResetRunner) Run(ctx context.Context, argv []string) error {
	if len(argv) >= 3 && argv[0] == "tc" && argv[1] == "qdisc" && argv[2] == "add" {
		for _, reset := range r.nc.TcResetCommands() {
			_ = r.ExecRunner.Run(ctx, reset)
		}
	}
	return r.ExecRunner.Run(ctx, argv)
}

func (r redundantResetRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	if len(argv) > 4 && argv[4] == "nft" {
		for _, reset := range r.nc.NftResetCommands() {
			_ = r.ExecRunner.Run(ctx, reset)
		}
	}
	return r.ExecRunner.RunInput(ctx, argv, input)
}

func TestMetalFreshNetworkPolicy(t *testing.T) {
	if os.Getenv("FAAS_TEST_NETWORK_BATCH") != "1" {
		t.Skip("requires private mount/network namespaces and /run/netns")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
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
	nc := netns.NewConfig("optfresh", "fc-optfresh", "vhoptfresh", "vpoptfresh", netip.MustParseAddr("10.100.0.2"))
	nc.TapUID, nc.EgressMbit = 29998, 100
	m := newTestManager(run, &fakeVMM{})
	cleanup := func() {
		for _, argv := range nc.TeardownCommands() {
			_ = run.Run(context.Background(), argv)
		}
	}
	t.Cleanup(cleanup)
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatal(err)
	}
	// Keep the former namespace alive as a crashed process could. The new
	// name must reference a distinct namespace, with none of its stale rules.
	oldNS, err := os.Open("/run/netns/" + nc.Netns)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oldNS.Close() }()
	oldInfo, err := oldNS.Stat()
	if err != nil {
		t.Fatal(err)
	}
	if err := run.Run(ctx, []string{"ip", "netns", "exec", nc.Netns, "nft", "add", "table", "ip", "stale_fixture"}); err != nil {
		t.Fatal(err)
	}
	if err := run.Run(ctx, []string{"tc", "qdisc", "replace", "dev", nc.VethHost, "root", "netem", "delay", "100ms"}); err != nil {
		t.Fatal(err)
	}
	if err := m.setupNetwork(ctx, nc); err != nil {
		t.Fatalf("rebuild with old namespace pinned: %v", err)
	}
	newInfo, err := os.Stat("/run/netns/" + nc.Netns)
	if err != nil || os.SameFile(oldInfo, newInfo) {
		t.Fatalf("rebuild reused the old namespace: %v", err)
	}
	checkFresh := func() {
		assertBatchNetwork(t, nc)
		for _, check := range []struct {
			argv []string
			want string
		}{
			{[]string{"ip", "netns", "exec", nc.Netns, "nft", "list", "table", "ip", "faas"}, "dnat to 10.0.0.2:8080"},
			{[]string{"ip", "netns", "exec", nc.Netns, "nft", "list", "table", "ip6", "faas"}, "drop"},
			{[]string{"tc", "qdisc", "show", "dev", nc.VethHost}, "rate 100Mbit"},
		} {
			out, err := exec.CommandContext(ctx, check.argv[0], check.argv[1:]...).CombinedOutput()
			if err != nil || !strings.Contains(string(out), check.want) || strings.Contains(string(out), "netem") {
				t.Fatalf("fresh policy %v: %v %s", check.argv, err, out)
			}
		}
		if err := run.Run(ctx, []string{"ip", "netns", "exec", nc.Netns, "nft", "list", "table", "ip", "stale_fixture"}); err == nil {
			t.Fatal("stale namespace policy survived recreation")
		}
	}
	checkFresh()
	cleanup()
	managers := map[string]*Manager{
		"with_resets":  newTestManager(redundantResetRunner{nc: nc}, &fakeVMM{}),
		"fresh_policy": m,
	}
	samples := map[string][]float64{"with_resets": {}, "fresh_policy": {}}
	for round := 0; round < 53; round++ {
		order := []string{"with_resets", "fresh_policy"}
		if round%2 == 1 {
			order[0], order[1] = order[1], order[0]
		}
		for _, mode := range order {
			start := time.Now()
			if err := managers[mode].setupNetwork(ctx, nc); err != nil {
				t.Fatalf("%s: %v", mode, err)
			}
			elapsed := float64(time.Since(start)) / float64(time.Millisecond)
			checkFresh()
			cleanup()
			if round >= 3 {
				samples[mode] = append(samples[mode], elapsed)
			}
		}
	}
	data, err := json.Marshal(samples)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("network_fresh_samples_ms=%s", data)
}
