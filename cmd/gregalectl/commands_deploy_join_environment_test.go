package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/manifest"
)

// renderJoinEnvironment renders manifestYAML (with a compute host fsn-2)
// into root and returns the compute join's environment hash.
func renderJoinEnvironment(t *testing.T, manifestYAML, root, varsFile string) string {
	t.Helper()
	m, err := manifest.Parse([]byte(manifestYAML))
	if err != nil {
		t.Fatalf("manifest.Parse: %v", err)
	}
	if errs := m.Validate(); errs != nil {
		t.Fatalf("manifest.Validate: %v", errs)
	}
	files, err := renderManifestAnsibleFiles(m, root)
	if err != nil {
		t.Fatalf("renderManifestAnsibleFiles: %v", err)
	}
	env, err := controlPlaneEnvironmentHash(root, files, varsFile)
	if err != nil {
		t.Fatalf("environment hash: %v", err)
	}
	return env
}

// rc.364 (2026-11-21): the manifest moved the apps domain to gregale.app
// but the compute bootstrap contract hashed repository files only, so it
// stayed current, CD skipped compute convergence and the gatewayd domain
// layout and serving probe were never re-rendered. The contract must move
// with the manifest-rendered inventory and stay put when only the
// temporary directory differs.
func TestJoinContractMovesWithTheManifest(t *testing.T) {
	base := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: 10.42.0.1:7100\n    - name: fsn-2\n      role: compute-only\n      address: 10.42.0.2:50051\n", 1)
	moved := strings.Replace(base, "dns:\n  apps_domain: apps.gregale.dev\n  mode: cloudflare\n",
		"dns:\n  apps_domain: gregale.app\n  mode: cloudflare\n  platform_domain: gregale.dev\n  apps_domain_aliases: [gregale.dev]\n", 1)
	if moved == base {
		t.Fatal("the fixture's dns block changed; update the apps-domain move")
	}
	const repo = "sha256:repo"

	first := renderJoinEnvironment(t, base, t.TempDir(), "")
	again := renderJoinEnvironment(t, base, t.TempDir(), "")
	if first != again {
		t.Errorf("the same manifest in another temporary directory moved the environment: %s != %s", first, again)
	}
	if joinContractWithEnvironment(repo, first) != joinContractWithEnvironment(repo, again) {
		t.Error("the same manifest must give the same compute contract")
	}
	after := renderJoinEnvironment(t, moved, t.TempDir(), "")
	if after == first {
		t.Fatal("moving the apps domain must move the environment hash")
	}
	if joinContractWithEnvironment(repo, after) == joinContractWithEnvironment(repo, first) {
		t.Error("moving the apps domain must make the compute contract stale")
	}
	if joinContractWithEnvironment("sha256:other", first) == joinContractWithEnvironment(repo, first) {
		t.Error("a repository change must still make the compute contract stale")
	}
}

// The operator vars are an input, but the directory CD stages them in is
// not: cd-compute writes ansible-vars.yml and the seal-domain files under a
// per-run artifact directory and names them by absolute path.
func TestJoinEnvironmentTracksOperatorVarsNotTheirDirectory(t *testing.T) {
	writeVars := func(dir, body string) string {
		path := filepath.Join(dir, "ansible-vars.yml")
		if err := os.WriteFile(path, []byte(strings.ReplaceAll(body, "DIR", dir)), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	inventory := []manifestAnsibleFile{{Path: "/r/inventory/host_vars/fsn-2.yml", Body: []byte("a: 1\n")}}
	hash := func(vars string) string {
		t.Helper()
		got, err := controlPlaneEnvironmentHash("/r", inventory, vars)
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	body := "faas_seal_domain_runtime_identity_file: DIR/seal-domains/runtime.key\nfaas_serving_probe_fleet: {fsn-2: {urls: [https://serving-probe.gregale.app/]}}\n"
	one := hash(writeVars(t.TempDir(), body))
	if two := hash(writeVars(t.TempDir(), body)); two != one {
		t.Errorf("the vars directory moved the environment: %s != %s", one, two)
	}
	if other := hash(writeVars(t.TempDir(), strings.Replace(body, "gregale.app", "gregale.dev", 1))); other == one {
		t.Error("an operator vars change must move the environment")
	}
	if none := hash(""); none == one {
		t.Error("a join without operator vars must hash differently from one with them")
	}
}

func TestWithJoinRecordEnvironment(t *testing.T) {
	if got, err := withJoinRecordEnvironment("", "sha256:env"); err != nil || got != "" {
		t.Errorf("no record = %q, %v; want none", got, err)
	}
	record := `{"format_version": 2, "group": "compute_nodes", "globals": "sha256:aa", "order": ["geoip"], "roles": {"geoip": "sha256:bb"}}`
	got, err := withJoinRecordEnvironment(record, "sha256:env")
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(got), &fields); err != nil {
		t.Fatalf("record is not JSON: %v\n%s", err, got)
	}
	if fields["environment"] != "sha256:env" || fields["group"] != "compute_nodes" || fields["globals"] != "sha256:aa" {
		t.Errorf("record = %v; want the hashes kept and the environment added", fields)
	}
	if roles, _ := fields["roles"].(map[string]any); roles["geoip"] != "sha256:bb" {
		t.Errorf("roles = %v", fields["roles"])
	}
	if _, err := withJoinRecordEnvironment("{", "sha256:env"); err == nil {
		t.Error("an invalid record must be refused")
	}
}
