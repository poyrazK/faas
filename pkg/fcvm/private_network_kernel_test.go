//go:build linux

package fcvm

// adr: 20261104001001

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/privatenetwork"
)

type privateKernelRunner struct{}

func (privateKernelRunner) SetStaticEgressRules([]netns.StaticEgressRule) {}

func (r privateKernelRunner) Run(ctx context.Context, argv []string) error {
	_, err := r.RunCapture(ctx, argv)
	return err
}
func (privateKernelRunner) RunCapture(ctx context.Context, argv []string) ([]byte, error) {
	output, err := exec.CommandContext(ctx, argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		return output, fmt.Errorf("%v: %w: %s", argv, err, output)
	}
	return output, nil
}
func (privateKernelRunner) RunInput(ctx context.Context, argv []string, input []byte) error {
	cmd := exec.CommandContext(ctx, argv[0], argv[1:]...)
	cmd.Stdin = bytes.NewReader(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %w: %s", argv, err, output)
	}
	return nil
}
func (r privateKernelRunner) Render(ctx context.Context) error {
	return r.RunInput(ctx, []string{"nft", "-f", "-"}, []byte(netns.ActiveHostPolicyForRender().Render()))
}

// Opt in on Linux with CAP_NET_ADMIN and CAP_SYS_ADMIN, iproute2, nft and
// ping. The test reexecutes in fresh network/mount namespaces before touching
// firewall state, and mounts its own /run/netns so host namespaces stay intact.
func TestPrivateNetworkRoutedPeeringKernel(t *testing.T) {
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_TEST") != "1" {
		t.Skip("opt-in isolated kernel networking test")
	}
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_INNER") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--mount", "--propagation", "private", "--mount-proc", binary, "-test.run=^TestPrivateNetworkRoutedPeeringKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "FAAS_PRIVATE_NETWORK_KERNEL_INNER=1")
		output, err := cmd.CombinedOutput()
		t.Logf("isolated kernel acceptance:\n%s", output)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	run := privateKernelRunner{}
	must := func(argv ...string) {
		t.Helper()
		if err := run.Run(t.Context(), argv); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	must("mount", "-t", "tmpfs", "tmpfs", "/run/netns")
	must("ip", "link", "set", "lo", "up")
	must("ip", "link", "add", "underlay0", "type", "veth", "peer", "name", "underlay1")
	must("ip", "addr", "add", "100.64.0.10/24", "dev", "underlay0")
	must("ip", "link", "set", "underlay0", "up")
	m := newTestManager(run, &fakeVMM{}).WithCaptureRunner(run)
	netns.SwapActiveHostPolicy(netns.DefaultHostPolicy)
	m.SetHostRenderer(run)
	networks := []privatenetwork.FabricSpec{
		{AccountID: "acct-kernel", NetworkID: "left", Region: "fra1", CIDR: netip.MustParsePrefix("10.42.0.0/24")},
		{AccountID: "acct-kernel", NetworkID: "right", Region: "fra1", CIDR: netip.MustParsePrefix("10.43.0.0/24")},
		{AccountID: "acct-kernel", NetworkID: "unpeered", Region: "fra1", CIDR: netip.MustParsePrefix("10.44.0.0/24")},
	}
	for _, spec := range networks {
		if err := m.ReconcilePrivateNetworkFabric(t.Context(), spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR); err != nil {
			t.Fatal(err)
		}
	}
	t.Run("VXLANFloodAndRepair", func(t *testing.T) {
		probeErr := run.Run(t.Context(), []string{"ip", "link", "add", "probe-vxlan", "type", "vxlan", "id", "111", "dev", "underlay0", "local", "100.64.0.10", "dstport", "4789", "nolearning"})
		if probeErr != nil {
			if strings.Contains(probeErr.Error(), "Unknown device type") || strings.Contains(probeErr.Error(), "Operation not supported") {
				t.Skipf("kernel VXLAN driver unavailable: %v", probeErr)
			}
			t.Fatal(probeErr)
		}
		must("ip", "link", "del", "probe-vxlan")
		m.WithPrivateNetworkTransport(PrivateNetworkTransportConfig{Enabled: true, OverlayInterface: "underlay0", LocalAddress: netip.MustParseAddr("100.64.0.10")})
		peers := []netip.Addr{netip.MustParseAddr("100.64.0.11"), netip.MustParseAddr("100.64.0.12")}
		if err := m.ReconcilePrivateNetworkFabricWithPeers(t.Context(), "acct-kernel", "left", "fra1", networks[0].CIDR, peers); err != nil {
			t.Fatal(err)
		}
		ready, err := m.CheckPrivateNetworkFabric(t.Context(), "acct-kernel", "left", "fra1", networks[0].CIDR, peers, true)
		if err != nil || !ready.Ready {
			t.Fatalf("kernel fabric not ready: %+v: %v", ready, err)
		}
		// Real kernel FDB repair after deletion must recreate the VXLAN, restore
		// bridge membership, and remove the drained peer without duplicate entries.
		must("ip", "link", "del", privatenetwork.FabricTransportLinkName("acct-kernel", "left"))
		for _, roster := range [][]netip.Addr{peers, peers[:1], peers[:1]} {
			if err := m.ReconcilePrivateNetworkFabricWithPeers(t.Context(), "acct-kernel", "left", "fra1", networks[0].CIDR, roster); err != nil {
				t.Fatal(err)
			}
		}
		ready, err = m.CheckPrivateNetworkFabric(t.Context(), "acct-kernel", "left", "fra1", networks[0].CIDR, peers[:1], true)
		if err != nil || !ready.Ready {
			t.Fatalf("kernel repair not ready: %+v: %v", ready, err)
		}
	})
	member := func(name string, spec privatenetwork.FabricSpec) {
		t.Helper()
		must("ip", "netns", "add", name)
		host := "kh-" + name
		must("ip", "link", "add", host, "type", "veth", "peer", "name", "kp-"+name)
		must("ip", "link", "set", "kp-"+name, "netns", name)
		must("ip", "link", "set", host, "master", privatenetwork.BridgeName(spec.AccountID, spec.NetworkID))
		must("ip", "link", "set", host, "up")
		address := spec.CIDR.Addr().Next().Next()
		must("ip", "-n", name, "addr", "add", netip.PrefixFrom(address, spec.CIDR.Bits()).String(), "dev", "kp-"+name)
		must("ip", "-n", name, "link", "set", "kp-"+name, "up")
		config := netns.NewConfig(name, name, "unused-h", "unused-p", netip.MustParseAddr("10.100.0.2"))
		config.PrivateNetworkBridge = privatenetwork.BridgeName(spec.AccountID, spec.NetworkID)
		config.PrivateVethHost, config.PrivateVethPeer = host, "kp-"+name
		config.PrivateNetworkAddress = address
		config.PrivateNetworkCIDRs = []netip.Prefix{spec.CIDR}
		for _, network := range networks {
			if network.CIDR != spec.CIDR {
				config.PrivateNetworkCIDRs = append(config.PrivateNetworkCIDRs, network.CIDR)
			}
		}
		for _, argv := range config.PrivateNetworkRouteCommands() {
			must(argv...)
		}
	}
	member("left", networks[0])
	member("right", networks[1])
	member("third", networks[2])
	routes := []privatenetwork.PeeringRoute{
		{FromNetworkID: "left", DestinationNetwork: "right", DestinationCIDR: networks[1].CIDR},
		{FromNetworkID: "right", DestinationNetwork: "left", DestinationCIDR: networks[0].CIDR},
	}
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "acct-kernel", "fra1", networks, routes); err != nil {
		t.Fatal(err)
	}
	ping := func(from, address string, allowed bool) {
		t.Helper()
		err := run.Run(t.Context(), []string{"ip", "netns", "exec", from, "ping", "-n", "-c", "1", "-W", "1", address})
		if (err == nil) != allowed {
			t.Fatalf("%s -> %s allowed=%t: %v", from, address, allowed, err)
		}
	}
	ping("left", "10.43.0.2", true)
	ping("right", "10.42.0.2", true)
	ping("left", "10.44.0.2", false)
	// Identical addresses in a second account must use a separate router and
	// cannot inherit the first account's peering authorization or conntrack.
	other := append([]privatenetwork.FabricSpec(nil), networks[:2]...)
	for i := range other {
		other[i].AccountID = "acct-other"
		if err := m.ReconcilePrivateNetworkFabric(t.Context(), other[i].AccountID, other[i].NetworkID, other[i].Region, other[i].CIDR); err != nil {
			t.Fatal(err)
		}
	}
	member("other-left", other[0])
	member("other-right", other[1])
	ping("other-left", "10.43.0.2", false)
	if err := m.ReconcilePrivateNetworkPeerings(t.Context(), "acct-other", "fra1", other, routes); err != nil {
		t.Fatal(err)
	}
	ping("other-left", "10.43.0.2", true)
	privateKernelTCPRevocation(t, func() error {
		return m.ReconcilePrivateNetworkPeerings(t.Context(), "acct-kernel", "fra1", networks, nil)
	})
	ping("left", "10.43.0.2", false)
	ping("other-left", "10.43.0.2", true)
	for _, spec := range append(networks, other...) {
		if err := m.RemovePrivateNetworkFabric(t.Context(), spec.AccountID, spec.NetworkID, spec.Region, spec.CIDR); err != nil {
			t.Fatal(err)
		}
	}
	remaining, err := run.RunCapture(t.Context(), []string{"ip", "netns", "list"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(remaining), "gpr-") {
		t.Fatalf("empty router namespace leaked: %s", remaining)
	}
}

