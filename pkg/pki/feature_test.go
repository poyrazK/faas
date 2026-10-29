package pki

import (
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// adr: 20261016151947 — live-query leaves are feature leaves: no host role
// requires them, so the required sets (and every rollout and renewal path
// built on them) are unchanged, and their directories are not ones a trust
// bundle installs into.
func TestFeatureLeavesStayOutOfTheRequiredSets(t *testing.T) {
	required := map[string]bool{}
	for _, role := range Roles() {
		required[role.Directory] = true
	}
	for _, feature := range Features() {
		for _, box := range []string{"control-plane", "compute-only", "single-box"} {
			roles := FeatureRoles(feature, box)
			if len(roles) == 0 {
				t.Fatalf("%s: %s carries no leaf", feature, box)
			}
			for _, role := range roles {
				if required[role.Directory] {
					t.Errorf("%s: %s: feature leaf %s/%s shares a required leaf's directory", feature, box, role.Directory, role.Filename)
				}
			}
		}
	}
	if got := FeatureRoles("nope", "control-plane"); got != nil {
		t.Errorf("unknown feature has leaves: %v", got)
	}
	if got := FeatureRoles(FeatureLiveQueries, "bogus"); got != nil {
		t.Errorf("unknown box role has leaves: %v", got)
	}
}

// adr: 20261016151947 — the issuer's CA signs the hub's server leaf and
// realtimed's client leaf; a second run keeps them, a leaf near expiry is
// reissued, and a directory without a CA issues nothing.
func TestEnsureFeatureLeaves(t *testing.T) {
	ca := t.TempDir()
	caCert, _, err := EnsureCA(ca, false)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	issued, err := EnsureFeatureLeaves(ca, out, FeatureLiveQueries, "single-box")
	if err != nil || len(issued) != 2 {
		t.Fatalf("first issue = %v, %v", issued, err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	for _, tc := range []struct {
		role  Role
		usage x509.ExtKeyUsage
		name  string
	}{
		{FeatureRoles(FeatureLiveQueries, "control-plane")[0], x509.ExtKeyUsageServerAuth, LivequerydServerName},
		{FeatureRoles(FeatureLiveQueries, "compute-only")[0], x509.ExtKeyUsageClientAuth, RealtimedLiveClient},
	} {
		certPath, keyPath := LeafPaths(out, tc.role)
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: tc.name, KeyUsages: []x509.ExtKeyUsage{tc.usage}}); err != nil {
			t.Errorf("%s does not verify as %s: %v", certPath, tc.name, err)
		}
		if info, err := os.Stat(keyPath); err != nil || info.Mode().Perm() != 0o400 {
			t.Errorf("%s mode = %v, %v; want 0400", keyPath, info.Mode().Perm(), err)
		}
	}

	if issued, err := EnsureFeatureLeaves(ca, out, FeatureLiveQueries, "single-box"); err != nil || len(issued) != 0 {
		t.Fatalf("second issue = %v, %v; want the leaves kept", issued, err)
	}
	// A leaf that no longer verifies (here: removed) is issued again alone.
	server := FeatureRoles(FeatureLiveQueries, "control-plane")[0]
	certPath, keyPath := LeafPaths(out, server)
	for _, p := range []string{certPath, keyPath} {
		if err := os.Remove(p); err != nil {
			t.Fatal(err)
		}
	}
	if issued, err := EnsureFeatureLeaves(ca, out, FeatureLiveQueries, "single-box"); err != nil || len(issued) != 1 || issued[0].Directory != "livequeryd" {
		t.Fatalf("after removing the server leaf: %v, %v", issued, err)
	}

	if _, err := EnsureFeatureLeaves(t.TempDir(), out, FeatureLiveQueries, "control-plane"); err == nil || !strings.Contains(err.Error(), "no CA") {
		t.Errorf("issuing without a CA = %v", err)
	}
	if _, err := EnsureFeatureLeaves(ca, out, "nope", "control-plane"); err == nil {
		t.Error("an unknown feature issued")
	}
	if _, err := os.Stat(filepath.Join(out, "ca")); !os.IsNotExist(err) {
		t.Error("feature issuance wrote a CA into the output directory")
	}
}

// adr: 20261023124955 — the platform-log leaves: Loki's server leaf and the
// Promtail, Prometheus and Grafana client leaves on the control plane, and
// only Promtail's on a compute node. Each verifies against the fleet CA under
// the name its peer checks, and a single box carries Promtail's leaf once.
func TestPlatformLogsFeatureLeaves(t *testing.T) {
	names := func(roles []Role) []string {
		var out []string
		for _, r := range roles {
			out = append(out, r.Directory+"/"+r.Filename)
		}
		return out
	}
	if got := names(FeatureRoles(FeaturePlatformLogs, "compute-only")); strings.Join(got, ",") != "promtail/loki-client" {
		t.Fatalf("compute-only leaves = %v", got)
	}
	if got := names(FeatureRoles(FeaturePlatformLogs, "single-box")); len(got) != 4 {
		t.Fatalf("single-box leaves = %v, want the four control-plane leaves once each", got)
	}

	ca := t.TempDir()
	caCert, _, err := EnsureCA(ca, false)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	issued, err := EnsureFeatureLeaves(ca, out, FeaturePlatformLogs, "control-plane")
	if err != nil || len(issued) != 4 {
		t.Fatalf("issue = %v, %v", names(issued), err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(caCert)
	want := map[string]struct {
		name  string
		usage x509.ExtKeyUsage
	}{
		"loki/server":            {LokiServerName, x509.ExtKeyUsageServerAuth},
		"promtail/loki-client":   {PromtailLokiClient, x509.ExtKeyUsageClientAuth},
		"prometheus/loki-client": {PrometheusLokiClient, x509.ExtKeyUsageClientAuth},
		"grafana/loki-client":    {GrafanaLokiClient, x509.ExtKeyUsageClientAuth},
	}
	for _, role := range issued {
		w := want[role.Directory+"/"+role.Filename]
		certPath, keyPath := LeafPaths(out, role)
		pair, err := tls.LoadX509KeyPair(certPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		leaf, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			t.Fatal(err)
		}
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: w.name, KeyUsages: []x509.ExtKeyUsage{w.usage}}); err != nil {
			t.Errorf("%s does not verify as %s: %v", certPath, w.name, err)
		}
	}
	if again, err := EnsureFeatureLeaves(ca, out, FeaturePlatformLogs, "control-plane"); err != nil || len(again) != 0 {
		t.Fatalf("second issue = %v, %v; want the leaves kept", names(again), err)
	}
}
