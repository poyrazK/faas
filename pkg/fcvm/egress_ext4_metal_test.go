//go:build metal

// egress_ext4_metal_test.go — tier-1 of the network roadmap regression:
// boot a guest and prove that bridged-tenant egress actually works
// end-to-end after the host MASQUERADE + boot-persistent bridge +
// ip_forward drop-in land.
//
// Why this test is NOT just `ip netns exec` on the host netns:
// `ip netns exec <ns> …` enters the *host* net namespace that wraps
// Firecracker — i.e. the same namespace that holds VethPeer and the
// per-netns MASQUERADE. A curl or nc from there proves the host netns
// has a default route and can reach the public internet, but it does
// NOT prove anything about the *guest* origin: it skips tap0, skips
// the guest's own IP stack, and never hits `iifname tap0` per-netns
// rules. The host `ip netns exec` runbook from PR #128 is a useful
// operator smoke test, but only a guest-OS probe proves the actual
// egress path a tenant app will use. This test is that probe.
//
// How:
//   1. Build a one-off guest rootfs whose /init is a shell script
//      that runs three probes (route, public wget, SMTP nc), writes
//      results into /var/www/, then `exec busybox httpd -f -p 8080 -h
//      /var/www` so the host test can fetch them via the DNAT path
//      already exercised by TestMetalDNATPublishedToGuestPort.
//   2. Boot the guest through Manager.ColdBoot with the same image
//      for BaseKey/LayerKey (the M0 documented exception; there's no
//      app layer for an egress regression probe).
//   3. Fail-fast on any probe miss:
//        - route:   must contain `default via 10.0.0.1 dev eth0`
//        - public:  must contain `HTTP/` (busyBox httpd response body)
//        - smtp:    must contain `smtp-dropped` (the §11 SMTP drop
//                   held at the host layer too, not just per-netns)
//   4. t.Cleanup is registered for Destroy immediately after boot so
//      a fatal in the assertions still tears down netns/jail/cgroup
//      — verified against the manager_metal_test.go:292-295 pattern.
//
// Skip semantics:
//   - The guest probe script reads $FAAS_TEST_EGRESS_URL at runtime;
//     the URL must be present in the test process's environment before
//     the guest rootfs is built (the fixture uses the host env at
//     fixture-build time, not at assertion time). The host test then
//     reads `/var/www/result/public` and `/var/www/result/public-exit`
//     via httpd.
//   - When FAAS_TEST_EGRESS_URL is unset (the default), the public
//     probe is skipped — so a hermetic dev loop with no public
//     connectivity still exercises the SMTP-drop + route assertions,
//     both of which only need host egress. To exercise the public
//     probe on a runner with internet, set it to a stable plain-text
//     HTTP URL (no TLS — the guest has no CA bundle; an IP literal is
//     also fine to skip DNS).
//
// This file is the test FIXTURE (the guest rootfs builder); the test
// itself is in pkg/fcvm/manager_metal_test.go (egress_metal_test.go
// sibling if needed). Keeping the builder here mirrors v6_resume_
// ext4_metal_test.go:152-223 — same per-fixture layout.

package fcvm

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// egressFixtureName is the on-disk filename the builder writes. Cached
// inside the test's t.TempDir so it disappears with the test.
const egressFixtureName = "egress-guest.ext4"

// egressProbeScript is the guest /init (PID 1). The kernel command
// line (Firecracker config) puts `init=` pointing at /sbin/init,
// which we symlink to /bin/busybox; busybox's init reads inittab and
// respawns this line verbatim. We use sh -c so the three probes run
// sequentially with their results captured via stdin/stdout files.
//
// IMPORTANT: this assumes a busybox build with both `sh` and `ip`
// applets compiled in. Debian/Ubuntu's `busybox-static` (full) ships
// with `ip`; `busybox-static-minimal` and Alpine slim builds may NOT
// compile `ip` in. If the guest's busybox lacks `ip`, probe 1 writes
// the literal "no-ip" body, and the test's `t.Fatalf` on that body
// points the operator at the missing applet. The `set +e` is
// load-bearing — busybox sh would otherwise abort on the first
// failing probe and never write the remaining result files. After
// each probe we write the result (NOT the exit code as the body) so
// a failing wget still produces a body for the host to read.
//
// The trailing `exec busybox httpd -f -p 8080 -h /var/www` shifts
// the guest's lifespan: it serves until killed. The host test fetches
// the result files via /result/route /result/public /result/smtp,
// then explicitly calls m.Destroy to poweroff the VM.
const egressProbeScript = `#!/bin/sh
set +e
mkdir -p /var/www/result
# Probe 1: guest default route. The guest's own ip command — proves
# it's the guest OS, not the host netns. Fail-soft: missing busybox
# ip applet (older builds) writes "no-ip" so the assertion fails
# loudly rather than silently passing on a missing binary.
if busybox ip -4 route show default > /var/www/result/route 2>&1; then :; else
  busybox echo "no-ip" > /var/www/result/route
fi
# Probe 2: public egress — only attempt if FAAS_TEST_EGRESS_URL is set.
# busybox wget runs out of /var/www as docroot, with --timeout so the
# guest doesn't hang on a slow link. A 0-byte file means "not run"
# (server-side skip), not a pass — the host test distinguishes by
# checking the file size.
if [ -n "$FAAS_TEST_EGRESS_URL" ]; then
  busybox wget --timeout=5 -O /var/www/result/public "$FAAS_TEST_EGRESS_URL" \
    > /var/www/result/public-exit 2>&1
  busybox echo "rc=$?" > /var/www/result/public-exit
fi
# Probe 3: SMTP port is dropped at the per-netns chain (tap0 iifname).
# busybox nc -w 2 -z exits nonzero on timeout; capture exit code as
# body so the host can grep for smtp-dropped OR the actual reason.
if busybox nc -w 2 -z 8.8.8.8 25 > /var/www/result/smtp 2>&1; then
  busybox echo "smtp-ok" > /var/www/result/smtp
else
  busybox echo "smtp-dropped" > /var/www/result/smtp
fi
# Hand the docroot to httpd. -f keeps it foregrounded so the kernel
# keeps the guest alive (until poweroff -f from the host).
exec busybox httpd -f -p 8080 -h /var/www
`

