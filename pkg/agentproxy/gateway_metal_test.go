//go:build metal

// adr: 20261117185346 — an agent VM's traffic leaves a node only through the
// agent tunnel, from its own tunnel address; the gateway sends web ports
// through agentproxyd and everything else straight out, both from the same
// agent public address; and losing the tunnel fails closed.
//
// This drives real packets through vmmd's rendered host policy and routing
// rules on a "node" namespace, the gateway role's rendered nftables template
// on a "gateway" namespace, and the agentproxyd binary. The tunnel is
// WireGuard when the wg tool is on PATH, else a veth named like the tunnel:
// everything the rules match on (interface names, marks, tables, addresses)
// is the same either way.
package agentproxy_test

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/agentproxy"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/netns"
)

const (
	gwTunnelCIDR = "10.77.0.0/16"
	gwRouteTable = "51821"
	gwInternet   = "45.0.0.10"
	gwAgentVM    = "10.100.0.9"
	gwTenantVM   = "10.100.0.10"
	gwAccount    = "acct-metal"
	gwSandbox    = "sb-metal"
)

var gwAddresses = []netip.Addr{netip.MustParseAddr("45.0.0.50"), netip.MustParseAddr("45.0.0.51")}

// gwServer listens on the internet host and records each connection's
// port, peer address and first line, one JSON object per line. A request
// whose first bytes name a "hold." host is kept open without an answer
// until the other side closes, so it stays counted against the proxy's caps.
const gwServer = `
import json, resource, socket, sys, threading, time
soft, hard = resource.getrlimit(resource.RLIMIT_NOFILE)
resource.setrlimit(resource.RLIMIT_NOFILE, (hard, hard))
log = open(sys.argv[1], "a", buffering=1)
def hold(c):
    # Keep it open until the other side closes, then close too.
    c.settimeout(None)
    try:
        while c.recv(4096):
            pass
    except OSError:
        pass
    c.close()
def serve(port):
    s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    s.bind((sys.argv[2], port)); s.listen(1024)
    while True:
        c, peer = s.accept()
        c.settimeout(2)
        try:
            data = c.recv(4096)
        except OSError:
            data = b""
        log.write(json.dumps({"port": port, "peer": peer[0], "first": data[:200].decode("latin-1")}) + "\n")
        if b"Host: hold." in data:
            threading.Thread(target=hold, args=(c,), daemon=True).start()
            continue
        try:
            c.sendall(("peer " + peer[0] + "\n").encode()); c.close()
        except OSError:
            pass
for p in (25, 80, 443, 8080):
    threading.Thread(target=serve, args=(p,), daemon=True).start()
print("ready", flush=True)
time.sleep(600)
`

// gwClient makes one connection and prints what the server said, or
// "error <reason>". http and tls send a request naming host.
const gwClient = `
import socket, ssl, sys
kind, ip, port, host = sys.argv[1], sys.argv[2], int(sys.argv[3]), sys.argv[4]
try:
    s = socket.create_connection((ip, port), timeout=3)
    if kind == "http":
        s.sendall(("GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n").encode())
    elif kind == "tls":
        ctx = ssl.SSLContext(ssl.PROTOCOL_TLS_CLIENT); ctx.check_hostname = False; ctx.verify_mode = ssl.CERT_NONE
        obj = ssl.MemoryBIO(); out = ssl.MemoryBIO()
        tls = ctx.wrap_bio(obj, out, server_hostname=host)
        try:
            tls.do_handshake()
        except ssl.SSLWantReadError:
            pass
        s.sendall(out.read())
    else:
        s.sendall(b"hello\n")
    s.settimeout(3)
    data = s.recv(200)
    print(data.decode("latin-1").strip() or "error closed")
except OSError as e:
    print("error", type(e).__name__, e)
`

// gwHolder opens count connections from src, each a request naming host,
// prints "ready" and keeps them open until it is killed.
const gwHolder = `
import socket, sys, time
src, ip, port, count, host = sys.argv[1], sys.argv[2], int(sys.argv[3]), int(sys.argv[4]), sys.argv[5]
conns = []
for _ in range(count):
    s = socket.create_connection((ip, port), timeout=5, source_address=(src, 0))
    s.sendall(("GET / HTTP/1.1\r\nHost: " + host + "\r\n\r\n").encode())
    conns.append(s)
print("ready", flush=True)
time.sleep(600)
`

// gwRegister plays vmmd's registration call from the node.
const gwRegister = `
import json, sys, urllib.request
method, url = sys.argv[1], sys.argv[2]
body = json.dumps({"account_id": sys.argv[3], "sandbox_id": sys.argv[4]}).encode() if method == "PUT" else None
req = urllib.request.Request(url, data=body, method=method, headers={"Content-Type": "application/json"})
try:
    print(urllib.request.urlopen(req, timeout=5).status)
except urllib.error.HTTPError as e:
    print(e.code, e.read().decode()[:200])
`

