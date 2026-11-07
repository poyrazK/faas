// adr: 20261102081618
package main

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/edgecert"
	"github.com/onebox-faas/faas/pkg/edgedns"
	"github.com/onebox-faas/faas/pkg/state"
)

// withCert returns env with a complete certificate configuration, then
// overrides applied.
func withCert(env, overrides map[string]string) map[string]string {
	out := map[string]string{
		"FAAS_EDGEDNS_CERT_NAMES":            "gregale.dev, *.Gregale.dev",
		"FAAS_EDGEDNS_CERT_ZONE_ID":          "zone-1",
		"FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE": "/run/credentials/faas-edgednsd.service/acme_account_key",
		"FAAS_EDGEDNS_CERT_RECIPIENT_FILE":   "/run/credentials/faas-edgednsd.service/edge_tls_recipient",
	}
	for k, v := range env {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

// withWatch returns env with the transparency settings in overrides.
func withWatch(env, overrides map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range env {
		out[k] = v
	}
	for k, v := range overrides {
		out[k] = v
	}
	return out
}

func TestConfigFromEnv(t *testing.T) {
	base := map[string]string{
		"FAAS_EDGEDNS_NAMES":          "zone-1:*.Gregale.dev, zone-1:pilot.gregale.dev",
		"FAAS_EDGEDNS_NODE_ADDRESSES": "gregale-compute-1=203.0.113.1,gregale-compute-2=203.0.113.2",
		"FAAS_EDGEDNS_CF_TOKEN_FILE":  "/run/credentials/faas-edgednsd.service/cf_token",
	}
	with := func(k, v string) map[string]string {
		env := map[string]string{}
		for key, val := range base {
			env[key] = val
		}
		env[k] = v
		return env
	}
	for _, tc := range []struct {
		name    string
		env     map[string]string
		wantErr string
		check   func(t *testing.T, c config)
	}{
		{name: "defaults", env: base, check: func(t *testing.T, c config) {
			if !c.dryRun || c.interval != 2*time.Second || c.edgePort != "9109" || c.metricsAddr != "127.0.0.1:9113" {
				t.Fatalf("defaults = %+v", c)
			}
			if len(c.names) != 2 || c.names[0] != (edgedns.Name{ZoneID: "zone-1", Host: "*.gregale.dev"}) {
				t.Fatalf("names = %+v", c.names)
			}
			if c.public["gregale-compute-2"] != netip.MustParseAddr("203.0.113.2") {
				t.Fatalf("public = %v", c.public)
			}
		}},
		{name: "live", env: with("FAAS_EDGEDNS_DRY_RUN", "0"), check: func(t *testing.T, c config) {
			if c.dryRun {
				t.Fatal("FAAS_EDGEDNS_DRY_RUN=0 left dry run on")
			}
		}},
		{name: "anything else stays dry", env: with("FAAS_EDGEDNS_DRY_RUN", "false"), check: func(t *testing.T, c config) {
			if !c.dryRun {
				t.Fatal("only 0 turns dry run off")
			}
		}},
		{name: "modes", env: with("FAAS_EDGEDNS_NAMES", "zone-1:*.gregale.dev:proxied,zone-1:pilot.gregale.dev:dns-only"), check: func(t *testing.T, c config) {
			want := []edgedns.Name{{ZoneID: "zone-1", Host: "*.gregale.dev"}, {ZoneID: "zone-1", Host: "pilot.gregale.dev", DNSOnly: true}}
			if len(c.names) != 2 || c.names[0] != want[0] || c.names[1] != want[1] {
				t.Fatalf("names = %+v, want %+v", c.names, want)
			}
		}},
		{name: "an unknown mode", env: with("FAAS_EDGEDNS_NAMES", "zone-1:pilot.gregale.dev:direct"), wantErr: "is not proxied or dns-only"},
		{name: "a name twice", env: with("FAAS_EDGEDNS_NAMES", "zone-1:pilot.gregale.dev,zone-1:Pilot.gregale.dev:dns-only"), wantErr: "twice"},
		{name: "no names", env: with("FAAS_EDGEDNS_NAMES", " "), wantErr: "no host"},
		{name: "a name without a zone", env: with("FAAS_EDGEDNS_NAMES", "*.gregale.dev"), wantErr: "<zone id>:<host>"},
		{name: "a bad address", env: with("FAAS_EDGEDNS_NODE_ADDRESSES", "gregale-compute-1=not-an-ip"), wantErr: "<node>=<address>"},
		{name: "no token", env: with("FAAS_EDGEDNS_CF_TOKEN_FILE", ""), wantErr: "TOKEN_FILE"},
		{name: "too short an interval", env: with("FAAS_EDGEDNS_INTERVAL", "100ms"), wantErr: "at least 1s"},
		{name: "no certificate by default", env: base, check: func(t *testing.T, c config) {
			if c.cert.enabled() {
				t.Fatalf("cert = %+v, want off", c.cert)
			}
		}},
		{name: "certificate", env: withCert(base, nil), check: func(t *testing.T, c config) {
			if !c.cert.enabled() || strings.Join(c.cert.names, ",") != "*.gregale.dev,gregale.dev" || c.cert.ca != edgecert.CALetsEncryptStaging || c.cert.zoneID != "zone-1" {
				t.Fatalf("cert = %+v; want both names, staging first", c.cert)
			}
		}},
		{name: "certificate from production", env: withCert(base, map[string]string{"FAAS_EDGEDNS_ACME_CA": "letsencrypt"}), check: func(t *testing.T, c config) {
			if c.cert.ca != edgecert.CALetsEncrypt {
				t.Fatalf("ca = %q", c.cert.ca)
			}
		}},
		{name: "an unknown CA", env: withCert(base, map[string]string{"FAAS_EDGEDNS_ACME_CA": "zerossl"}), wantErr: "FAAS_EDGEDNS_ACME_CA"},
		{name: "a certificate without its files", env: withCert(base, map[string]string{"FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE": "", "FAAS_EDGEDNS_CERT_ZONE_ID": ""}), wantErr: "FAAS_EDGEDNS_CERT_ZONE_ID, FAAS_EDGEDNS_ACME_ACCOUNT_KEY_FILE"},
		{name: "a bad certificate name", env: withCert(base, map[string]string{"FAAS_EDGEDNS_CERT_NAMES": "*.*.gregale.dev"}), wantErr: "FAAS_EDGEDNS_CERT_NAMES"},
		{name: "certificate settings without names", env: with("FAAS_EDGEDNS_ACME_CA", "letsencrypt"), wantErr: "names no host"},
		{name: "no transparency watch by default", env: base, check: func(t *testing.T, c config) {
			if c.watch.enabled() {
				t.Fatalf("watch = %+v, want off", c.watch)
			}
		}},
		{name: "transparency watch", env: withWatch(base, map[string]string{"FAAS_EDGEDNS_CT_DOMAIN": "Gregale.dev", "FAAS_EDGEDNS_CT_ALLOW_NAMES": "*.gregale.dev, Operations.gregale.dev"}), check: func(t *testing.T, c config) {
			if !c.watch.enabled() || c.watch.domain != "gregale.dev" || strings.Join(c.watch.allow, ",") != "*.gregale.dev,operations.gregale.dev" || c.watch.apiKeyFile != "" {
				t.Fatalf("watch = %+v", c.watch)
			}
		}},
		{name: "a wildcard watch domain", env: withWatch(base, map[string]string{"FAAS_EDGEDNS_CT_DOMAIN": "*.gregale.dev"}), wantErr: "FAAS_EDGEDNS_CT_*"},
		{name: "watch settings without a domain", env: withWatch(base, map[string]string{"FAAS_EDGEDNS_CT_ALLOW_NAMES": "gregale.dev"}), wantErr: "names no domain"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, err := configFromEnv(func(k string) string { return tc.env[k] })
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			tc.check(t, c)
		})
	}
}

type fakeRegistry []state.ComputeNode

func (f fakeRegistry) ListComputeNodes(_ context.Context, includeInactive bool) ([]state.ComputeNode, error) {
	if !includeInactive {
		panic("edgednsd must see drained nodes too")
	}
	return f, nil
}

type fakeProbe struct {
	mu    sync.Mutex
	ready map[string]bool
	urls  []string
}

func (p *fakeProbe) Ready(_ context.Context, url string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.urls = append(p.urls, url)
	return p.ready[url]
}

func TestNodeSourceJoinsRegistryAddressesAndProbes(t *testing.T) {
	probe := &fakeProbe{ready: map[string]bool{"http://10.10.0.3:9109/readyz": true}}
	src := nodeSource{
		registry: fakeRegistry{
			{Name: "gregale-compute-1", TargetURL: "tcp://10.10.0.3:50051", Active: true},
			{Name: "gregale-compute-2", TargetURL: "tcp://10.10.0.2:50051", Active: false},
			{Name: "single-box", TargetURL: "unix:///run/faas/vmmd.sock", Active: true},
		},
		probe:    probe,
		public:   map[string]netip.Addr{"gregale-compute-1": netip.MustParseAddr("203.0.113.1")},
		edgePort: "9109",
	}
	nodes, err := src.nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []edgedns.Node{
		{Name: "gregale-compute-1", Public: netip.MustParseAddr("203.0.113.1"), Active: true, Ready: true},
		{Name: "gregale-compute-2", Active: false, Ready: false},
		{Name: "single-box", Active: true, Ready: false},
	}
	if len(nodes) != len(want) {
		t.Fatalf("nodes = %+v", nodes)
	}
	for i := range want {
		if nodes[i] != want[i] {
			t.Errorf("node %d = %+v, want %+v", i, nodes[i], want[i])
		}
	}
	if len(probe.urls) != 2 {
		t.Fatalf("probed %v, want the two nodes with a private address", probe.urls)
	}
}

type fakeDirect struct {
	mu     sync.Mutex
	serves map[string]bool // "<addr> <host>"
	probed []string
}

func (p *fakeDirect) Serves(_ context.Context, addr netip.Addr, host string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	key := addr.String() + " " + host
	p.probed = append(p.probed, key)
	return p.serves[key]
}

// TestNodeSourceProbesEachPublicAddressForEveryDNSOnlyName: a node serves
// direct entry only when its public address completes a handshake for every
// DNS-only name; a node without a public address is not probed.
func TestNodeSourceProbesEachPublicAddressForEveryDNSOnlyName(t *testing.T) {
	direct := &fakeDirect{serves: map[string]bool{
		"203.0.113.1 a.gregale.dev": true, "203.0.113.1 b.gregale.dev": true,
		"203.0.113.2 a.gregale.dev": true,
	}}
	src := nodeSource{
		registry: fakeRegistry{
			{Name: "n1", TargetURL: "tcp://10.10.0.1:50051", Active: true},
			{Name: "n2", TargetURL: "tcp://10.10.0.2:50051", Active: true},
			{Name: "n3", TargetURL: "tcp://10.10.0.3:50051", Active: true},
		},
		probe: &fakeProbe{ready: map[string]bool{}},
		public: map[string]netip.Addr{
			"n1": netip.MustParseAddr("203.0.113.1"), "n2": netip.MustParseAddr("203.0.113.2"),
		},
		edgePort:    "9109",
		direct:      direct,
		directHosts: dnsOnlyHosts([]edgedns.Name{{Host: "*.gregale.dev"}, {Host: "a.gregale.dev", DNSOnly: true}, {Host: "b.gregale.dev", DNSOnly: true}}),
	}
	nodes, err := src.nodes(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := []bool{nodes[0].Direct, nodes[1].Direct, nodes[2].Direct}; !slices.Equal(got, []bool{true, false, false}) {
		t.Fatalf("direct = %v, want only the node serving both DNS-only names", got)
	}
	if len(direct.probed) != 4 {
		t.Fatalf("probed %v, want both DNS-only names on the two nodes with a public address", direct.probed)
	}

	src.directHosts = nil
	direct.probed = nil
	if _, err := src.nodes(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(direct.probed) != 0 {
		t.Fatalf("probed %v with no DNS-only name", direct.probed)
	}
}

func TestPrivateHost(t *testing.T) {
	for target, want := range map[string]string{
		"tcp://10.10.0.3:50051":      "10.10.0.3",
		"unix:///run/faas/vmmd.sock": "",
		"dns:///vmmd.internal:50051": "vmmd.internal",
		"not a target":               "",
	} {
		if got := privateHost(target); got != want {
			t.Errorf("privateHost(%q) = %q, want %q", target, got, want)
		}
	}
}