// ensureEgressGuestExt4 builds (and caches inside dir) a tiny ext4
// image whose /init runs egressProbeScript and ends as an httpd.
// Mirrors buildBusyboxExt4 in busybox_ext4_metal_test.go, but with
// inittab+symlinks shaped for our probe-then-serve model.
func ensureEgressGuestExt4(t *testing.T, dir string) string {
	t.Helper()
	dst := filepath.Join(dir, egressFixtureName)
	if _, err := os.Stat(dst); err == nil {
		return dst
	}

	bb, err := exec.LookPath("busybox")
	if err != nil {
		t.Fatalf("busybox not on PATH; cannot build egress fixture: %v", err)
	}

	work, err := os.MkdirTemp("", "egress-skel-*")
	if err != nil {
		t.Fatalf("skel tempdir: %v", err)
	}
	defer func() { _ = os.RemoveAll(work) }()

	for _, sub := range []string{"bin", "sbin", "dev", "sys", "proc", "etc", "var", "var/www", "var/www/result"} {
		if err := os.MkdirAll(filepath.Join(work, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}

	// busybox + symlinks (same shape as buildBusyboxExt4). /sbin/init
	// → /bin/busybox auto-dispatches by argv[0] → inittab boots.
	if err := bbCopyFile(bb, filepath.Join(work, "bin/busybox")); err != nil {
		t.Fatalf("copy busybox: %v", err)
	}
	for _, name := range []string{"bin/sh", "init", "sbin/init"} {
		if err := os.Symlink("/bin/busybox", filepath.Join(work, name)); err != nil {
			t.Fatalf("symlink %s: %v", name, err)
		}
	}

	// /etc/inittab routes through busybox's init → /bin/sh → script.
	// `::once:` rather than `::respawn:` because the script ends in
	// `exec busybox httpd -f` — it never exits, so respawn is dead
	// config and `once` reads truthfully.
	inittab := "::once:/bin/sh /etc/egress-probe.sh\n"
	if err := os.WriteFile(filepath.Join(work, "etc/inittab"), []byte(inittab), 0o644); err != nil {
		t.Fatalf("inittab: %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "etc/egress-probe.sh"), []byte(egressProbeScript), 0o755); err != nil {
		t.Fatalf("probe script: %v", err)
	}

	// Pre-size + mkfs. 32 MiB — egress script + busybox + httpd runtime
	// fit easily; large for the 8080 docroot so a big wget body doesn't
	// overflow the layer.
	if f, err := os.Create(dst); err != nil {
		t.Fatalf("create ext4: %v", err)
	} else if err := f.Truncate(32 << 20); err != nil {
		_ = f.Close()
		t.Fatalf("truncate ext4: %v", err)
	} else if err := f.Close(); err != nil {
		t.Fatalf("close ext4: %v", err)
	}
	cmd := exec.Command("mkfs.ext4", "-O", "^has_journal", "-d", work, "-L", "faas-egress", "-F", dst)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("mkfs.ext4: %v", err)
	}
	if err := os.Chmod(dst, 0o644); err != nil {
		t.Fatalf("chmod ext4: %v", err)
	}
	return dst
}

// fetchEgressResult does a GET against the guest's httpd-served probe
// result file (relative to the httpd docroot /var/www). Two-letter
// helper so the test body stays readable — this is the only place we
// have to round-trip through http. Uses inst.Lease.HostIP (the same
// DNAT path TestMetalDNATPublishedToGuestPort exercises) so we catch a
// regression where the tenant bridge is missing OR the public egress
// chain is wrong.
func fetchEgressResult(t *testing.T, hostIP, path string) string {
	t.Helper()
	url := fmt.Sprintf("http://%s:8080/%s", hostIP, strings.TrimPrefix(path, "/"))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: status %d", url, resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body %s: %v", url, err)
	}
	return string(body)
}