// gwRender renders the gateway role's nftables template the way Ansible
// does, with the two Ansible filters it uses.
const gwRender = `
import jinja2, json, re, sys
env = jinja2.Environment(undefined=jinja2.StrictUndefined, keep_trailing_newline=True)
env.filters["regex_replace"] = lambda s, p, r: re.sub(p, r, s)
env.filters["bool"] = lambda v: v if isinstance(v, bool) else str(v).lower() in ("1", "true", "yes", "on")
print(env.from_string(open(sys.argv[1]).read()).render(**json.load(open(sys.argv[2]))), end="")
`

type gwTopo struct {
	t                                        *testing.T
	dir                                      string
	vm1, vm2, node, gw, inet, tgw            string
	slice                                    netip.Prefix
	nodeAddr, gwAddr, agentIP, wantEgress    netip.Addr
	inetLog, tenantLog, proxyLog, proxyBlock string
	proxy                                    *exec.Cmd
	// extra are more agent VMs, as further addresses on vm1's interface,
	// each with its own account.
	extra []netns.AgentEgressRule
}

func gwRun(t *testing.T, argv ...string) string {
	t.Helper()
	out, err := exec.Command(argv[0], argv[1:]...).CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %v\n%s", argv, err, out)
	}
	return string(out)
}

func (g *gwTopo) in(ns string, argv ...string) string {
	g.t.Helper()
	return gwRun(g.t, append([]string{"ip", "netns", "exec", ns}, argv...)...)
}

func (g *gwTopo) script(name, body string) string {
	g.t.Helper()
	p := filepath.Join(g.dir, name)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		g.t.Fatal(err)
	}
	return p
}

func gwRequire(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root for network namespaces")
	}
	for _, bin := range []string{"ip", "nft", "python3"} {
		if _, err := exec.LookPath(bin); err != nil {
			t.Skipf("%s not on PATH", bin)
		}
	}
	if out, err := exec.Command("python3", "-c", "import jinja2").CombinedOutput(); err != nil {
		t.Skipf("python3 jinja2 is needed to render the gateway template: %v\n%s", err, out)
	}
}

// agentproxydBinary is FAAS_TEST_AGENTPROXYD_BINARY, else a fresh build.
func agentproxydBinary(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("FAAS_TEST_AGENTPROXYD_BINARY"); p != "" {
		return p
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("set FAAS_TEST_AGENTPROXYD_BINARY (no go toolchain to build agentproxyd)")
	}
	out := filepath.Join(t.TempDir(), "agentproxyd")
	gwRun(t, "go", "build", "-o", out, "../../cmd/agentproxyd")
	return out
}

func gatewayTemplate(t *testing.T) string {
	if p := os.Getenv("FAAS_TEST_AGENT_GW_TEMPLATE"); p != "" {
		return p
	}
	return "../../deploy/ansible/roles/agent_egress_gateway/templates/agent-egress-gw.nft.j2"
}

func newGatewayTopo(t *testing.T) *gwTopo { return newGatewayTopoWith(t, 0) }

