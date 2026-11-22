// adr: 20261107122928 amendment 4 — the guard's addressed ingress is the
// instance network namespace's Ip InReceives, read through its Firecracker
// process.
package fcvm

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

const testInstanceA = "11111111-2222-3333-4444-555555555555"

// fakeNetnsProc lays out the /proc entries the reader uses for one process.
func fakeNetnsProc(t *testing.T, root string, pid int, instance, netns string, inReceives uint64) {
	t.Helper()
	dir := filepath.Join(root, strconv.Itoa(pid))
	if err := os.MkdirAll(filepath.Join(dir, "ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "net"), 0o755); err != nil {
		t.Fatal(err)
	}
	cmdline := strings.Join([]string{"/firecracker-v1.17.0-x86_64", "--id", instance, "--api-sock", "api.sock"}, "\x00")
	if err := os.WriteFile(filepath.Join(dir, "cmdline"), []byte(cmdline), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "ns", "net")
	_ = os.Remove(link)
	if err := os.Symlink(netns, link); err != nil {
		t.Fatal(err)
	}
	setInReceives(t, root, pid, inReceives)
}

func setInReceives(t *testing.T, root string, pid int, inReceives uint64) {
	t.Helper()
	snmp := "Ip: Forwarding DefaultTTL InReceives InHdrErrors\nIp: 1 64 " + strconv.FormatUint(inReceives, 10) + " 0\n" +
		"Icmp: InMsgs InErrors\nIcmp: 3 0\n"
	if err := os.WriteFile(filepath.Join(root, strconv.Itoa(pid), "net", "snmp"), []byte(snmp), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newTestNetnsIngressReader(root string, now *time.Time) *NetnsIngressReader {
	r := NewNetnsIngressReader(root)
	r.now = func() time.Time { return *now }
	return r
}

func TestNetnsIngressReaderReadsInReceives(t *testing.T) {
	root := t.TempDir()
	fakeNetnsProc(t, root, 4242, testInstanceA, "net:[4026532001]", 17)
	now := time.Unix(1000, 0)
	r := newTestNetnsIngressReader(root, &now)
	live := map[string]string{testInstanceA: "vh1"}

	r.Poll(live)
	if got, ok := r.AddressedIngress(testInstanceA); !ok || got != 17 {
		t.Fatalf("AddressedIngress = %d, %v; want 17, true", got, ok)
	}
	setInReceives(t, root, 4242, 18)
	now = now.Add(250 * time.Millisecond)
	r.Poll(live)
	if got, ok := r.AddressedIngress(testInstanceA); !ok || got != 18 {
		t.Fatalf("AddressedIngress = %d, %v; want 18, true", got, ok)
	}
	// A reading the poller stopped refreshing is no reading.
	now = now.Add(netnsIngressMaxAge + time.Second)
	if _, ok := r.AddressedIngress(testInstanceA); ok {
		t.Fatal("stale reading served")
	}
}

func TestNetnsIngressReaderRefusesAMovedProcess(t *testing.T) {
	root := t.TempDir()
	fakeNetnsProc(t, root, 4242, testInstanceA, "net:[4026532001]", 17)
	now := time.Unix(1000, 0)
	r := newTestNetnsIngressReader(root, &now)
	live := map[string]string{testInstanceA: "vh1"}
	r.Poll(live)

	// The pid now names another namespace (a reused pid): no reading until
	// a rescan finds the instance's process again.
	fakeNetnsProc(t, root, 4242, "99999999-2222-3333-4444-555555555555", "net:[4026532099]", 5)
	now = now.Add(250 * time.Millisecond)
	r.Poll(live)
	if _, ok := r.AddressedIngress(testInstanceA); ok {
		t.Fatal("served a reading through a pid in another namespace")
	}
	fakeNetnsProc(t, root, 5151, testInstanceA, "net:[4026532001]", 40)
	now = now.Add(netnsIngressRescanEvery)
	r.Poll(live)
	if got, ok := r.AddressedIngress(testInstanceA); !ok || got != 40 {
		t.Fatalf("AddressedIngress after rescan = %d, %v; want 40, true", got, ok)
	}
}

func TestNetnsIngressReaderForgetsGoneInstances(t *testing.T) {
	root := t.TempDir()
	fakeNetnsProc(t, root, 4242, testInstanceA, "net:[4026532001]", 17)
	now := time.Unix(1000, 0)
	r := newTestNetnsIngressReader(root, &now)
	r.Poll(map[string]string{testInstanceA: "vh1"})
	r.Poll(map[string]string{})
	if _, ok := r.AddressedIngress(testInstanceA); ok {
		t.Fatal("served a reading for an instance no longer live")
	}
	if len(r.procs) != 0 || len(r.readings) != 0 {
		t.Fatalf("procs %d readings %d, want none", len(r.procs), len(r.readings))
	}
}

// An instance whose process is not up yet has no reading, and the reader
// scans the process table at most once per netnsIngressRescanEvery for it.
func TestNetnsIngressReaderRescansAtMostEveryInterval(t *testing.T) {
	root := t.TempDir()
	now := time.Unix(1000, 0)
	r := newTestNetnsIngressReader(root, &now)
	live := map[string]string{testInstanceA: "vh1"}
	r.Poll(live)
	if _, ok := r.AddressedIngress(testInstanceA); ok {
		t.Fatal("reading without a process")
	}
	fakeNetnsProc(t, root, 4242, testInstanceA, "net:[4026532001]", 3)
	now = now.Add(time.Second)
	r.Poll(live)
	if _, ok := r.AddressedIngress(testInstanceA); ok {
		t.Fatal("rescanned before netnsIngressRescanEvery")
	}
	now = now.Add(netnsIngressRescanEvery)
	r.Poll(live)
	if got, ok := r.AddressedIngress(testInstanceA); !ok || got != 3 {
		t.Fatalf("AddressedIngress = %d, %v; want 3, true", got, ok)
	}
}

func TestParseIPInReceives(t *testing.T) {
	for _, tc := range []struct {
		name    string
		raw     string
		want    uint64
		wantErr bool
	}{
		{name: "kernel layout", raw: "Ip: Forwarding DefaultTTL InReceives InHdrErrors\nIp: 1 64 123456 0\nIcmp: InMsgs\nIcmp: 0\n", want: 123456},
		{name: "field order from the header", raw: "Ip: InReceives Forwarding\nIp: 9 1\n", want: 9},
		{name: "no Ip lines", raw: "Icmp: InMsgs\nIcmp: 0\n", wantErr: true},
		{name: "no InReceives", raw: "Ip: Forwarding\nIp: 1\n", wantErr: true},
		{name: "not a number", raw: "Ip: InReceives\nIp: x\n", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseIPInReceives([]byte(tc.raw))
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Fatalf("parseIPInReceives = %d, %v; want %d, err %v", got, err, tc.want, tc.wantErr)
			}
		})
	}
}
