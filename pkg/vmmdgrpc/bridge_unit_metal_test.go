//go:build linux && metal

package vmmdgrpc

// adr: 20261017 connection survival, §2 instance bridge unit (P1c)
//
// The rc.305 canary's shape. Production vmmd runs under faas-vmmd.service,
// whose sandbox (ProtectSystem=strict and the rest) gives it its own mount
// namespace, a slave of the host's: the network namespace `ip netns add`
// mounts at /run/netns/<name> there never reaches PID 1. The e2e harness
// runs vmmd in the host's mount namespace, so it passed while every bridge
// unit on fsn-2 failed (EINVAL, 225/NETWORK). This test runs the vmmd side
// in a transient service with faas-vmmd.service's namespace and privilege
// settings (pkg/daemonunitspec), creates the namespace there as vmmd does,
// starts the instance's bridge as a unit and sends a request through it to
// a server at the guest address.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/daemonunitspec"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/sdunit"
)

const (
	ibridgeMetalDirEnv   = "FAAS_TEST_IBRIDGE_METAL_DIR"
	ibridgeMetalNetnsEnv = "FAAS_TEST_IBRIDGE_METAL_NETNS"
	// ibridgeMetalBridgeEnv names a built vmmd-stream-bridge; without it the
	// test builds one.
	ibridgeMetalBridgeEnv = "FAAS_TEST_STREAM_BRIDGE_BINARY"
)