// newGatewayTopoWith adds extra agent VMs beside vm1, registered with the
// proxy under accounts acct-extra-<i>.
func newGatewayTopoWith(t *testing.T, extra int) *gwTopo {
	t.Helper()
	gwRequire(t)
	proxyBin := agentproxydBinary(t)
	g := &gwTopo{t: t, dir: t.TempDir(), vm1: "ag_vm1", vm2: "ag_vm2", node: "ag_node", gw: "ag_gw", inet: "ag_inet", tgw: "ag_tgw"}
	var err error
	if g.slice, g.nodeAddr, err = manifest.AgentNodeSlice(gwTunnelCIDR, manifest.AgentNodePrefixDefault, "metal-node-3"); err != nil {
		t.Fatal(err)
	}
	gwPrefix, err := manifest.AgentGatewayAddress(gwTunnelCIDR)
	if err != nil {
		t.Fatal(err)
	}
	g.gwAddr = gwPrefix.Addr()
	g.agentIP = g.nodeAddr.Next() // vmmd's first allocation: past the network and node addresses
	g.wantEgress = agentproxy.AddressFor(gwAddresses, g.agentIP)
	agentIP := g.agentIP
	for i := range extra {
		agentIP = agentIP.Next()
		g.extra = append(g.extra, netns.AgentEgressRule{PerVMHostIP: netip.AddrFrom4([4]byte{10, 100, 1, byte(i + 1)}),
			AgentIP: agentIP, AccountID: "acct-extra-" + strconv.Itoa(i), SandboxID: "sb-extra-" + strconv.Itoa(i)})
	}
	g.inetLog, g.tenantLog = filepath.Join(g.dir, "inet.jsonl"), filepath.Join(g.dir, "tenant.jsonl")
	g.proxyLog, g.proxyBlock = filepath.Join(g.dir, "agentproxyd.log"), filepath.Join(g.dir, "blocklist.txt")

	for _, ns := range []string{g.vm1, g.vm2, g.node, g.gw, g.inet, g.tgw} {
		ns := ns
		_, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput()
		gwRun(t, "ip", "netns", "add", ns)
		t.Cleanup(func() { _, _ = exec.Command("ip", "netns", "del", ns).CombinedOutput() })
		gwRun(t, "ip", "-n", ns, "link", "set", "lo", "up")
	}
	for _, ns := range []string{g.node, g.gw} {
		g.in(ns, "sysctl", "-qw", "net.ipv4.ip_forward=1")
	}

	// Two VMs on the node's tenant bridge.
	gwRun(t, "ip", "-n", g.node, "link", "add", netns.TenantBridge, "type", "bridge")
	gwRun(t, "ip", "-n", g.node, "addr", "add", "10.100.0.1/16", "dev", netns.TenantBridge)
	gwRun(t, "ip", "-n", g.node, "link", "set", netns.TenantBridge, "up")
	for i, vm := range []struct{ ns, ip string }{{g.vm1, gwAgentVM}, {g.vm2, gwTenantVM}} {
		host := "agvh" + strconv.Itoa(i)
		gwRun(t, "ip", "link", "add", host, "netns", g.node, "type", "veth", "peer", "name", "eth0", "netns", vm.ns)
		gwRun(t, "ip", "-n", g.node, "link", "set", host, "master", netns.TenantBridge, "up")
		gwRun(t, "ip", "-n", vm.ns, "addr", "add", vm.ip+"/16", "dev", "eth0")
		gwRun(t, "ip", "-n", vm.ns, "link", "set", "eth0", "up")
		gwRun(t, "ip", "-n", vm.ns, "route", "add", "default", "via", "10.100.0.1")
	}
	for _, r := range g.extra {
		gwRun(t, "ip", "-n", g.vm1, "addr", "add", r.PerVMHostIP.String()+"/16", "dev", "eth0")
	}

	// The tenant path: wg-tenant to a tenant gateway that also answers for
	// the internet host, so a leak onto it is seen.
	gwRun(t, "ip", "link", "add", "wg-tenant", "netns", g.node, "type", "veth", "peer", "name", "wg-tenant", "netns", g.tgw)
	gwRun(t, "ip", "-n", g.node, "addr", "add", "10.200.0.2/30", "dev", "wg-tenant")
	gwRun(t, "ip", "-n", g.tgw, "addr", "add", "10.200.0.1/30", "dev", "wg-tenant")
	gwRun(t, "ip", "-n", g.tgw, "addr", "add", gwInternet+"/32", "dev", "lo")
	for _, ns := range []string{g.node, g.tgw} {
		gwRun(t, "ip", "-n", ns, "link", "set", "wg-tenant", "up")
	}
	gwRun(t, "ip", "-n", g.node, "route", "add", "default", "via", "10.200.0.1", "dev", "wg-tenant")
	gwRun(t, "ip", "-n", g.tgw, "route", "add", "10.100.0.0/16", "via", "10.200.0.2")

	g.agentTunnel()

	// The gateway's public side and the internet host.
	gwRun(t, "ip", "link", "add", "pub0", "netns", g.gw, "type", "veth", "peer", "name", "eth0", "netns", g.inet)
	gwRun(t, "ip", "-n", g.gw, "addr", "add", "45.0.0.1/24", "dev", "pub0")
	for _, a := range gwAddresses {
		gwRun(t, "ip", "-n", g.gw, "addr", "add", a.String()+"/32", "dev", "pub0")
	}
	gwRun(t, "ip", "-n", g.inet, "addr", "add", gwInternet+"/24", "dev", "eth0")
	for _, pair := range [][2]string{{g.gw, "pub0"}, {g.inet, "eth0"}} {
		gwRun(t, "ip", "-n", pair[0], "link", "set", pair[1], "up")
	}

	g.nodePolicy()
	g.gatewayPolicy()
	g.startServers()
	g.startProxy(proxyBin)
	t.Cleanup(g.dumpOnFailure)
	return g
}

