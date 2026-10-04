//go:build metal && linux

// adr: 530 — the service TCP proxy recovers the dialed service address with
// SO_ORIGINAL_DST. This drives a real DNAT in a throwaway netns and reads it
// back through TCPOriginalDestination, so the sockaddr parsing (offsets and
// port byte order) is proven on a kernel rather than assumed.
package gateway

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

const origDstHelperEnv = "FAAS_ORIGDST_HELPER"

// TestOriginalDestinationHelper runs only inside the netns prepared by
// TestMetalTCPOriginalDestination.
func TestOriginalDestinationHelper(t *testing.T) {
	if os.Getenv(origDstHelperEnv) != "1" {
		t.Skip("helper process only")
	}
	ln, err := net.Listen("tcp", "127.0.0.1:10082")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := ln.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	client, err := net.DialTimeout("tcp", "198.19.0.7:5432", 2*time.Second)
	if err != nil {
		t.Fatalf("dial service address: %v", err)
	}
	defer func() { _ = client.Close() }()
	select {
	case conn := <-accepted:
		defer func() { _ = conn.Close() }()
		dst, err := TCPOriginalDestination(conn)
		if err != nil {
			t.Fatalf("TCPOriginalDestination: %v", err)
		}
		fmt.Printf("ORIGDST=%s\n", dst)
	case <-time.After(2 * time.Second):
		t.Fatal("DNATed connection never reached the listener")
	}
}

func TestMetalTCPOriginalDestination(t *testing.T) {
	for _, bin := range []string{"ip", "nft"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	ns := fmt.Sprintf("fo_%d", os.Getpid())
	if out, err := exec.Command("ip", "netns", "add", ns).CombinedOutput(); err != nil {
		t.Skipf("cannot create a netns (need CAP_SYS_ADMIN): %v\n%s", err, out)
	}
	t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput() })
	for _, argv := range [][]string{
		{"ip", "-n", ns, "link", "set", "lo", "up"},
		{"ip", "-n", ns, "route", "add", "198.19.0.0/16", "dev", "lo"},
	} {
		if out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput(); err != nil {
			t.Fatalf("%v: %v\n%s", argv, err, out)
		}
	}
	// Locally originated traffic is DNATed in the output hook; conntrack
	// keeps the original destination exactly as for the host prerouting rule.
	rules := "table ip t { chain out { type nat hook output priority -100; ip daddr 198.19.0.0/16 meta l4proto tcp dnat to 127.0.0.1:10082; } }\n"
	load := exec.Command("ip", "netns", "exec", ns, "nft", "-f", "-")
	load.Stdin = strings.NewReader(rules)
	if out, err := load.CombinedOutput(); err != nil {
		t.Fatalf("load nft rules: %v\n%s", err, out)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command("ip", "netns", "exec", ns, self, "-test.run=^TestOriginalDestinationHelper$", "-test.v")
	helper.Env = append(os.Environ(), origDstHelperEnv+"=1")
	out, err := helper.CombinedOutput()
	if err != nil {
		t.Fatalf("helper: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ORIGDST=198.19.0.7:5432") {
		t.Fatalf("original destination not recovered:\n%s", out)
	}
}