func TestInstanceBridgeUnitServesFromVmmdMountNamespaceMetal(t *testing.T) {
	if os.Getenv(ibridgeMetalDirEnv) != "" {
		instanceBridgeUnitMetalVmmd(t)
		return
	}
	if os.Geteuid() != 0 {
		t.Skip("needs root")
	}
	if _, err := os.Stat("/run/systemd/system"); err != nil {
		t.Skip("not running under systemd")
	}
	// Both sandboxes exec from here: not /tmp or /var/tmp (the bridge unit's
	// PrivateTmp) and not /root or /home (ProtectHome).
	dir, err := os.MkdirTemp("/run", "faas-ibridge-metal-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	bridge := filepath.Join(dir, "vmmd-stream-bridge")
	if built := os.Getenv(ibridgeMetalBridgeEnv); built != "" {
		copyMetalBinary(t, built, bridge)
	} else if out, err := exec.Command("go", "build", "-o", bridge, "github.com/onebox-faas/faas/cmd/vmmd-stream-bridge").CombinedOutput(); err != nil {
		t.Fatalf("build vmmd-stream-bridge: %v\n%s", err, out)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	test := filepath.Join(dir, "vmmdgrpc.test")
	copyMetalBinary(t, self, test)

	ns := fmt.Sprintf("ibridge-metal-%d", os.Getpid())
	// The vmmd side's `ip netns del` removes the placeholder; a crash leaves it.
	t.Cleanup(func() { _ = os.Remove(filepath.Join("/run/netns", ns)) })
	unit := fmt.Sprintf("faas-metal-vmmd-%d.service", os.Getpid())
	args := []string{"--quiet", "--wait", "--pipe", "--collect", "--unit=" + unit,
		"--setenv=" + ibridgeMetalDirEnv + "=" + dir, "--setenv=" + ibridgeMetalNetnsEnv + "=" + ns,
		"--setenv=" + streamBridgePathEnv + "=" + bridge}
	args = append(args, vmmdSandboxProperties(t, dir)...)
	args = append(args, test, "-test.run=^TestInstanceBridgeUnitServesFromVmmdMountNamespaceMetal$", "-test.v", "-test.count=1")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	out, err := exec.CommandContext(ctx, "systemd-run", args...).CombinedOutput()
	t.Logf("vmmd side (%s):\n%s", unit, out)
	if err != nil {
		_ = exec.Command("systemctl", "stop", unit).Run()
		t.Fatalf("vmmd side failed: %v", err)
	}
	if !strings.Contains(string(out), "--- PASS: TestInstanceBridgeUnitServesFromVmmdMountNamespaceMetal") {
		t.Fatal("vmmd side did not pass")
	}
}

// vmmdSandboxProperties are faas-vmmd.service's namespace and privilege
// settings, read from the unit production renders, as systemd-run
// properties. Its ReadWritePaths become optional: a test host need not have
// them. rw is writable too.
func vmmdSandboxProperties(t *testing.T, rw string) []string {
	t.Helper()
	keep := map[string]bool{
		"ProtectSystem": true, "ProtectHome": true, "PrivateTmp": true, "PrivateDevices": true, "PrivateMounts": true,
		"MountFlags": true, "ProtectKernelTunables": true, "ProtectKernelModules": true, "ProtectControlGroups": true,
		"ProtectHostname": true, "ProtectClock": true, "ProtectProc": true, "ReadOnlyPaths": true, "ReadWritePaths": true,
		"CapabilityBoundingSet": true, "NoNewPrivileges": true,
	}
	var props []string
	for _, line := range strings.Split(string(daemonunitspec.UnitVmmd().Render()), "\n") {
		key, value, ok := strings.Cut(strings.TrimSpace(line), "=")
		if !ok || !keep[key] {
			continue
		}
		if key == "ReadWritePaths" {
			paths := strings.Fields(value)
			for i, p := range paths {
				paths[i] = "-" + strings.TrimPrefix(p, "-")
			}
			value = strings.Join(paths, " ")
		}
		props = append(props, "--property="+key+"="+value)
	}
	if !strings.Contains(strings.Join(props, "\n"), "--property=ProtectSystem=strict") {
		t.Fatalf("faas-vmmd.service no longer renders ProtectSystem=strict; revisit this test: %v", props)
	}
	return append(props, "--property=ReadWritePaths="+rw)
}

// instanceBridgeUnitMetalVmmd is the vmmd side, inside the sandbox.
func instanceBridgeUnitMetalVmmd(t *testing.T) {
	dir, ns := os.Getenv(ibridgeMetalDirEnv), os.Getenv(ibridgeMetalNetnsEnv)
	path := filepath.Join("/run/netns", ns)
	runMetal(t, "ip", "netns", "add", ns)
	t.Cleanup(func() { _ = exec.Command("ip", "netns", "del", ns).Run() })

	// The production shape: the binding is a namespace here and only the
	// empty placeholder in PID 1's mount namespace.
	var here, pid1 syscall.Stat_t
	if err := syscall.Stat(path, &here); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Stat("/proc/1/root"+path, &pid1); err != nil {
		t.Fatal(err)
	}
	if here.Dev == pid1.Dev && here.Ino == pid1.Ino {
		t.Fatal("PID 1 sees this mount namespace's binding: not faas-vmmd.service's shape")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	units, err := sdunit.Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = units.Close() }()
	// What rc.305 asked of systemd fails as it did on fsn-2.
	old := fmt.Sprintf("faas-metal-oldpath-%d.service", os.Getpid())
	_, err = units.StartService(ctx, old, sdunit.Service{ExecStart: []string{"/bin/sleep", "60"}, NetworkNamespacePath: path})
	_ = units.Stop(context.WithoutCancel(ctx), old)
	if !errors.Is(err, sdunit.ErrStartFailed) {
		t.Fatalf("unit in %s as PID 1 sees it: %v; want the rc.305 start failure", path, err)
	}

	// The guest: a server at the guest address inside the namespace.
	runMetal(t, "ip", "-n", ns, "link", "set", "lo", "up")
	runMetal(t, "ip", "-n", ns, "addr", "add", netns.GuestIP+"/32", "dev", "lo")
	ln, err := listenInNetns(path, net.JoinHostPort(netns.GuestIP, "8080"))
	if err != nil {
		t.Fatal(err)
	}
	guest := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, "guest saw %s", r.URL.Path)
	})}
	go func() { _ = guest.Serve(ln) }()
	defer func() { _ = guest.Close() }()

	saved := bridgeRunRoot
	bridgeRunRoot = filepath.Join(dir, "ibridge")
	defer func() { bridgeRunRoot = saved }()
	m := newStreamBridgeManager(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	m.unitsOn = func() bool { return true }
	defer func() { _ = m.close(context.WithoutCancel(ctx)) }()
	id, err := newBridgeIdentity(fcvm.BridgeUIDMax-os.Getpid()%1000, fcvm.BridgeUIDMax-os.Getpid()%1000)
	if err != nil {
		t.Fatal(err)
	}
	instance := fmt.Sprintf("metal-%d", os.Getpid())
	lease, err := m.acquire(ctx, &vmmdpb.ForwardHTTPRequestInit{Instance: instance, Port: 8080}, ns, id)
	if err != nil {
		t.Fatal(err)
	}
	if lease.entry.unit == nil {
		lease.release()
		t.Fatal("the bridge runs as vmmd's child: its unit did not start (see the log above and the journal)")
	}
	name := lease.entry.unit.name
	if !strings.HasSuffix(lease.entry.unit.unit.ControlGroup, "/"+instanceBridgeSlice+"/"+name) {
		t.Fatalf("bridge unit %s runs in cgroup %s", name, lease.entry.unit.unit.ControlGroup)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://unix/hello", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Faas-Bridge-Persistent", "1")
	req.Header.Set("X-Faas-Bridge-Protocol", "h1")
	req.Header.Set("X-Faas-Bridge-Port", "8080")
	resp, err := lease.entry.client.Do(req)
	if err != nil {
		lease.release()
		t.Fatalf("request through bridge unit %s: %v", name, err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	lease.release()
	if resp.StatusCode != http.StatusOK || string(body) != "guest saw /hello" {
		t.Fatalf("through bridge unit %s: %d %q", name, resp.StatusCode, body)
	}

	// Park: the unit is stopped and unloaded (invariant 4).
	m.forget(ctx, instance)
	if u, err := units.Unit(ctx, name); err != nil || u.Exists() {
		t.Fatalf("after park: %+v, %v; want %s unloaded", u, err, name)
	}
}

// listenInNetns listens on addr in the network namespace bound at path. The
// thread that joins the namespace stays locked and exits with its goroutine.
func listenInNetns(path, addr string) (net.Listener, error) {
	type result struct {
		ln  net.Listener
		err error
	}
	ch := make(chan result, 1)
	go func() {
		runtime.LockOSThread()
		fd, err := unix.Open(path, unix.O_RDONLY|unix.O_CLOEXEC, 0)
		if err != nil {
			ch <- result{err: err}
			return
		}
		defer func() { _ = unix.Close(fd) }()
		if err := unix.Setns(fd, unix.CLONE_NEWNET); err != nil {
			ch <- result{err: fmt.Errorf("setns %s: %w", path, err)}
			return
		}
		ln, err := net.Listen("tcp", addr)
		ch <- result{ln, err}
	}()
	r := <-ch
	return r.ln, r.err
}

func runMetal(t *testing.T, name string, args ...string) {
	t.Helper()
	if out, err := exec.Command(name, args...).CombinedOutput(); err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func copyMetalBinary(t *testing.T, from, to string) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(to, raw, 0o755); err != nil {
		t.Fatal(err)
	}
}