// dumpOnFailure logs both hosts' routing and rulesets with their counters,
// and agentproxyd's output, when the test failed.
func (g *gwTopo) dumpOnFailure() {
	if !g.t.Failed() {
		return
	}
	for _, c := range [][]string{
		{g.node, "ip", "-4", "rule"}, {g.node, "ip", "-4", "route", "show", "table", "all"}, {g.node, "ip", "-s", "link"},
		{g.node, "nft", "list", "ruleset"}, {g.gw, "ip", "-4", "route"}, {g.gw, "ip", "-s", "link"}, {g.gw, "nft", "list", "ruleset"},
	} {
		out, _ := exec.Command("ip", append([]string{"netns", "exec"}, c...)...).CombinedOutput()
		g.t.Logf("%s: %v\n%s", c[0], c[1:], out)
	}
	g.t.Logf("agentproxyd:\n%s", g.read(g.proxyLog))
}

// agentTunnel builds wg-agent the way the agent_egress_client and
// agent_egress_gateway roles do: the node's address and the gateway route on
// the node, the default route only in the agent table, the node's slice
// routed back on the gateway.
func (g *gwTopo) agentTunnel() {
	t := g.t
	if _, err := exec.LookPath("wg"); err == nil {
		g.wireGuardTunnel()
	} else {
		t.Log("wg not on PATH: the agent tunnel is a veth named wg-agent")
		gwRun(t, "ip", "link", "add", "wg-agent", "netns", g.node, "type", "veth", "peer", "name", "wg-agent", "netns", g.gw)
		for _, ns := range []string{g.node, g.gw} {
			// A tunnel resolves no neighbours. A NOARP link sends every
			// frame to its own MAC, so both ends share one, and the peer
			// takes each frame as its own, as the tunnel would deliver it.
			gwRun(t, "ip", "-n", ns, "link", "set", "wg-agent", "address", "02:00:00:77:00:01", "arp", "off", "mtu", "1420", "up")
		}
		gwRun(t, "ip", "-n", g.node, "addr", "add", g.nodeAddr.String()+"/32", "dev", "wg-agent")
		gwRun(t, "ip", "-n", g.gw, "addr", "add", g.gwAddr.String()+"/32", "dev", "wg-agent")
		gwRun(t, "ip", "-n", g.gw, "route", "add", g.slice.String(), "dev", "wg-agent")
	}
	g.in(g.node, "sysctl", "-qw", "net.ipv4.conf.wg-agent.rp_filter=2")
	gwRun(t, "ip", "-n", g.node, "route", "replace", "default", "dev", "wg-agent", "table", gwRouteTable)
	gwRun(t, "ip", "-n", g.node, "route", "replace", g.gwAddr.String()+"/32", "dev", "wg-agent", "src", g.nodeAddr.String())
}

func (g *gwTopo) wireGuardTunnel() {
	t := g.t
	key := func() (string, string) {
		priv := strings.TrimSpace(gwRun(t, "wg", "genkey"))
		cmd := exec.Command("wg", "pubkey")
		cmd.Stdin = strings.NewReader(priv)
		pub, err := cmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		p := filepath.Join(g.dir, "k"+strconv.FormatInt(time.Now().UnixNano(), 36))
		if err := os.WriteFile(p, []byte(priv), 0o600); err != nil {
			t.Fatal(err)
		}
		return p, strings.TrimSpace(string(pub))
	}
	nodeKey, nodePub := key()
	gwKey, gwPub := key()
	// The underlay between the two.
	gwRun(t, "ip", "link", "add", "ul0", "netns", g.node, "type", "veth", "peer", "name", "ul0", "netns", g.gw)
	gwRun(t, "ip", "-n", g.node, "addr", "add", "192.0.2.2/30", "dev", "ul0")
	gwRun(t, "ip", "-n", g.gw, "addr", "add", "192.0.2.1/30", "dev", "ul0")
	for _, ns := range []string{g.node, g.gw} {
		gwRun(t, "ip", "-n", ns, "link", "set", "ul0", "up")
		gwRun(t, "ip", "-n", ns, "link", "add", "wg-agent", "type", "wireguard")
	}
	g.in(g.gw, "wg", "set", "wg-agent", "private-key", gwKey, "listen-port", "51821", "peer", nodePub, "allowed-ips", g.slice.String())
	g.in(g.node, "wg", "set", "wg-agent", "private-key", nodeKey, "peer", gwPub, "endpoint", "192.0.2.1:51821",
		"allowed-ips", "0.0.0.0/0", "persistent-keepalive", "25")
	gwRun(t, "ip", "-n", g.node, "addr", "add", g.nodeAddr.String()+"/32", "dev", "wg-agent")
	gwRun(t, "ip", "-n", g.gw, "addr", "add", g.gwAddr.String()+"/32", "dev", "wg-agent")
	for _, ns := range []string{g.node, g.gw} {
		gwRun(t, "ip", "-n", ns, "link", "set", "wg-agent", "mtu", "1420", "up")
	}
	gwRun(t, "ip", "-n", g.gw, "route", "add", g.slice.String(), "dev", "wg-agent")
}

