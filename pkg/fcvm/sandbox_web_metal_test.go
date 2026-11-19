//go:build metal

// adr: 20261112121456 §2–§4, 20261117091026 §3–§4, 20261116182615 §1 — a
// web-mode session reaches only addresses its resolver granted, on web ports;
// its domain policy refuses a grant before the budget counts it, and the
// session's domain log records both; a browser session boots with Chromium
// serving and reads a page through that same gate.
package fcvm

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/wire"
)

// sandboxWebProbe tries each host:port from the guest and prints one line
// per target: "host port open|closed".
const sandboxWebProbe = `python3 -c '
import socket, sys
for target in sys.argv[1:]:
    host, port = target.rsplit(":", 1)
    s = socket.socket(); s.settimeout(3)
    try:
        s.connect((host, int(port))); print(host, port, "open")
    except Exception:
        print(host, port, "closed")
    finally:
        s.close()
' `

func newWebMetalManager(t *testing.T) (*Manager, string) {
	t.Helper()
	kernel := os.Getenv("FAAS_TEST_KERNEL")
	base := os.Getenv("FAAS_TEST_AGENT_RUN_BASE")
	if kernel == "" || base == "" {
		t.Skip("set FAAS_TEST_KERNEL and FAAS_TEST_AGENT_RUN_BASE (an agent-base with the sandbox browser)")
	}
	withCgroupRootAt(t, "/sys/fs/cgroup")
	m := NewManager(wire.ExecRunner{}, newMetalVMM(t, 120*time.Second), Paths{Kernel: kernel}, os.Getenv("FAAS_TEST_FC_VERSION"), slog.Default(), nil)
	// The test host has no tenant egress gateway; web sessions leave from
	// the node, as on development fleets.
	m.WithSandboxWebNodeEgress(true)
	return m, base
}

func webMetalWake(t *testing.T, base, instance string) SandboxWakeRequest {
	return SandboxWakeRequest{Instance: instance, SandboxID: instance + "-session", AccountID: "acct-sandbox-web", Plan: api.PlanPro,
		BaseKey: base, LayerKey: sandboxRootLayer(t), VcpuCount: 2, MemSizeMiB: 1024, CPUMillicores: 2000, Network: api.SandboxNetworkWeb}
}

func TestMetalSandboxWebEgressPolicy(t *testing.T) {
	m, base := newWebMetalManager(t)
	ctx := context.Background()
	req := webMetalWake(t, base, "sbx-web-policy")
	req.EgressDeny = []string{"example.com"}
	inst, err := m.WakeSandbox(ctx, req)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
	if !inst.Net.SandboxWeb || !inst.Net.DNSGated {
		t.Fatalf("network %+v is not web mode", inst.Net)
	}

	// Before any grant, nothing public is reachable.
	probe := sandboxWebProbe + "1.1.1.1:443 1.0.0.1:443"
	if out, _ := rootExec(t, m, req.Instance, req.SandboxID, probe); out != "1.1.1.1 443 closed\n1.0.0.1 443 closed\n" {
		t.Fatalf("before any grant:\n%s", out)
	}

	owner := resolvedOwner(t, m, inst.Net.HostIP)
	if err := m.AllowResolvedEgressNamed(ctx, inst.Net.HostIP, owner, "one.one.one.one", []netip.Addr{netip.MustParseAddr("1.1.1.1")}, time.Minute, 0); err != nil {
		t.Fatalf("grant for an allowed name: %v", err)
	}
	err = m.AllowResolvedEgressNamed(ctx, inst.Net.HostIP, owner, "www.example.com", []netip.Addr{netip.MustParseAddr("1.0.0.1")}, time.Minute, 0)
	if err == nil {
		t.Fatal("the session's policy denies example.com, yet the grant passed")
	}

	out, code := rootExec(t, m, req.Instance, req.SandboxID,
		sandboxWebProbe+"1.1.1.1:443 1.1.1.1:80 1.1.1.1:25 1.1.1.1:22 1.0.0.1:443 169.254.169.254:80 10.100.0.1:22")
	want := "1.1.1.1 443 open\n1.1.1.1 80 open\n1.1.1.1 25 closed\n1.1.1.1 22 closed\n1.0.0.1 443 closed\n169.254.169.254 80 closed\n10.100.0.1 22 closed\n"
	if code != 0 || out != want {
		t.Fatalf("egress after one granted and one refused name (exit %d):\n%s\nwant:\n%s", code, out, want)
	}

	logged := map[string]string{}
	for _, d := range m.DrainSandboxWebDomains(req.Instance) {
		logged[d.Domain] = d.Refused
	}
	// A grant is logged by registrable domain.
	if r, ok := logged["one.one"]; !ok || r != "" {
		t.Errorf("domain log %v: want one.one granted", logged)
	}
	if r := logged["www.example.com"] + logged["example.com"]; r != SandboxWebRefusedPolicy {
		t.Errorf("domain log %v: want example.com refused by policy", logged)
	}
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
}

