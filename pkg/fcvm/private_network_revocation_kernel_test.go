//go:build linux

package fcvm

// adr: 20261104120000

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/netns"
)

// Runs in a fresh network/mount namespace and exercises actual conntrack NAT
// tuples, including server-only reply traffic after a same-CIDR tightening.
func TestPrivatePolicyRevocationKernel(t *testing.T) {
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_TEST") != "1" {
		t.Skip("opt-in isolated kernel networking test")
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("FAAS_PRIVATE_NETWORK_KERNEL_INNER") != "1" {
		cmd := exec.CommandContext(t.Context(), "unshare", "--net", "--mount", "--propagation", "private", binary, "-test.run=^TestPrivatePolicyRevocationKernel$", "-test.v")
		cmd.Env = append(os.Environ(), "FAAS_PRIVATE_NETWORK_KERNEL_INNER=1")
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("isolated revocation acceptance: %v\n%s", err, output)
		}
		return
	}
	r := privateKernelRunner{}
	must := func(args ...string) {
		t.Helper()
		if err := r.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll("/run/netns", 0o755); err != nil {
		t.Fatal(err)
	}
	must("mount", "-t", "tmpfs", "tmpfs", "/run/netns")
	must("ip", "link", "add", "private-br", "type", "bridge")
	must("ip", "link", "set", "private-br", "up")
	var configs []netns.Config
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
		c := netns.NewConfig(side, instance, "unused-h", "unused-p", netip.MustParseAddr("10.100.0.2"))
		c.PrivateNetworkBridge, c.PrivateVethHost, c.PrivateVethPeer = "private-br", "ph-"+side, "pp-"+side
		c.PrivateNetworkAddress = netip.MustParseAddr([]string{"10.42.0.2", "10.42.0.3"}[i])
		c.PrivateNetworkCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.0/24")}
		if side == "right" {
			c.PrivateNetworkServiceAddress = netip.MustParseAddr("10.42.0.9")
		}
		c.GuestAppPort = 9090 // published 8080 must survive translation in the guard
		for _, argv := range c.PrivateNetworkSetupCommands() {
			must(argv...)
		}
		for _, argv := range c.PrivateNetworkRouteCommands() {
			must(argv...)
		}
		configs = append(configs, c)
	}
	apply := func(t *testing.T, c netns.Config, reset bool) {
		t.Helper()
		cmds := c.NftCommands()
		if reset {
			cmds = append(c.NftResetCommands(), cmds...)
		}
		var script strings.Builder
		for _, argv := range cmds {
			script.WriteString(strings.Join(argv[5:], " ") + "\n")
		}
		if err := r.RunInput(t.Context(), []string{"ip", "netns", "exec", c.Netns, "nft", "-f", "-"}, []byte(script.String())); err != nil {
			t.Fatal(err)
		}
	}
	apply(t, configs[0], false)
	apply(t, configs[1], false)
	// Repeat replacement to exercise interval-set idempotence and overlap.
	apply(t, configs[0], true)
	apply(t, configs[0], true)
	for _, direction := range []string{"ingress", "egress", "withdraw-vip"} {
		t.Run(direction, func(t *testing.T) {
			apply(t, configs[0], true)
			apply(t, configs[1], true)
			server := startPrivatePolicyProbe(t, binary, "guest-right", "server")
			client := startPrivatePolicyProbe(t, binary, "guest-left", "client")
			client("echo")
			if direction == "withdraw-vip" {
				c := configs[1]
				c.PrivateNetworkServiceAddress = netip.Addr{}
				// Keep the old IP physically bound to prove cached DNAT is denied
				// by current identity even when a stale neighbor still delivers it.
				apply(t, c, true)
			} else {
				idx := 1
				if direction == "egress" {
					idx = 0
				}
				c := configs[idx]
				c.PrivateNetworkAllowedCIDRs = []netip.Prefix{netip.MustParsePrefix("10.42.0.128/25")}
				apply(t, c, true)
			}
			server("push")
			client("read-blocked") // reply direction alone
			client("echo-blocked") // original direction
		})
	}
	t.Run("NeighborAnnouncement", func(t *testing.T) { privatePolicyKernelARPAnnouncement(t, r, configs) })

}