// nodePolicy loads vmmd's host policy with vm1 as a live agent VM, and adds
// the two routing rules vmmd keeps (ensureAgentEgressRouting).
func (g *gwTopo) nodePolicy() {
	t := g.t
	p := netns.DefaultHostPolicy
	p.TenantEgressIface = "wg-tenant"
	p.AgentEgressIface = "wg-agent"
	p.AgentEgressRules = []netns.AgentEgressRule{{PerVMHostIP: netip.MustParseAddr(gwAgentVM), AgentIP: g.agentIP,
		AccountID: gwAccount, SandboxID: gwSandbox}}
	p.AgentEgressRules = append(p.AgentEgressRules, g.extra...)
	g.in(g.node, "nft", "-f", g.script("node.nft", p.Render()))
	mark := fmt.Sprintf("%#x/%#x", netns.AgentEgressRoutingMark, netns.AgentEgressRoutingMark)
	gwRun(t, "ip", "-n", g.node, "-4", "rule", "add", "priority", "95", "iif", netns.TenantBridge, "fwmark", mark, "lookup", gwRouteTable)
	gwRun(t, "ip", "-n", g.node, "-4", "rule", "add", "priority", "96", "iif", netns.TenantBridge, "fwmark", mark, "unreachable")
}

func (g *gwTopo) gatewayPolicy() {
	t := g.t
	var addrs []string
	for _, a := range gwAddresses {
		addrs = append(addrs, a.String())
	}
	vars, err := json.Marshal(map[string]any{
		"ansible_managed":                        "metal test",
		"faas_agent_egress_iface":                "wg-agent",
		"faas_agent_egress_gateway_address":      g.gwAddr.String() + "/16",
		"faas_agent_egress_gateway_public_iface": "pub0",
		"faas_agent_egress_tunnel_cidr":          gwTunnelCIDR,
		"faas_agent_egress_addresses":            addrs,
		"faas_agentproxyd_enabled":               true,
		"faas_agentproxyd_http_port":             15080,
		"faas_agentproxyd_tls_port":              15443,
		"faas_agentproxyd_registration_port":     15081,
	})
	if err != nil {
		t.Fatal(err)
	}
	rendered := gwRun(t, "python3", g.script("render.py", gwRender), gatewayTemplate(t), g.script("vars.json", string(vars)))
	// The template deletes its table before loading; the first load has none.
	g.in(g.gw, "nft", "add", "table", "inet", "faas_agent_gw")
	g.in(g.gw, "nft", "-f", g.script("gateway.nft", rendered))
}

func (g *gwTopo) startServers() {
	t := g.t
	server := g.script("server.py", gwServer)
	for _, s := range []struct{ ns, log string }{{g.inet, g.inetLog}, {g.tgw, g.tenantLog}} {
		cmd := exec.Command("ip", "netns", "exec", s.ns, "python3", server, s.log, gwInternet)
		out, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		cmd.Stderr = os.Stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
		if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "ready" {
			t.Fatalf("server in %s did not start: %q", s.ns, line)
		}
	}
}

