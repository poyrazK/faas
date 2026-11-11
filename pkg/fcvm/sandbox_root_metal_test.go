//go:build metal

// adr: 20261027090013 §10 — a sandbox session's tenant is root inside its
// guest, and everything that enforces isolation stays outside it: root
// cannot load kernel code, cannot send from another address or MAC, cannot
// reach private, link-local or metadata addresses or mail ports, and cannot
// take more memory than the VM's memory.max. §11: a server root starts in
// the guest answers through port_connect over vsock, in any network mode.
package fcvm

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/netns"
	"github.com/onebox-faas/faas/pkg/sandboxproto"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sandboxRootLayer is the platform sandbox layer: drive1 whose upper holds
// only the sandbox marker.
func sandboxRootLayer(t *testing.T) string {
	t.Helper()
	skel := t.TempDir()
	marker := filepath.Join(skel, "upper", "etc", "faas", "sandbox.json")
	if err := os.MkdirAll(filepath.Dir(marker), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(marker, []byte(`{"kind":"sandbox","version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	layer := filepath.Join(t.TempDir(), "sandbox-layer.ext4")
	if out, err := exec.Command("truncate", "-s", "512M", layer).CombinedOutput(); err != nil {
		t.Fatalf("truncate: %v %s", err, out)
	}
	if out, err := exec.Command("mkfs.ext4", "-q", "-O", "^has_journal", "-d", skel, "-L", "sandbox-layer", "-F", layer).CombinedOutput(); err != nil {
		t.Fatalf("mkfs.ext4: %v %s", err, out)
	}
	return layer
}

// rootExec runs script as root in the session and returns its output and
// exit code.
func rootExec(t *testing.T, m *Manager, instance, sandboxID, script string) (string, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
	if err != nil {
		t.Fatalf("dial sandbox: %v", err)
	}
	var out strings.Builder
	var result sandboxproto.ExecResult
	req := sandboxproto.Request{Op: sandboxproto.OpExec, Argv: []string{"/bin/sh", "-c", script}, RunAs: sandboxproto.RunAsRoot,
		TimeoutMillis: 60_000, MaxOutputBytes: 1 << 20}
	if err := sandboxproto.Do(ctx, conn, req, func(_ uint32, body []byte) error {
		out.Write(body)
		return nil
	}, &result); err != nil {
		t.Fatalf("exec %q: %v", script, err)
	}
	return out.String(), result.ExitCode
}

// guardDrops reads the tap guard's drop counter in the VM's netns.
func guardDrops(t *testing.T, ns string) int {
	t.Helper()
	out, err := exec.Command("ip", "netns", "exec", ns, "nft", "-j", "list", "counter", "netdev", netns.GuestGuardTable, netns.GuestSpoofCounter).Output()
	if err != nil {
		t.Fatalf("read the guard counter: %v", err)
	}
	var doc struct {
		Nftables []struct {
			Counter *struct {
				Packets int `json:"packets"`
			} `json:"counter"`
		} `json:"nftables"`
	}
	if err := json.Unmarshal(out, &doc); err != nil {
		t.Fatalf("decode the guard counter: %v\n%s", err, out)
	}
	for _, o := range doc.Nftables {
		if o.Counter != nil {
			return o.Counter.Packets
		}
	}
	t.Fatalf("no guard counter in:\n%s", out)
	return 0
}

func TestMetalSandboxRootStaysInsideTheVM(t *testing.T) {
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	if kernel == "" || base == "" {
		t.Skip("set FAAS_TEST_KERNEL (a guest kernel from scripts/build-firecracker-kernel.sh) and FAAS_TEST_AGENT_RUN_BASE (an agent-base ext4 with this checkout's guest-init)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	const instance, sandboxID = "sbx-root", "sbx-root-1"
	ctx := context.Background()
	inst, err := m.WakeSandbox(ctx, SandboxWakeRequest{Instance: instance, SandboxID: sandboxID, AccountID: "acct-sandbox", Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 1, MemSizeMiB: 1024, CPUMillicores: 1000, Network: api.SandboxNetworkRegistries})
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), instance) })
	ns := inst.Net.Netns

	t.Run("is root", func(t *testing.T) {
		if out, code := rootExec(t, m, instance, sandboxID, "id -u"); code != 0 || strings.TrimSpace(out) != "0" {
			t.Fatalf("id -u = %q (exit %d)", out, code)
		}
	})

	t.Run("loads no kernel code", func(t *testing.T) {
		// init_module (175), finit_module (313) and kexec_file_load (320)
		// on x86_64: a kernel without modules or kexec answers ENOSYS (38).
		script := `python3 -c '
import ctypes, os
libc = ctypes.CDLL(None, use_errno=True)
for nr in (175, 313, 320):
    rc = libc.syscall(nr, 0, 0, 0, 0, 0)
    print(nr, rc, ctypes.get_errno())
'; test -e /proc/modules && echo has-proc-modules; true`
		out, code := rootExec(t, m, instance, sandboxID, script)
		if code != 0 {
			t.Fatalf("probe exit %d: %s", code, out)
		}
		for _, nr := range []string{"175", "313", "320"} {
			if !strings.Contains(out, nr+" -1 38\n") {
				t.Errorf("syscall %s is not ENOSYS:\n%s", nr, out)
			}
		}
		if strings.Contains(out, "has-proc-modules") {
			t.Errorf("the guest kernel supports modules:\n%s", out)
		}
	})

	t.Run("cannot spoof its address or MAC", func(t *testing.T) {
		before := guardDrops(t, ns)
		// Ordinary traffic from GuestIP and the fixed MAC passes the guard.
		rootExec(t, m, instance, sandboxID, "ping -c 2 -W 1 "+netns.GuestGateway+" >/dev/null 2>&1; true")
		if got := guardDrops(t, ns); got != before {
			t.Fatalf("the guard dropped %d frames of ordinary traffic", got-before)
		}
		phase := func(name, script string) {
			t.Helper()
			was := guardDrops(t, ns)
			out, _ := rootExec(t, m, instance, sandboxID, script)
			if strings.Contains(out, "answered") {
				t.Errorf("%s: a spoofed packet was answered: %s", name, out)
			}
			dropped := guardDrops(t, ns) - was
			t.Logf("%s: the guard dropped %d frames", name, dropped)
			if dropped < 1 {
				t.Errorf("%s: the guard dropped nothing", name)
			}
		}
		// A raw socket writes its own IPv4 header, so the guest's stack
		// sends whatever source root names.
		phase("another source address", `python3 -c '
import socket, struct
def checksum(b):
    s = sum(struct.unpack("!%dH" % (len(b) // 2), b))
    s = (s >> 16) + (s & 0xffff); s += s >> 16
    return ~s & 0xffff
raw = socket.socket(socket.AF_INET, socket.SOCK_RAW, socket.IPPROTO_RAW)
for src, dst in [("10.0.0.3", "`+netns.GuestGateway+`"), ("10.0.0.9", "`+netns.GuestGateway+`"), ("192.0.2.7", "1.1.1.1")]:
    payload = struct.pack("!HHHH", 40000, 53, 8 + 4, 0) + b"spof"
    hdr = struct.pack("!BBHHHBBH4s4s", 0x45, 0, 20 + len(payload), 0, 0, 64, socket.IPPROTO_UDP, 0,
                      socket.inet_aton(src), socket.inet_aton(dst))
    hdr = hdr[:10] + struct.pack("!H", checksum(hdr)) + hdr[12:]
    raw.sendto(hdr + payload, (dst, 0))
print("sent")
'`)
		phase("another MAC", `
ip link set eth0 address 06:00:0a:00:00:03
ip neigh flush dev eth0
ping -c 3 -W 1 `+netns.GuestGateway+` >/dev/null 2>&1 && echo answered
ip link set eth0 address `+netns.SandboxGuestMAC+`
ip neigh flush dev eth0; true`)
		// Back on its own address and MAC, the guest is reachable again.
		if out, _ := rootExec(t, m, instance, sandboxID, "ping -c 2 -W 1 "+netns.GuestGateway+" >/dev/null 2>&1 && echo answered"); !strings.Contains(out, "answered") {
			t.Logf("ordinary ping after the spoof phases: %q", out)
		}
	})

	t.Run("cannot reach private, link-local, metadata or mail", func(t *testing.T) {
		public := netip.MustParseAddr("1.1.1.1")
		// Even an address the registries resolver answered opens only 443.
		if err := m.AllowResolvedEgressNamed(ctx, inst.Net.HostIP, "registry.npmjs.org", []netip.Addr{public}, time.Minute, 0); err != nil {
			t.Fatal(err)
		}
		dial := `python3 -c '
import socket, sys
for host, port in [("169.254.169.254", 80), ("10.100.0.1", 22), ("192.168.1.1", 443), ("172.16.0.1", 443),
                   ("1.1.1.1", 25), ("1.1.1.1", 465), ("1.1.1.1", 587), ("1.1.1.1", 443)]:
    s = socket.socket(); s.settimeout(3)
    try:
        s.connect((host, port)); print(host, port, "open")
    except Exception as e:
        print(host, port, "closed")
    finally:
        s.close()
'`
		out, code := rootExec(t, m, instance, sandboxID, dial)
		if code != 0 {
			t.Fatalf("dial exit %d: %s", code, out)
		}
		for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
			open := strings.HasSuffix(line, " open")
			if want := line == "1.1.1.1 443 open"; open != want {
				t.Errorf("%s", line)
			}
		}
		if !strings.Contains(out, "1.1.1.1 443 open") {
			t.Errorf("the allowed address on 443 is unreachable:\n%s", out)
		}
	})

	t.Run("a server in the guest answers through port_connect", func(t *testing.T) {
		rootExec(t, m, instance, sandboxID, "setsid python3 -m http.server 3000 --bind 127.0.0.1 --directory /etc >/dev/null 2>&1 < /dev/null & "+
			"for i in $(seq 50); do python3 -c 'import socket; socket.create_connection((\"127.0.0.1\", 3000), 1)' 2>/dev/null && break; sleep 0.1; done")
		body, result := portRequest(t, m, instance, sandboxID, 3000, "GET / HTTP/1.0\r\nHost: preview\r\n\r\n")
		if !strings.HasPrefix(body, "HTTP/1.0 200") || result.BytesOut == 0 {
			t.Fatalf("port_connect answered %q (%+v)", body, result)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
		if err != nil {
			t.Fatal(err)
		}
		err = sandboxproto.Do(ctx, conn, sandboxproto.Request{Op: sandboxproto.OpPortConnect, Port: 3999}, nil, nil)
		var guest *sandboxproto.GuestError
		if !errors.As(err, &guest) || guest.Code != sandboxproto.CodePortClosed {
			t.Fatalf("a closed port = %v", err)
		}
	})

	t.Run("cannot exceed memory.max", func(t *testing.T) {
		scope := vmCgroupScope(inst.Lease)
		max, err := readCgroupInt(scope, "memory.max")
		if err != nil {
			t.Fatal(err)
		}
		if want := int64(api.BillableRAMMB(1024)) << 20; max != want {
			t.Fatalf("memory.max %d, want %d", max, want)
		}
		// Root asks for twice the VM's memory; the guest's own kernel
		// stops it, never the host's limit.
		rootExec(t, m, instance, sandboxID, `python3 -c '
chunks = []
while True:
    chunks.append(bytearray(32 << 20))
' >/dev/null 2>&1; true`)
		peak, err := readCgroupInt(scope, "memory.peak")
		if err != nil {
			t.Fatal(err)
		}
		if peak > max {
			t.Fatalf("memory.peak %d is over memory.max %d", peak, max)
		}
		if peak < max/2 {
			t.Fatalf("memory.peak %d MiB: the allocation never pressed the VM's memory", peak>>20)
		}
		if out, code := rootExec(t, m, instance, sandboxID, "echo alive"); code != 0 || strings.TrimSpace(out) != "alive" {
			t.Fatalf("the session died: %q exit %d", out, code)
		}
		t.Logf("memory.peak %s MiB of memory.max %s MiB", strconv.FormatInt(peak>>20, 10), strconv.FormatInt(max>>20, 10))
	})
}

// portRequest sends raw to the guest port over port_connect and returns
// what the port answered.
func portRequest(t *testing.T, m *Manager, instance, sandboxID string, port int, raw string) (string, sandboxproto.PortResult) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := m.DialSandboxSession(ctx, instance, sandboxID)
	if err != nil {
		t.Fatalf("dial sandbox: %v", err)
	}
	c := sandboxproto.NewConn(conn)
	defer func() { _ = c.Close() }()
	if err := c.WriteJSON(ctx, sandboxproto.FrameRequest, sandboxproto.Request{Op: sandboxproto.OpPortConnect, Port: port}); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, sandboxproto.FrameData, []byte(raw)); err != nil {
		t.Fatal(err)
	}
	if err := c.Write(ctx, sandboxproto.FrameDataEnd, nil); err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	for {
		frameType, body, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("read: %v (so far %q)", err, out.String())
		}
		switch frameType {
		case sandboxproto.FrameData:
			out.Write(body)
		case sandboxproto.FrameResult:
			var r sandboxproto.PortResult
			if err := json.Unmarshal(body, &r); err != nil {
				t.Fatal(err)
			}
			return out.String(), r
		case sandboxproto.FrameError:
			t.Fatalf("port_connect error: %s", body)
		}
	}
}