func startPrivatePolicyProbe(t *testing.T, binary, namespace, mode string) func(string) {
	t.Helper()
	cmd := exec.CommandContext(t.Context(), "ip", "netns", "exec", namespace, binary, "-test.run=^TestPrivatePolicyKernelProbe$")
	cmd.Env = append(os.Environ(), "FAAS_PRIVATE_POLICY_PROBE="+mode)
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = input.Close(); _ = cmd.Process.Kill(); _ = cmd.Wait() })
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() || scanner.Text() != "ready" {
		t.Fatalf("%s probe not ready: %s", mode, scanner.Text())
	}
	return func(command string) {
		t.Helper()
		if _, err := fmt.Fprintln(input, command); err != nil {
			t.Fatal(err)
		}
		if !scanner.Scan() || scanner.Text() != "ok" {
			t.Fatalf("%s %s failed: %s", mode, command, scanner.Text())
		}
	}
}

func TestPrivatePolicyKernelProbe(t *testing.T) {
	mode := os.Getenv("FAAS_PRIVATE_POLICY_PROBE")
	if mode == "" {
		t.Skip("subprocess helper")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 20*time.Second)
	defer cancel()
	var conn net.Conn
	if mode == "server" {
		ln, err := net.Listen("tcp4", "10.0.0.2:9090")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = ln.Close() }()
		fmt.Println("ready")
		conn, err = ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		go func() { _, _ = io.Copy(conn, conn) }()
	} else {
		var err error
		conn, err = (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", "10.42.0.9:8080")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("ready")
	}
	defer func() { _ = conn.Close() }()
	scanner := bufio.NewScanner(os.Stdin)
	for scanner.Scan() {
		command := scanner.Text()
		if err := conn.SetDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		if command == "push" {
			if _, err := conn.Write([]byte("push")); err != nil {
				t.Fatal(err)
			}
			fmt.Println("ok")
			continue
		}
		if command != "read-blocked" {
			if _, err := conn.Write([]byte("echo")); err != nil {
				t.Fatal(err)
			}
		}
		var buf [4]byte
		_, err := io.ReadFull(conn, buf[:])
		if command == "echo" {
			if err != nil || string(buf[:]) != "echo" {
				t.Fatalf("echo: %q %v", buf, err)
			}
		} else {
			var timeout net.Error
			if !errors.As(err, &timeout) || !timeout.Timeout() {
				t.Fatalf("revoked %s still passes: %q %v", command, buf, err)
			}
		}
		fmt.Println("ok")
	}
}

// Prime a real neighbor entry with the old owner, then move the VIP. A kernel
// ARP notification from the new owner must replace that cached MAC promptly.
func privatePolicyKernelARPAnnouncement(t *testing.T, r privateKernelRunner, configs []netns.Config) {
	t.Helper()
	must := func(args ...string) {
		t.Helper()
		if err := r.Run(t.Context(), args); err != nil {
			t.Fatal(err)
		}
	}
	oldOwner, newOwner := configs[1], configs[0]
	vip := oldOwner.PrivateNetworkServiceAddress.String()
	must("ip", "addr", "add", "10.42.0.1/24", "dev", "private-br")
	must("ping", "-n", "-c", "1", "-W", "1", vip)
	neighborMAC := func() string {
		t.Helper()
		data, err := r.RunCapture(t.Context(), []string{"ip", "-j", "neigh", "show", "to", vip, "dev", "private-br"})
		if err != nil {
			t.Fatal(err)
		}
		var neighbors []struct {
			MAC string `json:"lladdr"`
		}
		if err := json.Unmarshal(data, &neighbors); err != nil || len(neighbors) != 1 {
			t.Fatalf("neighbor probe: %s %v", data, err)
		}
		return neighbors[0].MAC
	}
	oldMAC := neighborMAC()
	mac := newResourceLinkAddress()
	for mac == oldMAC {
		mac = newResourceLinkAddress()
	}
	must("ip", "-n", oldOwner.Netns, "addr", "del", vip+"/32", "dev", oldOwner.PrivateVethPeer)
	must("ip", "-n", newOwner.Netns, "addr", "replace", vip+"/32", "dev", newOwner.PrivateVethPeer)
	for _, argv := range newOwner.PrivateNetworkAnnounceCommands(mac) {
		must(argv...)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		if neighborMAC() == mac {
			return
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("VIP neighbor retained old MAC %s instead of new owner %s", oldMAC, mac)
		}
	}
}