func (g *gwTopo) startProxy(bin string) {
	t := g.t
	if err := os.WriteFile(g.proxyBlock, []byte("blocked.test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	logf, err := os.Create(g.proxyLog)
	if err != nil {
		t.Fatal(err)
	}
	var addrs []string
	for _, a := range gwAddresses {
		addrs = append(addrs, a.String())
	}
	gw := g.gwAddr.String()
	g.proxy = exec.Command("ip", "netns", "exec", g.gw, bin,
		"-tunnel-cidr", gwTunnelCIDR, "-node-prefix", strconv.Itoa(manifest.AgentNodePrefixDefault),
		"-addresses", strings.Join(addrs, ","),
		"-http-listen", gw+":15080", "-tls-listen", gw+":15443", "-registration-listen", gw+":15081",
		"-metrics-listen", "127.0.0.1:9473", "-blocklist", g.proxyBlock)
	g.proxy.Stdout, g.proxy.Stderr = logf, logf
	if err := g.proxy.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.proxy.Process.Kill(); _ = g.proxy.Wait(); _ = logf.Close() })
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(g.read(g.proxyLog), "agentproxyd: serving") {
		if time.Now().After(deadline) {
			t.Fatalf("agentproxyd did not start:\n%s", g.read(g.proxyLog))
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func (g *gwTopo) read(p string) string {
	b, _ := os.ReadFile(p)
	return string(b)
}

func (g *gwTopo) register(method string) string {
	return g.registerAs(method, g.agentIP, gwAccount, gwSandbox)
}

func (g *gwTopo) registerAs(method string, agentIP netip.Addr, account, sandbox string) string {
	url := "http://" + g.gwAddr.String() + ":15081/v1/registrations/" + agentIP.String()
	return strings.TrimSpace(g.in(g.node, "python3", g.script("register.py", gwRegister), method, url, account, sandbox))
}

// hold opens count held connections from src to port 80 naming host
// (which must start with "hold.") and waits until the internet host has
// them all. It returns a func that closes them.
func (g *gwTopo) hold(src netip.Addr, count int, host string) func() {
	t := g.t
	t.Helper()
	before := g.countSeen(host)
	cmd := exec.Command("ip", "netns", "exec", g.vm1, "python3", g.script("holder.py", gwHolder),
		src.String(), gwInternet, "80", strconv.Itoa(count), host)
	out, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	stop := func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }
	t.Cleanup(stop)
	if line, _ := bufio.NewReader(out).ReadString('\n'); strings.TrimSpace(line) != "ready" {
		t.Fatalf("holder from %s for %s did not open its connections: %q", src, host, line)
	}
	deadline := time.Now().Add(30 * time.Second)
	for g.countSeen(host)-before < count {
		if time.Now().After(deadline) {
			t.Fatalf("internet host has %d of %d held connections for %s from %s", g.countSeen(host)-before, count, host, src)
		}
		time.Sleep(100 * time.Millisecond)
	}
	return stop
}

// countSeen counts the internet host's connections whose request named host.
func (g *gwTopo) countSeen(host string) int {
	n := 0
	for _, s := range g.seen(g.inetLog) {
		if strings.Contains(s.First, "Host: "+host+"\r") {
			n++
		}
	}
	return n
}

// connectFrom makes one plain-HTTP request from src naming host.
func (g *gwTopo) connectFrom(src netip.Addr, host string) string {
	const client = `
import socket, sys
try:
    s = socket.create_connection((sys.argv[2], 80), timeout=3, source_address=(sys.argv[1], 0))
    s.sendall(("GET / HTTP/1.1\r\nHost: " + sys.argv[3] + "\r\n\r\n").encode())
    s.settimeout(3)
    print(s.recv(200).decode("latin-1").strip() or "error closed")
except OSError as e:
    print("error", type(e).__name__, e)
`
	return strings.TrimSpace(g.in(g.vm1, "python3", g.script("client_from.py", client), src.String(), gwInternet, host))
}

// refusal waits for a connection record from agentIP with outcome.
func (g *gwTopo) refusal(agentIP netip.Addr, outcome string) map[string]any {
	deadline := time.Now().Add(5 * time.Second)
	for {
		for _, r := range g.connLog() {
			if r["outcome"] == outcome && strings.HasPrefix(fmt.Sprint(r["source"]), agentIP.String()+":") {
				return r
			}
		}
		if time.Now().After(deadline) {
			g.t.Fatalf("no %s record from %s in the connection log: %v", outcome, agentIP, g.connLog())
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func (g *gwTopo) connect(vm, kind string, port int, host string) string {
	return strings.TrimSpace(g.in(vm, "python3", g.script("client.py", gwClient), kind, gwInternet, strconv.Itoa(port), host))
}

type gwSeen struct {
	Port  int    `json:"port"`
	Peer  string `json:"peer"`
	First string `json:"first"`
}

func (g *gwTopo) seen(p string) []gwSeen {
	var out []gwSeen
	for _, line := range strings.Split(strings.TrimSpace(g.read(p)), "\n") {
		if line == "" {
			continue
		}
		var s gwSeen
		if err := json.Unmarshal([]byte(line), &s); err != nil {
			g.t.Fatalf("server log line %q: %v", line, err)
		}
		out = append(out, s)
	}
	return out
}

// connLog returns agentproxyd's connection records.
func (g *gwTopo) connLog() []map[string]any {
	var out []map[string]any
	for _, line := range strings.Split(g.read(g.proxyLog), "\n") {
		var rec map[string]any
		if json.Unmarshal([]byte(line), &rec) == nil && rec["msg"] == agentproxy.ConnLogMessage {
			out = append(out, rec)
		}
	}
	return out
}

func (g *gwTopo) waitConnLog(n int) []map[string]any {
	deadline := time.Now().Add(5 * time.Second)
	for {
		recs := g.connLog()
		if len(recs) >= n || time.Now().After(deadline) {
			return recs
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestMetalAgentEgressGatewayPath(t *testing.T) {
	g := newGatewayTopo(t)
	want := "peer " + g.wantEgress.String()

	// Unregistered, the proxy refuses web ports; the forwarded path does not
	// consult registrations.
	if got := g.connect(g.vm1, "tls", 443, "example.test"); strings.HasPrefix(got, "peer") {
		t.Fatalf("unregistered agent VM reached port 443: %q", got)
	}
	if recs := g.waitConnLog(1); len(recs) != 1 || recs[0]["outcome"] != agentproxy.OutcomeUnregistered {
		t.Fatalf("unregistered connection log = %v", recs)
	}

	if got := g.register("PUT"); got != "200" {
		t.Fatalf("registration from the node = %q, want 200", got)
	}

	for _, c := range []struct {
		kind string
		port int
	}{{"http", 80}, {"tls", 443}, {"raw", 8080}} {
		if got := g.connect(g.vm1, c.kind, c.port, "example.test"); got != want {
			t.Errorf("agent VM %s port %d: got %q, want %q (its agent address)", c.kind, c.port, got, want)
		}
	}
	recs := g.waitConnLog(3)
	accepted := 0
	for _, r := range recs {
		if r["outcome"] != agentproxy.OutcomeAccepted {
			continue
		}
		accepted++
		if r["sandbox_id"] != gwSandbox || r["account_id"] != gwAccount || r["host"] != "example.test" ||
			r["egress"] != g.wantEgress.String() || !strings.HasPrefix(fmt.Sprint(r["source"]), g.agentIP.String()+":") {
			t.Errorf("connection record does not name the session and its address: %v", r)
		}
	}
	if accepted != 2 {
		t.Errorf("accepted proxied connections = %d, want 2 (80 and 443): %v", accepted, recs)
	}

	if got := g.connect(g.vm1, "http", 80, "blocked.test"); strings.HasPrefix(got, "peer") {
		t.Errorf("blocklisted domain reached: %q", got)
	}
	if got := g.connect(g.vm1, "raw", 25, ""); strings.HasPrefix(got, "peer") {
		t.Errorf("agent VM reached SMTP: %q", got)
	}

	// A VM that is not an agent VM keeps the tenant path.
	if got := g.connect(g.vm2, "raw", 8080, ""); got != "peer 10.200.0.2" {
		t.Errorf("tenant VM: got %q, want the tenant gateway path (peer 10.200.0.2)", got)
	}
	for _, s := range g.seen(g.tenantLog) {
		if s.Port != 8080 || s.Peer != "10.200.0.2" {
			t.Errorf("agent traffic leaked onto the tenant path: %+v", s)
		}
	}
	for _, s := range g.seen(g.inetLog) {
		if s.Peer != g.wantEgress.String() {
			t.Errorf("internet host saw %+v, want every connection from %s", s, g.wantEgress)
		}
		if s.Port == 25 || strings.Contains(s.First, "blocked.test") {
			t.Errorf("a denied connection reached the internet host: %+v", s)
		}
	}

	// Losing the tunnel fails closed: no route, and nothing on the tenant path.
	before := len(g.seen(g.tenantLog))
	gwRun(t, "ip", "-n", g.node, "link", "set", "wg-agent", "down")
	for _, port := range []int{443, 8080} {
		if got := g.connect(g.vm1, "raw", port, ""); strings.HasPrefix(got, "peer") {
			t.Errorf("agent VM reached port %d with the tunnel down: %q", port, got)
		}
	}
	if after := len(g.seen(g.tenantLog)); after != before {
		t.Errorf("with the tunnel down, agent traffic took the tenant path: %v", g.seen(g.tenantLog)[before:])
	}

	gwRun(t, "ip", "-n", g.node, "link", "set", "wg-agent", "up")
	gwRun(t, "ip", "-n", g.node, "route", "replace", "default", "dev", "wg-agent", "table", gwRouteTable)
	gwRun(t, "ip", "-n", g.node, "route", "replace", g.gwAddr.String()+"/32", "dev", "wg-agent", "src", g.nodeAddr.String())
	if got := g.register("DELETE"); got != "204" {
		t.Fatalf("unregister = %q, want 204", got)
	}
	if got := g.connect(g.vm1, "http", 80, "example.test"); strings.HasPrefix(got, "peer") {
		t.Errorf("after unregistering, the proxy still carried the VM: %q", got)
	}
	if got := g.connect(g.vm1, "raw", 8080, ""); got != want {
		t.Errorf("tunnel back up, forwarded port: got %q, want %q", got, want)
	}
}

func TestMetalAgentEgressGatewayRefusesOtherNodesRegistrations(t *testing.T) {
	g := newGatewayTopo(t)
	// An address outside the calling node's slice.
	other := netip.MustParseAddr("10.77.200.2")
	url := "http://" + g.gwAddr.String() + ":15081/v1/registrations/" + other.String()
	got := strings.TrimSpace(g.in(g.node, "python3", g.script("register.py", gwRegister), "PUT", url, gwAccount, gwSandbox))
	if strings.HasPrefix(got, "200") {
		t.Fatalf("node registered an address in another node's slice: %q", got)
	}
}

// TestMetalAgentEgressGatewayCaps fills each of agentproxyd's
// concurrent-connection caps (ADR-20261117185346 §4, pkg/api/limits.go)
// with held connections through the real path, and checks that the next
// connection is refused with that cap's outcome while others still pass.
func TestMetalAgentEgressGatewayCaps(t *testing.T) {
	perVM, perAccountDomain, perDomain := api.AgentEgressConnsPerVM, api.AgentEgressConnsPerAccountDomain, api.AgentEgressConnsPerDomainFleet
	if perVM%perAccountDomain != 0 || perDomain%perAccountDomain != 0 {
		t.Fatalf("caps %d/%d/%d: the test fills the larger caps in whole per-account-domain batches", perVM, perAccountDomain, perDomain)
	}
	// One VM for the per-VM and per-account-domain caps; enough accounts
	// to fill the fleet cap one domain at a time, and one more to be refused.
	fleet := perDomain/perAccountDomain + 1
	g := newGatewayTopoWith(t, fleet)
	if got := g.register("PUT"); got != "200" {
		t.Fatalf("registration = %q, want 200", got)
	}
	for _, r := range g.extra {
		if got := g.registerAs("PUT", r.AgentIP, r.AccountID, r.SandboxID); got != "200" {
			t.Fatalf("registration of %s = %q, want 200", r.AgentIP, got)
		}
	}
	vm := netip.MustParseAddr(gwAgentVM)
	answered := func(got string) bool { return strings.HasPrefix(got, "peer ") }

	// Per account and domain: a full batch on one domain refuses the next
	// connection to it, and another domain still passes.
	vmHolds := []func(){g.hold(vm, perAccountDomain, "hold.d0.test")}
	if got := g.connectFrom(vm, "www.d0.test"); answered(got) {
		t.Fatalf("connection %d from one account to d0.test was carried: %q", perAccountDomain+1, got)
	}
	if r := g.refusal(g.agentIP, agentproxy.OutcomeCapAccountDomain); r["host"] != "www.d0.test" || r["account_id"] != gwAccount {
		t.Errorf("cap_account_domain record = %v", r)
	}
	if got := g.connectFrom(vm, "www.other.test"); !answered(got) {
		t.Fatalf("another domain was refused while only d0.test is at its cap: %q", got)
	}

	// Per VM: fill the rest of the VM's cap across more domains; the next
	// connection, to a fresh domain, is refused.
	for i := 1; i < perVM/perAccountDomain; i++ {
		vmHolds = append(vmHolds, g.hold(vm, perAccountDomain, "hold.d"+strconv.Itoa(i)+".test"))
	}
	if got := g.connectFrom(vm, "www.fresh.test"); answered(got) {
		t.Fatalf("connection %d from one VM was carried: %q", perVM+1, got)
	}
	if r := g.refusal(g.agentIP, agentproxy.OutcomeCapVM); r["host"] != "www.fresh.test" {
		t.Errorf("cap_vm record = %v", r)
	}

	// Per domain, fleet-wide: accounts each fill their own share of one
	// domain until the fleet cap; one more account is refused there and
	// still reaches another domain.
	for _, r := range g.extra[:fleet-1] {
		g.hold(r.PerVMHostIP, perAccountDomain, "hold.busy.test")
	}
	last := g.extra[fleet-1]
	if got := g.connectFrom(last.PerVMHostIP, "www.busy.test"); answered(got) {
		t.Fatalf("connection %d to busy.test across the fleet was carried: %q", perDomain+1, got)
	}
	if r := g.refusal(last.AgentIP, agentproxy.OutcomeCapDomainFleet); r["account_id"] != last.AccountID || r["host"] != "www.busy.test" {
		t.Errorf("cap_domain_fleet record = %v", r)
	}
	if got := g.connectFrom(last.PerVMHostIP, "www.quiet.test"); !answered(got) {
		t.Fatalf("an account under every cap was refused another domain: %q", got)
	}

	// Closed connections give their slots back.
	for _, stop := range vmHolds {
		stop()
	}
	deadline := time.Now().Add(10 * time.Second)
	for got := g.connectFrom(vm, "www.fresh.test"); !answered(got); got = g.connectFrom(vm, "www.fresh.test") {
		if time.Now().After(deadline) {
			t.Fatalf("the VM closed its connections, yet is still refused: %q", got)
		}
		time.Sleep(200 * time.Millisecond)
	}
}
