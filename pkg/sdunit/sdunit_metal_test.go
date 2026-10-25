//go:build metal

package sdunit

// adr: 20261017 connection survival, §2 unit properties (P1c)
//
// The property encoding against the real PID 1: every property an instance
// bridge unit carries must be accepted by systemd's transient-unit parser,
// and the run must land where vmmd expects it (uid, netns, slice cgroup).

import (
	"context"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestTransientServiceOnSystemdMetal(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Skip("not running under systemd")
	}
	ns := fmt.Sprintf("sdunit-test-%d", os.Getpid())
	if out, err := exec.Command("ip", "netns", "add", ns).CombinedOutput(); err != nil {
		t.Fatalf("ip netns add: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", ns).Run() })
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o777); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	m, err := Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.Close() }()

	const uid = 39998 // top of the bridge uid range, which must be free of accounts
	name := fmt.Sprintf("faas-ibridge-sdunit-test-%08x.service", os.Getpid())
	svc := Service{
		Description:             "sdunit metal test",
		ExecStart:               []string{"/bin/sleep", "60"},
		Environment:             []string{"PATH=/usr/bin", "FAAS_TEST=1", "PATH=/usr/bin:/bin"},
		UID:                     uid,
		GID:                     uid,
		NetworkNamespacePath:    filepath.Join("/run/netns", ns),
		Slice:                   "faas-tenant-ibridge.slice",
		MemoryMaxBytes:          32 << 20,
		TasksMax:                64,
		LimitNOFILE:             8256,
		ReadWritePaths:          []string{dir},
		IPAddressAllow:          []netip.Prefix{netip.MustParsePrefix("10.0.0.2/32")},
		RestrictAddressFamilies: []string{"AF_UNIX", "AF_INET"},
		TimeoutStop:             5 * time.Second,
	}
	u, err := m.StartService(ctx, name, svc)
	t.Cleanup(func() { _ = m.Stop(context.Background(), name) })
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if !u.Active() || u.MainPID <= 1 || len(u.InvocationID) != 32 || !strings.HasSuffix(u.ControlGroup, "/faas-tenant-ibridge.slice/"+name) {
		t.Fatalf("started unit = %+v", u)
	}
	status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", u.MainPID))
	if err != nil || !strings.Contains(string(status), fmt.Sprintf("Uid:\t%d\t%d\t%d\t%d", uid, uid, uid, uid)) {
		t.Fatalf("main process status: %v\n%s", err, status)
	}
	var want, got syscall.Stat_t
	if syscall.Stat(svc.NetworkNamespacePath, &want) != nil || syscall.Stat(fmt.Sprintf("/proc/%d/ns/net", u.MainPID), &got) != nil || want.Ino != got.Ino {
		t.Fatal("main process is not in the unit's network namespace")
	}
	// vmmd reads the bridge slice's memory here (instance_bridge_resident_bytes).
	if _, err := os.Stat("/sys/fs/cgroup/faas.slice/faas-tenant.slice/faas-tenant-ibridge.slice/memory.current"); err != nil {
		t.Fatalf("bridge slice cgroup: %v", err)
	}
	names, err := m.List(ctx, "faas-ibridge-sdunit-test-*.service")
	if err != nil || len(names) != 1 || names[0] != name {
		t.Fatalf("list = %v, %v", names, err)
	}
	if err := m.Stop(ctx, name); err != nil {
		t.Fatalf("stop: %v", err)
	}
	if u, err := m.Unit(ctx, name); err != nil || u.Exists() {
		t.Fatalf("after stop: %+v, %v; want unloaded", u, err)
	}
	if err := m.Stop(ctx, name); err != nil {
		t.Fatalf("stopping an absent unit: %v", err)
	}
}