func TestMetalSandboxBrowserReadsAGrantedPage(t *testing.T) {
	m, base := newWebMetalManager(t)
	ctx := context.Background()
	req := webMetalWake(t, base, "sbx-web-browser")
	req.Browser = true
	begun := time.Now()
	inst, err := m.WakeSandbox(ctx, req)
	if err != nil {
		t.Fatalf("wake a browser session: %v", err)
	}
	t.Cleanup(func() { _ = m.Destroy(context.Background(), req.Instance) })
	t.Logf("browser session ready in %d ms", time.Since(begun).Milliseconds())

	// The session's ready command passed, so DevTools answers in the guest.
	if out, code := userExec(t, m, req.Instance, req.SandboxID, `wget -qO- http://127.0.0.1:9222/json/version`); code != 0 || !strings.Contains(out, "Browser") {
		t.Fatalf("DevTools on 9222 (exit %d): %q", code, out)
	}

	fetch := func(url string) SandboxTaskResult {
		t.Helper()
		res, err := m.RunSandboxTask(ctx, req.Instance, req.SandboxID,
			[]string{"gregale-browser", "fetch", "--max-chars", "2000", "--timeout", "30", "--", url}, 40*time.Second, 64<<10)
		if err != nil {
			t.Fatalf("fetch %s: %v", url, err)
		}
		return res
	}
	// Cloudflare's trace page answers on the address itself: the test has no
	// resolver, so the page must not redirect to a name.
	const page = "https://1.1.1.1/cdn-cgi/trace"
	// Not granted yet: the page cannot load.
	if res := fetch(page); res.ExitCode == 0 && strings.Contains(string(res.Output), "fl=") {
		t.Fatalf("the browser read an ungranted address: %s", res.Output)
	}
	if err := m.AllowResolvedEgressNamed(ctx, inst.Net.HostIP, resolvedOwner(t, m, inst.Net.HostIP), "one.one.one.one",
		[]netip.Addr{netip.MustParseAddr("1.1.1.1")}, time.Minute, 0); err != nil {
		t.Fatal(err)
	}
	res := fetch(page)
	if res.ExitCode != 0 || res.TimedOut {
		t.Fatalf("fetch after the grant: exit %d timed out %v: %s", res.ExitCode, res.TimedOut, res.Output)
	}
	var read struct {
		URL  string `json:"url"`
		Text string `json:"text"`
	}
	if err := json.Unmarshal(res.Output, &read); err != nil || !strings.Contains(read.Text, "fl=") {
		t.Fatalf("fetch output %q: %v", res.Output, err)
	}
	t.Logf("fetched %s: %d chars", read.URL, len(read.Text))
	if err := m.Destroy(ctx, req.Instance); err != nil {
		t.Fatal(err)
	}
}