func privateKernelWaitFile(t *testing.T, path string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for TCP helper: %s", path)
		}
	}
}

func privateKernelTCPRevocation(t *testing.T, revoke func() error) {
	t.Helper()
	dir := t.TempDir()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	start := func(namespace, mode string) *exec.Cmd {
		cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", namespace, binary, "-test.run=^TestPrivateNetworkKernelTCPHelper$")
		cmd.Env = append(os.Environ(), "FAAS_PRIVATE_NETWORK_TCP_MODE="+mode, "FAAS_PRIVATE_NETWORK_TCP_DIR="+dir)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		return cmd
	}
	start("right", "server")
	privateKernelWaitFile(t, filepath.Join(dir, "server"))
	client := start("left", "client")
	privateKernelWaitFile(t, filepath.Join(dir, "connected"))
	if err := revoke(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "revoked"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := client.Wait(); err != nil {
		t.Fatalf("established TCP peering was not revoked: %v", err)
	}
}

func TestPrivateNetworkKernelTCPHelper(t *testing.T) {
	mode := os.Getenv("FAAS_PRIVATE_NETWORK_TCP_MODE")
	if mode == "" {
		t.Skip("subprocess helper for isolated kernel acceptance")
	}
	dir := os.Getenv("FAAS_PRIVATE_NETWORK_TCP_DIR")
	if mode == "server" {
		listener, err := net.Listen("tcp4", "10.43.0.2:8090")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		if err := os.WriteFile(filepath.Join(dir, "server"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
		conn, err := listener.Accept()
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = conn.Close() }()
		_, _ = io.Copy(conn, conn)
		return
	}
	conn, err := net.DialTimeout("tcp4", "10.43.0.2:8090", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len("before"))
	if _, err := io.ReadFull(conn, buf); err != nil || string(buf) != "before" {
		t.Fatalf("TCP echo before revocation: %q, %v", buf, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "connected"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	privateKernelWaitFile(t, filepath.Join(dir, "revoked"))
	if err := conn.SetDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("after")); err != nil {
		t.Fatal(err)
	}
	_, err = conn.Read(buf)
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Fatalf("revoked established flow remained usable: %v", err)
	}
}
