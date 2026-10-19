package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/pki"
)

// adr: 20261016151947 — `pki issue-feature` issues a feature's leaves from the
// CA once, keeps them while they are valid, and reports which it issued so a
// playbook restarts only the daemon whose leaf changed.
func TestPKIIssueFeature(t *testing.T) {
	ca := t.TempDir()
	if _, _, err := pki.EnsureCA(ca, false); err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	args := []string{"--feature", "live-queries", "--box-role", "control-plane", "--ca-dir", ca, "--root-dir", out}
	got, err := captureStdout(t, func() int { return cmdPKIIssueFeature(args) })
	if err != nil || strings.TrimSpace(got) != `{"issued":["livequeryd/server"]}` {
		t.Fatalf("first run = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(out, "livequeryd", "server.key")); err != nil {
		t.Fatalf("server key not written: %v", err)
	}
	got, err = captureStdout(t, func() int { return cmdPKIIssueFeature(args) })
	if err != nil || strings.TrimSpace(got) != `{"issued":[]}` {
		t.Fatalf("second run = %q, %v; want the leaf kept", got, err)
	}
	for name, bad := range map[string][]string{
		"unknown feature": {"--feature", "nope", "--box-role", "control-plane", "--ca-dir", ca, "--root-dir", out},
		"no CA":           {"--feature", "live-queries", "--box-role", "compute-only", "--ca-dir", t.TempDir(), "--root-dir", out},
		"no box role":     {"--feature", "live-queries", "--ca-dir", ca, "--root-dir", out},
	} {
		if _, err := captureStdout(t, func() int { return cmdPKIIssueFeature(bad) }); err == nil {
			t.Errorf("%s: issue-feature succeeded", name)
		}
	}
}

// adr: 20261016151947 — expiry metrics cover a feature leaf once the host
// carries it, and a host without the feature is not an error.
func TestRenderPKIMetricsCoversPresentFeatureLeaves(t *testing.T) {
	rootDir := seedPKIRootDir(t)
	label := `daemon="livequeryd"`
	body, err := renderPKIMetrics(rootDir, "control-plane", "fsn-1", "", time.Unix(1234, 0))
	if err != nil || strings.Contains(string(body), label) {
		t.Fatalf("without the feature: err=%v, livequeryd row present=%v", err, strings.Contains(string(body), label))
	}
	role := pki.FeatureRoles(pki.FeatureLiveQueries, "control-plane")[0]
	certPath, keyPath := pki.LeafPaths(rootDir, role)
	if err := os.MkdirAll(filepath.Dir(certPath), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFakeCert(t, certPath, keyPath, "leaf-"+role.CommonName, []string{role.CommonName})
	body, err = renderPKIMetrics(rootDir, "control-plane", "fsn-1", "", time.Unix(1234, 0))
	if err != nil || !strings.Contains(string(body), `faas_internal_mtls_leaf_earliest_expiry_timestamp_seconds{host="fsn-1",box_role="control-plane",`+label+`}`) {
		t.Fatalf("with the feature leaf: err=%v\n%s", err, body)
	}
}
