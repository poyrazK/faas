//go:build linux

package fcvm

// adr: 20261103235901

import (
	"bytes"
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
)

// Exercises a real guest -> instance NAT namespace -> reduced-MTU L2
// transit -> instance NAT namespace -> guest path without needing KVM.
func TestPrivateNetworkMTUKernel(t *testing.T) {
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_TEST") != "1" {
		t.Skip("opt-in isolated kernel networking test")
	}
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_INNER") != "1" {
		binary, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--mount", "--propagation", "private", "--mount-proc", binary, "-test.run=^TestPrivateNetworkMTUKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "FAAS_PRIVATE_NETWORK_KERNEL_INNER=1")
		output, err := cmd.CombinedOutput()
		t.Logf("isolated MTU acceptance:\n%s", output)
		if err != nil {
			t.Fatal(err)
		}
		return
	}
	runner := privateKernelRunner{}
	must := func(args ...string) {
		t.Helper()
		if err := runner.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	must("mount", "-t", "tmpfs", "tmpfs", "/run/netns")
	policy := netns.DefaultHostPolicy
	policy.StaticEgressBlockedSources = []netip.Addr{netip.MustParseAddr("10.100.0.77")}
	if err := runner.RunInput(t.Context(), []string{"nft", "-c", "-f", "-"}, []byte(policy.Render())); err != nil {
		t.Fatal(err)
	}
	for _, bridge := range []string{"br0", "br1"} {
		must("ip", "link", "add", bridge, "type", "bridge")
		must("ip", "link", "set", bridge, "up")
	}
	must("ip", "link", "add", "transit0", "type", "veth", "peer", "name", "transit1")
	for i, link := range []string{"transit0", "transit1"} {
		bridge := []string{"br0", "br1"}[i]
		must("ip", "link", "set", link, "mtu", "1230")
		must("ip", "link", "set", link, "master", bridge)
		must("ip", "link", "set", link, "up")
	}
	for i, side := range []string{"left", "right"} {
		instance, guest := "inst-"+side, "guest-"+side
		must("ip", "netns", "add", instance)
		must("ip", "netns", "add", guest)
		must("ip", "-n", instance, "link", "add", "tap0", "type", "veth", "peer", "name", "guest-link")
		must("ip", "-n", instance, "link", "set", "guest-link", "netns", guest)
		must("ip", "-n", instance, "addr", "add", "10.0.0.1/30", "dev", "tap0")
		must("ip", "-n", instance, "link", "set", "tap0", "up")
		must("ip", "-n", guest, "addr", "add", "10.0.0.2/30", "dev", "guest-link")
		must("ip", "-n", guest, "link", "set", "guest-link", "up")
		must("ip", "-n", guest, "route", "add", "default", "via", "10.0.0.1")
		must("ip", "netns", "exec", instance, "sysctl", "-qw", "net.ipv4.ip_forward=1", "net.ipv4.conf.all.rp_filter=0")
		config := netns.NewConfig(side, instance, "unused-h", "unused-p", netip.MustParseAddr("10.100.0.2"))
		config.PrivateNetworkBridge = []string{"br0", "br1"}[i]
		config.PrivateVethHost, config.PrivateVethPeer = "private-h-"+side, "private-p-"+side
		config.PrivateNetworkAddress = netip.MustParseAddr([]string{"10.42.0.2", "10.42.0.3"}[i])
		config.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
		config.PrivateNetworkMTU = 1230
		for _, command := range config.PrivateNetworkSetupCommands() {
			must(command...)
		}
		for _, command := range config.PrivateNetworkRouteCommands() {
			must(command...)
		}
		var script strings.Builder
		for _, command := range config.NftCommands() {
			script.WriteString(strings.Join(command[5:], " ") + "\n")
		}
		if err := runner.RunInput(t.Context(), []string{"ip", "netns", "exec", instance, "nft", "-f", "-"}, []byte(script.String())); err != nil {
			t.Fatal(err)
		}
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	server := exec.CommandContext(t.Context(), "ip", "netns", "exec", "guest-right", binary, "-test.run=^TestPrivateNetworkMTUTCPHelper$")
	server.Env = append(os.Environ(), "FAAS_MTU_TCP_HELPER=server", "FAAS_MTU_TCP_DIR="+dir)
	server.Stdout, server.Stderr = os.Stdout, os.Stderr
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Process.Kill(); _ = server.Wait() })
	privateKernelWaitFile(t, filepath.Join(dir, "ready"))
	client := exec.CommandContext(t.Context(), "ip", "netns", "exec", "guest-left", binary, "-test.run=^TestPrivateNetworkMTUTCPHelper$")
	client.Env = append(os.Environ(), "FAAS_MTU_TCP_HELPER=client")
	if output, err := client.CombinedOutput(); err != nil {
		t.Fatalf("large private TCP transfer failed: %v\n%s", err, output)
	}
	output, err := runner.RunCapture(t.Context(), []string{"ip", "netns", "exec", "guest-left", "ping", "-n", "-c", "1", "-W", "1", "-M", "do", "-s", "1300", "10.42.0.3"})
	if err == nil || !strings.Contains(string(output), "1230") {
		t.Fatalf("oversized datagram lacked MTU feedback: err=%v output=%s", err, output)
	}
}

func TestPrivateNetworkMTUTCPHelper(t *testing.T) {
	mode := os.Getenv("FAAS_MTU_TCP_HELPER")
	if mode == "" {
		t.Skip("subprocess helper for MTU acceptance")
	}
	if mode == "server" {
		listener, err := net.Listen("tcp4", ":8080")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = listener.Close() }()
		if err := os.WriteFile(filepath.Join(os.Getenv("FAAS_MTU_TCP_DIR"), "ready"), nil, 0o600); err != nil {
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
	conn, err := net.DialTimeout("tcp4", "10.42.0.3:8080", 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	payload := bytes.Repeat([]byte("private-mtu\n"), 8192)
	if _, err := io.Copy(conn, bytes.NewReader(payload)); err != nil {
		t.Fatal(err)
	}
	echo := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, echo); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(payload, echo) {
		t.Fatal("corrupt private TCP payload")
	}
}
