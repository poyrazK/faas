package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestResolveJoinPrivateAddressesSeedsSkippedPreflightFacts(t *testing.T) {
	m, err := manifest.Load(splitboxJoinManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, network, host string) ([]net.IP, error) {
		if network != "ip4" {
			t.Fatalf("lookup network = %q, want ip4", network)
		}
		switch host {
		case "fsn-1.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.1")}, nil
		case "fsn-2.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.2"), net.ParseIP("10.42.0.2")}, nil
		default:
			return nil, fmt.Errorf("unexpected host %s", host)
		}
	}

	addresses, err := resolveJoinPrivateAddresses(t.Context(), m, lookup)
	if err != nil {
		t.Fatalf("resolveJoinPrivateAddresses: %v", err)
	}
	if got := addresses["fsn-1"]; got != "10.42.0.1" {
		t.Fatalf("fsn-1 address = %q, want 10.42.0.1", got)
	}
	if got := addresses["fsn-2"]; got != "10.42.0.2" {
		t.Fatalf("fsn-2 address = %q, want 10.42.0.2", got)
	}

	files, err := renderManifestAnsibleFiles(m, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	seedJoinPrivateAddressFacts(files, addresses)
	for _, file := range files {
		host := strings.TrimSuffix(filepath.Base(file.Path), ".yml")
		address := addresses[host]
		if address == "" {
			continue
		}
		if !strings.Contains(string(file.Body), `faas_private_address: "`+address+`"`) {
			t.Fatalf("%s host vars do not contain resolved private address %s:\n%s", host, address, file.Body)
		}
	}
}

func TestResolveJoinPrivateAddressesRejectsPublicAnswer(t *testing.T) {
	m, err := manifest.Load(splitboxJoinManifest(t))
	if err != nil {
		t.Fatal(err)
	}
	lookup := func(_ context.Context, _, _ string) ([]net.IP, error) {
		return []net.IP{net.ParseIP("203.0.113.10")}, nil
	}
	_, err = resolveJoinPrivateAddresses(t.Context(), m, lookup)
	if err == nil || !strings.Contains(err.Error(), "outside overlay") {
		t.Fatalf("resolveJoinPrivateAddresses error = %v, want outside-overlay rejection", err)
	}
}

func TestNodeJoinLeaseRefreshInterval(t *testing.T) {
	tests := []struct {
		name string
		ttl  time.Duration
		want time.Duration
	}{
		{name: "workflow lease", ttl: 5 * time.Minute, want: 100 * time.Second},
		{name: "minimum", ttl: time.Second, want: time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nodeJoinLeaseRefreshInterval(tt.ttl); got != tt.want {
				t.Fatalf("nodeJoinLeaseRefreshInterval(%s) = %s, want %s", tt.ttl, got, tt.want)
			}
		})
	}
}

func TestJoinBootstrapContractHashTracksBootstrapSources(t *testing.T) {
	ansibleDir := t.TempDir()
	for _, dir := range []string{"roles/_shared", "roles/compute/tasks", "roles/control/tasks"} {
		if err := os.MkdirAll(filepath.Join(ansibleDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for path, body := range map[string]string{
		"bootstrap.yml":                "---\n- hosts: control_plane\n  roles:\n    - role: control\n- hosts: control_plane:compute_nodes\n  tasks:\n    - debug: {msg: shared}\n- hosts: compute_nodes\n  roles:\n    - role: compute\n",
		"node_join.yml":                "---\n",
		"requirements.yml":             "collections: []\n",
		"roles/_shared/common.yml":     "---\n",
		"roles/compute/tasks/main.yml": "---\n",
		"roles/control/tasks/main.yml": "---\n",
	} {
		if err := os.WriteFile(filepath.Join(ansibleDir, path), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	before, err := joinBootstrapContractHash(ansibleDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != len("sha256:")+sha256.Size*2 || !strings.HasPrefix(before, "sha256:") {
		t.Fatalf("bootstrap contract hash = %q", before)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "roles/compute/tasks/main.yml"), []byte("---\n- debug: msg=changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after, err := joinBootstrapContractHash(ansibleDir)
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Fatalf("bootstrap contract hash did not change after a compute role change: %s", after)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "roles/control/tasks/main.yml"), []byte("---\n- debug: msg=control-only\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	controlOnly, err := joinBootstrapContractHash(ansibleDir)
	if err != nil {
		t.Fatal(err)
	}
	if controlOnly != after {
		t.Fatalf("control-only role invalidated compute contract: before=%s after=%s", after, controlOnly)
	}
}

func TestNodeJoinFullBootstrapPreservesPlayLevelRoleSemantics(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	converge := strings.Index(playbook, "Converge the adopted node with the production bootstrap")
	record := strings.Index(playbook, "Record successful compute bootstrap convergence")
	if converge < 0 || record < 0 || converge >= record {
		t.Fatalf("node_join.yml is missing the full bootstrap convergence block")
	}
	block := playbook[converge:record]
	if !strings.Contains(block, "import_playbook: bootstrap.yml") {
		t.Fatalf("full node convergence must retain bootstrap.yml play-level role semantics")
	}
	bootstrapBody, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "bootstrap.yml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(bootstrapBody), "ansible.builtin.meta: end_host") ||
		!strings.Contains(string(bootstrapBody), "faas_join_bootstrap_contract_current | default(false) | bool") {
		t.Fatalf("bootstrap.yml must end each managed host before evaluating full-bootstrap roles")
	}
}

func TestNodeJoinStagesAndVerifiesFleetSealBeforeServicesStart(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	stage := strings.Index(playbook, "Stage the dedicated fleet unseal identity before bootstrap")
	bootstrap := strings.Index(playbook, "import_playbook: bootstrap.yml")
	verify := strings.Index(playbook, "Prove fleet customer-secret and shared-kid access before service activation")
	restart := strings.Index(playbook, "Enable and restart the compute-only daemon set")
	if stage < 0 || bootstrap < 0 || verify < 0 || restart < 0 {
		t.Fatal("node_join is missing a fleet-seal staging, bootstrap, verification, or service-start gate")
	}
	if stage >= bootstrap {
		t.Fatal("fleet.age must be staged before bootstrap renders units that require it")
	}
	if verify >= restart {
		t.Fatal("fleet secret and shared-kid verification must pass before compute services start")
	}
	block := playbook[verify:restart]
	for _, token := range []string{"fleet-seal", "verify", "/etc/faas/secrets/fleet.age", "/etc/faas/secrets/host.age", "faas_fleet_seal.prom"} {
		if !strings.Contains(block, token) {
			t.Errorf("fleet verification gate missing %q", token)
		}
	}
}

func TestFleetVerifyUsesPrivateTransportAddressForComputeReadiness(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "roles", "fleet_verify", "tasks", "main.yml"))
	if err != nil {
		t.Fatal(err)
	}
	tasks := string(body)
	if !strings.Contains(tasks, "faas_private_dns_address") || !strings.Contains(tasks, "faas_private_address") {
		t.Fatal("fleet_verify must probe compute readiness through the provider-neutral private transport address")
	}
	if strings.Contains(tasks, "regex_replace('127\\.0\\.0\\.1', ansible_host)") {
		t.Fatal("fleet_verify must not use the provider SSH address for private readiness probes")
	}
}

func TestNodeJoinPublishesHardwareCapacityBeforeVMMDStarts(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	capacity := strings.Index(playbook, "Install the hardware-derived vmmd capacity contract")
	restart := strings.Index(playbook, "Enable and restart the compute-only daemon set")
	if capacity < 0 || restart < 0 || capacity >= restart {
		t.Fatal("node_join must install the vmmd capacity drop-in before restarting vmmd")
	}
	block := playbook[capacity:restart]
	for _, token := range []string{
		"FAAS_COMPUTE_VCPUS={{ ansible_processor_vcpus }}",
		"FAAS_COMPUTE_MEM_MB={{ ansible_memtotal_mb }}",
		"FAAS_COMPUTE_MAX_CONCURRENCY=",
		"FAAS_COMPUTE_ADMISSION_CEILING_MB=",
		"FAAS_VCPU_BUDGET=",
	} {
		if !strings.Contains(block, token) {
			t.Errorf("capacity contract missing %q", token)
		}
	}
}

func TestNodeJoinRemovesEmergencyGatewayReleaseOverrideBeforeRestart(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	remove := strings.Index(playbook, "Remove emergency gateway release override before service activation")
	restart := strings.Index(playbook, "Enable and restart the compute-only daemon set")
	if remove < 0 || restart < 0 || remove >= restart {
		t.Fatal("node_join must remove the emergency gateway release override before restarting the compute services")
	}
	block := playbook[remove:restart]
	if !strings.Contains(block, "/etc/systemd/system/faas-gatewayd-internal.service.d/zz-emergency-release.conf") {
		t.Fatal("node_join emergency override cleanup targets the wrong path")
	}
}

func TestNodeJoinPrestagesRuntimeBasesBeforeDrain(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	prestage := strings.Index(playbook, "Pre-stage release-bound runtime bases before draining the node")
	preregister := strings.Index(playbook, "Pre-register the newly adopted node as unavailable before release installation")
	if prestage < 0 || preregister < 0 || prestage >= preregister {
		t.Fatal("runtime bases must be staged with the candidate release before node preregistration and drain")
	}
	block := playbook[strings.Index(playbook, "Install the runtime-base pre-stage one-shot"):preregister]
	for _, token := range []string{
		"FAAS_IMAGED_PRESTAGE_ONLY=1",
		"FAAS_GUEST_INIT=/opt/faas/prestage/",
		"FAAS_FUNCTION_RUNNER_NODE24=/opt/faas/prestage/",
		"LoadCredential=faas_fleet_age_identity",
	} {
		if !strings.Contains(block, token) {
			t.Errorf("pre-stage unit missing %q", token)
		}
	}
}

func TestNodeJoinCASStampsRefreshedCertificateBeforePrestage(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	inspect := strings.Index(playbook, "Inspect existing compute-node certificate attestation before trust refresh")
	stage := strings.Index(playbook, "Stage the compute trust bundle (the source never includes the CA private key)")
	stamp := strings.Index(playbook, "CAS-stamp the staged vmmd certificate before runtime pre-stage")
	prestage := strings.Index(playbook, "Pre-stage release-bound runtime bases before draining the node")
	if inspect < 0 || stage < 0 || stamp < 0 || prestage < 0 || !(inspect < stage && stage < stamp && stamp < prestage) {
		t.Fatalf("certificate convergence order invalid: inspect=%d stage=%d stamp=%d prestage=%d", inspect, stage, stamp, prestage)
	}
	block := playbook[inspect:prestage]
	for _, token := range []string{"compute-nodes", "show", "--break-glass-db", "cert_fingerprint=", "regex_findall", "secrets", "stamp", "--expected-fingerprint"} {
		if !strings.Contains(block, token) {
			t.Errorf("certificate convergence block missing %q", token)
		}
	}
	if strings.Contains(block, "from_json") || strings.Contains(block, "- --json") {
		t.Fatal("certificate convergence must parse the stable human output emitted by already-installed legacy gregalectl binaries")
	}
}

func TestControlPlanePeerConvergenceHasContractFastPath(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join_control_plane.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	probe := strings.Index(playbook, "Probe the control-plane peer contract")
	end := strings.Index(playbook, "ansible.builtin.meta: end_host")
	gather := strings.Index(playbook, "Gather facts for changed control-plane peer convergence")
	persist := strings.Index(playbook, "Persist the converged control-plane peer contract")
	if probe < 0 || end <= probe || gather <= end || persist <= gather {
		t.Fatal("control-plane peer contract must exit unchanged hosts before fact gathering and persist only after convergence")
	}
}

func TestNodeJoinPreregistrationAcknowledgesBootstrapDatabaseWrite(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	start := strings.Index(playbook, "Pre-register the newly adopted node as unavailable before release installation")
	end := strings.Index(playbook, "Stop stale compute-only services before replacing the active release")
	if start < 0 || end < 0 || start >= end {
		t.Fatal("node_join.yml is missing the compute-node preregistration block")
	}
	block := playbook[start:end]
	for _, token := range []string{
		"- --reason",
		"- node_join_preregister",
		"- --break-glass-db",
		"- --yes",
	} {
		if !strings.Contains(block, token) {
			t.Errorf("compute-node preregistration missing %q", token)
		}
	}
}

func TestNodeJoinDrainsExistingTrafficBeforeStoppingListeners(t *testing.T) {
	body, err := os.ReadFile(filepath.Join("..", "..", "deploy", "ansible", "node_join.yml"))
	if err != nil {
		t.Fatal(err)
	}
	playbook := string(body)
	drain := strings.Index(playbook, "Begin graceful drain of the existing node before release installation")
	wait := strings.Index(playbook, "Wait for the existing node to reach an empty maintenance hold")
	stop := strings.Index(playbook, "Stop stale compute-only services before replacing the active release")
	if drain < 0 || wait < 0 || stop < 0 || !(drain < wait && wait < stop) {
		t.Fatalf("graceful drain order invalid: drain=%d wait=%d stop=%d", drain, wait, stop)
	}
	waitBlock := playbook[wait:stop]
	for _, token := range []string{"drain-status", "--break-glass-db", "retries: 48", "until: faas_compute_node_drain_status.rc == 0"} {
		if !strings.Contains(waitBlock, token) {
			t.Errorf("drain barrier missing %q", token)
		}
	}
	stopBlockEnd := strings.Index(playbook[stop:], "Install the verified release while keeping the row drained")
	if stopBlockEnd < 0 {
		t.Fatal("node_join is missing the release install after service stop")
	}
	stopBlock := playbook[stop : stop+stopBlockEnd]
	gateway := strings.Index(stopBlock, "faas-gatewayd-internal.service")
	schedd := strings.Index(stopBlock, "faas-schedd.service")
	vmmd := strings.Index(stopBlock, "faas-vmmd.service")
	if gateway < 0 || schedd < 0 || vmmd < 0 || !(gateway < schedd && schedd < vmmd) {
		t.Fatalf("service stop order must quiesce ingress before schedd and vmmd: gateway=%d schedd=%d vmmd=%d", gateway, schedd, vmmd)
	}
}

func splitboxJoinManifest(t *testing.T) string {
	t.Helper()
	body := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:9091\n    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1)
	return writeSplitboxManifest(t, body)
}

func TestDeployJoinValidate_DryRunNeedsOnlyManifestAndSSH(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-2",
		SSHHost:      "198.51.100.20",
		RepoRoot:     repo,
		DryRun:       true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if report.DatabaseNode != "fsn-2.faas" {
		t.Errorf("DatabaseNode = %q, want fsn-2.faas", report.DatabaseNode)
	}
	if report.ReleaseGitSHA != "abc1234567890abcdef1234567890abcdef12345" {
		t.Errorf("ReleaseGitSHA = %q", report.ReleaseGitSHA)
	}
	if len(report.Steps) < 8 {
		t.Fatalf("steps = %d, want lifecycle plan", len(report.Steps))
	}
}

func TestHasComputeDatabaseEnvRequiresBothDaemonVariables(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compute-db.env")

	tests := []struct {
		name string
		body string
		want bool
	}{
		{
			name: "both entries",
			body: "DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=postgres://faas@example/faas\n",
			want: true,
		},
		{
			name: "vmmd entry missing",
			body: "DATABASE_URL=postgres://faas@example/faas\n",
			want: false,
		},
		{
			name: "empty vmmd entry",
			body: "DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=\n",
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := os.WriteFile(path, []byte(tt.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := hasComputeDatabaseEnv(path); got != tt.want {
				t.Fatalf("hasComputeDatabaseEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateRuntimeBasesEnvRequiresAllPinnedRefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-bases.env")
	valid := strings.Join([]string{
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT=ghcr.io/example/base-debian-parent@sha256:" + strings.Repeat("7", 64),
		"FAAS_DEPLOY_BASE_REF_NODE22=ghcr.io/example/runner-node22@sha256:" + strings.Repeat("a", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	}, "\n") + "\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeBasesEnv(path, nil); err != nil {
		t.Fatalf("valid runtime contract rejected: %v", err)
	}

	if err := os.WriteFile(path, []byte(strings.Replace(valid, "FAAS_DEPLOY_BASE_REF_NODE24=", "", 1)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRuntimeBasesEnv(path, nil); err == nil || !strings.Contains(err.Error(), "NODE24") {
		t.Fatalf("missing runtime ref error = %v", err)
	}
}

func TestValidateRuntimeBasesEnvMatchesSignedManifestRefs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime-bases.env")
	ref := "ghcr.io/example/runner-node22@sha256:" + strings.Repeat("a", 64)
	body := "FAAS_DEPLOY_BASE_REF_NODE22=" + ref + "\n"
	for _, line := range []string{
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT=ghcr.io/example/base-debian-parent@sha256:" + strings.Repeat("7", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	} {
		body += line + "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := map[string]string{
		"minimal":       "ghcr.io/example/base-minimal@sha256:" + strings.Repeat("0", 64),
		"debian_parent": "ghcr.io/example/base-debian-parent@sha256:" + strings.Repeat("7", 64),
		"node22":        ref,
		"python312":     "ghcr.io/example/runner-python312@sha256:" + strings.Repeat("b", 64),
		"go124":         "ghcr.io/example/runner-go124@sha256:" + strings.Repeat("c", 64),
		"go124_alpine":  "ghcr.io/example/runner-go124-alpine@sha256:" + strings.Repeat("d", 64),
		"node24":        "ghcr.io/example/runner-node24@sha256:" + strings.Repeat("e", 64),
		"python313":     "ghcr.io/example/runner-python313@sha256:" + strings.Repeat("f", 64),
	}
	if err := validateRuntimeBasesEnv(path, expected); err != nil {
		t.Fatalf("signed runtime contract rejected: %v", err)
	}
	expected["node22"] = strings.Replace(ref, "runner-node22", "runner-node24", 1)
	if err := validateRuntimeBasesEnv(path, expected); err == nil || !strings.Contains(err.Error(), "NODE22") {
		t.Fatalf("manifest mismatch error = %v", err)
	}
}

func TestDeployJoinValidate_RejectsControlPlane(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	_, err := deployJoinValidate(deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-1",
		SSHHost:      "198.51.100.10",
		RepoRoot:     t.TempDir(),
		DryRun:       true,
	})
	if err == nil || !strings.Contains(err.Error(), "requires compute-only") {
		t.Fatalf("error = %v, want compute-only guard", err)
	}
}

func TestDeployJoinValidate_AllowsSignedReleaseOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	const release = "0123456789abcdef0123456789abcdef01234567"
	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:  manifestPath,
		Node:          "fsn-2",
		SSHHost:       "198.51.100.20",
		ReleaseGitSHA: release,
		RepoRoot:      repo,
		DryRun:        true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if report.ReleaseGitSHA != release {
		t.Fatalf("ReleaseGitSHA = %q, want %q", report.ReleaseGitSHA, release)
	}
}

func TestDeployJoinValidate_RejectsInvalidReleaseOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:  manifestPath,
		Node:          "fsn-2",
		SSHHost:       "198.51.100.20",
		ReleaseGitSHA: "not-a-sha",
		RepoRoot:      repo,
		DryRun:        true,
	})
	if err == nil || !strings.Contains(err.Error(), "--release-git-sha") {
		t.Fatalf("error = %v, want invalid release override", err)
	}
}

func TestDeployJoinValidate_StorageContract(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, "deploy/ansible"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "deploy/ansible/node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	base := deployJoinOptions{
		ManifestFile: manifestPath,
		Node:         "fsn-2",
		SSHHost:      "198.51.100.20",
		RepoRoot:     repo,
		DryRun:       true,
	}
	base.StorageDevice = "nvme0n1"
	if _, err := deployJoinValidate(base); err == nil || !strings.Contains(err.Error(), "absolute device path") {
		t.Fatalf("relative storage device error = %v, want absolute-path guard", err)
	}
	base.StorageDevice = ""
	base.FormatStorage = true
	if _, err := deployJoinValidate(base); err == nil || !strings.Contains(err.Error(), "requires --storage-device") {
		t.Fatalf("format-without-device error = %v, want explicit device guard", err)
	}
	manifestBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	withManifestDevice := strings.Replace(
		string(manifestBytes),
		"      address: fsn-2.gregale.dev:50051\n",
		"      address: fsn-2.gregale.dev:50051\n      storage_device: /dev/disk/by-id/google-local-ssd-0\n", 1,
	)
	deviceManifest := writeSplitboxManifest(t, withManifestDevice)
	base.ManifestFile = deviceManifest
	base.FormatStorage = true
	if _, err := deployJoinValidate(base); err != nil {
		t.Fatalf("manifest storage device should satisfy --format-storage: %v", err)
	}
}

func TestOverrideJoinHostVars_UsesProviderSSHOnlyForConnection(t *testing.T) {
	body := []byte("ansible_host: \"fsn-2.gregale.dev\"\nfaas_box_role: compute-only\n")
	got := string(overrideJoinHostVars(body, &deployJoinOptions{
		SSHHost: "203.0.113.8",
		SSHUser: "gregale",
		SSHPort: 2222,
		SSHKey:  "/tmp/id_ed25519",
	}))
	for _, want := range []string{
		`ansible_host: "203.0.113.8"`,
		`ansible_user: "gregale"`,
		"ansible_port: 2222",
		`ansible_ssh_private_key_file: "/tmp/id_ed25519"`,
		"faas_box_role: compute-only",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("host vars missing %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "ansible_host: \"fsn-2.gregale.dev\"") {
		t.Errorf("provider SSH override left manifest endpoint in place:\n%s", got)
	}
}

func TestOverrideJoinHostVars_PinsKnownHostFile(t *testing.T) {
	got := string(overrideJoinHostVars([]byte("ansible_host: \"fsn-2.gregale.dev\"\n"), &deployJoinOptions{
		SSHHost:           "203.0.113.8",
		SSHUser:           "root",
		SSHPort:           22,
		SSHKnownHostsFile: "/tmp/gregale-known-hosts",
	}))
	want := `ansible_ssh_common_args: "-o UserKnownHostsFile=/tmp/gregale-known-hosts -o StrictHostKeyChecking=yes"`
	if !strings.Contains(got, want) {
		t.Fatalf("known_hosts pinning missing %q:\n%s", want, got)
	}
}

func TestFingerprintMatches(t *testing.T) {
	output := "256 SHA256:other host-a (ED25519)\n256 SHA256:expected host-b (ED25519)\n"
	if !fingerprintMatches(output, "SHA256:expected") {
		t.Fatal("expected fingerprint was not found")
	}
	if fingerprintMatches(output, "SHA256:missing") {
		t.Fatal("missing fingerprint was reported as present")
	}
}

func TestRequireFleetKnownHostsCoversEveryManifestAddress(t *testing.T) {
	path := filepath.Join(t.TempDir(), "known_hosts")
	const key = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIBJcCV3B7r6Ey6qjXgmPLQxZQ6Ho9dJv0h5vPXLqyYV3"
	if err := os.WriteFile(path, []byte("fsn-1.gregale.dev "+key+"\nfsn-2.gregale.dev "+key+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := &manifest.Manifest{Fleet: manifest.Fleet{Hosts: []manifest.Host{
		{Name: "control", Address: "fsn-1.gregale.dev"},
		{Name: "compute-a", Address: "fsn-2.gregale.dev"},
	}}}
	if err := requireFleetKnownHosts(path, m); err != nil {
		t.Fatal(err)
	}
	m.Fleet.Hosts = append(m.Fleet.Hosts, manifest.Host{Name: "compute-b", Address: "fsn-3.gregale.dev"})
	if err := requireFleetKnownHosts(path, m); err == nil || !strings.Contains(err.Error(), "compute-b") {
		t.Fatalf("missing peer error = %v", err)
	}
}

func TestOverrideJoinHostVars_PreservesStorageContract(t *testing.T) {
	got := string(overrideJoinHostVars([]byte("faas_box_role: compute-only\n"), &deployJoinOptions{
		SSHHost:       "203.0.113.8",
		SSHUser:       "root",
		SSHPort:       22,
		StorageDevice: "/dev/disk/by-id/scsi-0Google_PersistentDisk_data",
		FormatStorage: true,
	}))
	for _, want := range []string{
		`faas_storage_device: "/dev/disk/by-id/scsi-0Google_PersistentDisk_data"`,
		"faas_storage_format: true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("host vars missing %q:\n%s", want, got)
		}
	}
}

func TestValidateSharedStorageEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "storage.env")
	valid := "FAAS_STORAGE_BACKEND=oci\n" +
		"FAAS_STORAGE_LOCAL_PREFIXES=none\n" +
		"FAAS_REQUIRE_SHARED_ARTIFACTS=1\n" +
		"FAAS_STORAGE_CACHE_SERVE_STALE=0\n" +
		"FAAS_OCI_REGISTRY=https://registry.example\n"
	if err := os.WriteFile(path, []byte(valid), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err != nil {
		t.Fatalf("valid storage env rejected: %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=local\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "BACKEND=oci") {
		t.Fatalf("local storage env error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=snap/,base/\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "snap/") {
		t.Fatalf("snap prefix error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\nFAAS_STORAGE_CACHE_DIR=\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "CACHE_DIR") {
		t.Fatalf("empty cache dir error = %v", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\nFAAS_STORAGE_CACHE_DIR=/srv/custom-cache\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "managed systemd units") {
		t.Fatalf("non-canonical cache path error = %v, want managed-unit guard", err)
	}
	if err := os.WriteFile(path, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateSharedStorageEnv(path); err == nil || !strings.Contains(err.Error(), "LOCAL_PREFIXES=none") {
		t.Fatalf("incomplete strict contract error = %v", err)
	}
}

func TestValidateImagedStorageEnv(t *testing.T) {
	path := filepath.Join(t.TempDir(), "imaged-storage.env")
	if err := os.WriteFile(path, []byte("FAAS_OCI_USERNAME=gregale-bot\nFAAS_OCI_PASSWORD=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateImagedStorageEnv(path); err != nil {
		t.Fatalf("valid lifecycle env rejected: %v", err)
	}
	for _, body := range []string{
		"FAAS_OCI_USERNAME=gregale-bot\n",
		"FAAS_OCI_USERNAME=gregale-bot\nFAAS_OCI_PASSWORD=secret\nFAAS_STORAGE_BACKEND=local\n",
		"FAAS_OCI_USERNAME=gregale-bot\nFAAS_OCI_PASSWORD=__SET_FROM_SECRET_STORE__\n",
	} {
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := validateImagedStorageEnv(path); err == nil {
			t.Fatalf("invalid lifecycle env accepted: %q", body)
		}
	}
}

func TestScopeDoctorNodes_IsNodeLocal(t *testing.T) {
	rows := []releaseinstall.ComputeNodeRow{
		{Name: "fsn-2.faas"},
		{Name: "fsn-3.faas"},
	}
	scoped, found := scopeDoctorNodes(rows, "fsn-3.faas")
	if !found || len(scoped) != 1 || scoped[0].Name != "fsn-3.faas" {
		t.Fatalf("scopeDoctorNodes = %#v, found=%v", scoped, found)
	}
	if scoped, found := scopeDoctorNodes(rows, "missing.faas"); found || len(scoped) != 0 {
		t.Fatalf("missing node scope = %#v, found=%v; want not found", scoped, found)
	}
}

func TestReleaseAssetPath_FindsCanonicalSibling(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, releaseTarballName)
	if err := os.WriteFile(filepath.Join(dir, releaseSigName), []byte("sig"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := releaseAssetPath(tarball, releaseSigName)
	if err != nil {
		t.Fatalf("releaseAssetPath: %v", err)
	}
	if got != filepath.Join(dir, releaseSigName) {
		t.Errorf("path = %q", got)
	}
}

func TestCopyTrustBundleNeverCopiesCAKey(t *testing.T) {
	source := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(source, false)
	if err != nil {
		t.Fatal(err)
	}
	extra := pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		var err error
		if pki.RoleUsesNodeIdentity(role) {
			err = pki.EnsureLeafWithCNAndSANs(source, role, "fsn-2.faas", caCert, caKey, false, extra)
		} else {
			err = pki.EnsureLeafWithSANs(source, role, caCert, caKey, false, extra)
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "trust")
	if err := copyTrustBundle(source, destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("copyTrustBundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.key")); !os.IsNotExist(err) {
		t.Fatalf("destination CA key stat = %v, want not exist", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.crt")); err != nil {
		t.Fatalf("destination CA cert: %v", err)
	}
}

func TestCopyTrustBundleIssuesMissingEndpointSANLocally(t *testing.T) {
	source := t.TempDir()
	caCert, caKey, err := pki.EnsureCA(source, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		if err := pki.EnsureLeaf(source, role, caCert, caKey, false); err != nil {
			t.Fatal(err)
		}
	}
	destination := filepath.Join(t.TempDir(), "trust")
	extra := pki.AltNames{DNSNames: []string{"fsn-3.gregale.dev"}}
	if err := copyTrustBundle(source, destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("copyTrustBundle: %v", err)
	}
	if err := pki.ValidateTrustBundleForNode(destination, roleComputeOnly, extra, "fsn-2.faas"); err != nil {
		t.Fatalf("destination trust bundle: %v", err)
	}
	if _, err := os.Stat(filepath.Join(destination, "ca", "ca.key")); !os.IsNotExist(err) {
		t.Fatalf("destination CA key stat = %v, want not exist", err)
	}
}

func TestVerifyAndActivateJoinedNodeUsesControlPlaneRow(t *testing.T) {
	st := state.NewMemStore()
	role := roleComputeOnly
	release := "abcdef"
	hash := "sha256:" + strings.Repeat("a", 64)
	certificate := "-----BEGIN CERTIFICATE-----\njoined-node\n-----END CERTIFICATE-----"
	fingerprint := strings.Repeat("b", 64)
	staleHeartbeat := time.Now().Add(-48 * time.Hour)
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:            "fsn-2.faas",
		TargetURL:       "tcp://fsn-2.gregale.dev:50051",
		Role:            &role,
		ReleaseID:       &release,
		ManifestHash:    &hash,
		HostCertificate: &certificate,
		CertFingerprint: &fingerprint,
		LastHeartbeatAt: staleHeartbeat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
		t.Fatal(err)
	}
	old := computeNodesStoreOpener
	t.Cleanup(func() { computeNodesStoreOpener = old })
	computeNodesStoreOpener = func() (state.Store, func(), error) { return st, func() {}, nil }
	report := &deployJoinReport{DatabaseNode: "fsn-2.faas", ReleaseGitSHA: release}
	if err := verifyAndActivateJoinedNode(context.Background(), report, hash); err != nil {
		t.Fatalf("verifyAndActivateJoinedNode: %v", err)
	}
	got, err := st.ComputeNodeByName(context.Background(), "fsn-2.faas")
	if err != nil || !got.Active || !got.LastHeartbeatAt.After(staleHeartbeat) {
		t.Fatalf("row after activation = %#v, err=%v", got, err)
	}
}

func TestVerifyAndActivateJoinedNodeRejectsUnstampedIdentity(t *testing.T) {
	st := state.NewMemStore()
	role := roleComputeOnly
	release := "abcdef"
	hash := "sha256:" + strings.Repeat("a", 64)
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:         "fsn-2.faas",
		TargetURL:    "tcp://fsn-2.gregale.dev:50051",
		Role:         &role,
		ReleaseID:    &release,
		ManifestHash: &hash,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
		t.Fatal(err)
	}
	old := computeNodesStoreOpener
	t.Cleanup(func() { computeNodesStoreOpener = old })
	computeNodesStoreOpener = func() (state.Store, func(), error) { return st, func() {}, nil }
	report := &deployJoinReport{DatabaseNode: "fsn-2.faas", ReleaseGitSHA: release}
	if err := verifyAndActivateJoinedNode(context.Background(), report, hash); err == nil || !strings.Contains(err.Error(), "host_certificate is empty") {
		t.Fatalf("verifyAndActivateJoinedNode error = %v, want unstamped identity guard", err)
	}
}

func TestDeactivateJoinedNodeReturnsActivatedRowToDrained(t *testing.T) {
	st := state.NewMemStore()
	role := roleComputeOnly
	row, err := st.UpsertComputeNodeFromOperator(context.Background(), state.ComputeNode{
		Name:      "fsn-2.faas",
		TargetURL: "tcp://fsn-2.gregale.dev:50051",
		Role:      &role,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := computeNodesStoreOpener
	t.Cleanup(func() { computeNodesStoreOpener = old })
	computeNodesStoreOpener = func() (state.Store, func(), error) { return st, func() {}, nil }
	if err := deactivateJoinedNode(context.Background(), &deployJoinReport{DatabaseNode: row.Name}); err != nil {
		t.Fatal(err)
	}
	got, err := st.ComputeNodeByName(context.Background(), row.Name)
	if err != nil || got.Active {
		t.Fatalf("row after re-drain = %#v, err=%v", got, err)
	}
}

func TestDeployJoinApply_RendersProviderConnectionOverride(t *testing.T) {
	manifestPath := splitboxJoinManifest(t)
	repo := t.TempDir()
	ansibleDir := filepath.Join(repo, "deploy", "ansible")
	if err := os.MkdirAll(ansibleDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "node_join.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "bootstrap.yml"), []byte("---\n- hosts: compute_nodes\n  roles:\n    - role: compute\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "requirements.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"group_vars", "roles/_shared", "roles/compute", "roles/postgres_capacity", "roles/nftables", "roles/control_plane_peer_access"} {
		if err := os.MkdirAll(filepath.Join(ansibleDir, dir), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(ansibleDir, "node_join_control_plane.yml"), []byte("---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	artifactDir := t.TempDir()
	tarball := filepath.Join(artifactDir, releaseTarballName)
	if err := os.WriteFile(tarball, []byte("tarball"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{releaseSigName, releaseSBOMName} {
		if err := os.WriteFile(filepath.Join(artifactDir, name), []byte(name), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	bootstrap := filepath.Join(artifactDir, "gregalectl")
	cosign := filepath.Join(artifactDir, "cosign")
	for _, path := range []string{bootstrap, cosign} {
		if err := os.WriteFile(path, []byte("binary"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	computeDBEnv := filepath.Join(artifactDir, "compute-db.env")
	if err := os.WriteFile(computeDBEnv, []byte("DATABASE_URL=postgres://faas@example/faas\nFAAS_VMMD_DBURL=postgres://faas@example/faas\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	storageEnv := filepath.Join(artifactDir, "storage.env")
	if err := os.WriteFile(storageEnv, []byte("FAAS_STORAGE_BACKEND=oci\nFAAS_STORAGE_LOCAL_PREFIXES=none\nFAAS_REQUIRE_SHARED_ARTIFACTS=1\nFAAS_STORAGE_CACHE_SERVE_STALE=0\nFAAS_OCI_REGISTRY=https://registry.example\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	imagedStorageEnv := filepath.Join(artifactDir, "imaged-storage.env")
	if err := os.WriteFile(imagedStorageEnv, []byte("FAAS_OCI_USERNAME=gregale-bot\nFAAS_OCI_PASSWORD=secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runtimeBasesEnv := filepath.Join(artifactDir, "runtime-bases.env")
	if err := os.WriteFile(runtimeBasesEnv, []byte(
		"FAAS_DEPLOY_BASE_REF_MINIMAL=ghcr.io/example/base-minimal@sha256:0000000000000000000000000000000000000000000000000000000000000000\n"+
			"FAAS_DEPLOY_BASE_REF_DEBIAN_PARENT=ghcr.io/example/base-debian-parent@sha256:7777777777777777777777777777777777777777777777777777777777777777\n"+
			"FAAS_DEPLOY_BASE_REF_NODE22=ghcr.io/example/runner-node22@sha256:1111111111111111111111111111111111111111111111111111111111111111\n"+"FAAS_DEPLOY_BASE_REF_PYTHON312=ghcr.io/example/runner-python312@sha256:2222222222222222222222222222222222222222222222222222222222222222\n"+"FAAS_DEPLOY_BASE_REF_GO124=ghcr.io/example/runner-go124@sha256:3333333333333333333333333333333333333333333333333333333333333333\n"+"FAAS_DEPLOY_BASE_REF_GO124_ALPINE=ghcr.io/example/runner-go124-alpine@sha256:4444444444444444444444444444444444444444444444444444444444444444\n"+"FAAS_DEPLOY_BASE_REF_NODE24=ghcr.io/example/runner-node24@sha256:5555555555555555555555555555555555555555555555555555555555555555\n"+"FAAS_DEPLOY_BASE_REF_PYTHON313=ghcr.io/example/runner-python313@sha256:6666666666666666666666666666666666666666666666666666666666666666\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	fleetIdentity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	fleetAgeKey := filepath.Join(artifactDir, "fleet.age")
	fleetAgeRecipient := filepath.Join(artifactDir, "fleet.age.pub")
	if err := os.WriteFile(fleetAgeKey, []byte(fleetIdentity.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fleetAgeRecipient, []byte(fleetIdentity.Recipient().String()), 0o444); err != nil {
		t.Fatal(err)
	}
	signKey := filepath.Join(artifactDir, "sign.key")
	verifyKey := filepath.Join(artifactDir, "sign-pub.pem")
	for _, path := range []string{signKey, verifyKey} {
		if err := os.WriteFile(path, []byte("key"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pkiDir := filepath.Join(artifactDir, "pki")
	caCertObj, caKeyObj, err := pki.EnsureCA(pkiDir, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, role := range pki.RolesForBox(roleComputeOnly) {
		var err error
		if pki.RoleUsesNodeIdentity(role) {
			err = pki.EnsureLeafWithCNAndSANs(pkiDir, role, "fsn-2.faas", caCertObj, caKeyObj, false, pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}})
		} else {
			err = pki.EnsureLeafWithSANs(pkiDir, role, caCertObj, caKeyObj, false, pki.AltNames{DNSNames: []string{"fsn-2.gregale.dev"}})
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	_, caKey := pki.CARoot(pkiDir)
	if err := os.Remove(caKey); err != nil {
		t.Fatal(err)
	}

	oldRunner := ansiblePlaybookRunner
	oldVerifier := joinControlPlaneVerifier
	oldRegistrar := joinReleaseBundleRegistrar
	oldLookup := joinPrivateAddressLookup
	t.Cleanup(func() {
		ansiblePlaybookRunner = oldRunner
		joinControlPlaneVerifier = oldVerifier
		joinReleaseBundleRegistrar = oldRegistrar
		joinPrivateAddressLookup = oldLookup
	})
	joinControlPlaneVerifier = func(context.Context, *deployJoinReport, string) error { return nil }
	joinReleaseBundleRegistrar = func(context.Context, string, string, string) error { return nil }
	joinPrivateAddressLookup = func(_ context.Context, _, host string) ([]net.IP, error) {
		switch host {
		case "fsn-1.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.1")}, nil
		case "fsn-2.gregale.dev":
			return []net.IP{net.ParseIP("10.42.0.2")}, nil
		default:
			return nil, fmt.Errorf("unexpected private host %s", host)
		}
	}
	var calls [][]string
	var rolloutOverlap float64
	ansiblePlaybookRunner = func(_ context.Context, _ string, args []string) error {
		calls = append(calls, append([]string(nil), args...))
		inventory := ""
		for i := range args {
			if args[i] == "-i" && i+1 < len(args) {
				inventory = args[i+1]
			}
			if args[i] == "-e" && i+1 < len(args) && strings.HasSuffix(args[i+1], "join-vars.json") {
				body, err := os.ReadFile(strings.TrimPrefix(args[i+1], "@"))
				if err != nil {
					return err
				}
				var vars map[string]any
				if err := json.Unmarshal(body, &vars); err != nil {
					return err
				}
				rolloutOverlap, _ = vars["faas_postgres_rollout_overlap_nodes"].(float64)
			}
		}
		if inventory == "" {
			return fmt.Errorf("fake runner: missing inventory")
		}
		hostVars, err := os.ReadFile(filepath.Join(filepath.Dir(inventory), "host_vars", "fsn-2.yml"))
		if err != nil {
			return err
		}
		body := string(hostVars)
		if !strings.Contains(body, `ansible_host: "203.0.113.27"`) {
			return fmt.Errorf("provider SSH address missing from generated host vars:\n%s", body)
		}
		if !strings.Contains(body, `faas_vmmd_target_url: "tcp://fsn-2.gregale.dev:50051"`) {
			return fmt.Errorf("stable runtime endpoint was overwritten:\n%s", body)
		}
		return nil
	}

	report, err := deployJoinValidate(deployJoinOptions{
		ManifestFile:            manifestPath,
		Node:                    "fsn-2",
		SSHHost:                 "203.0.113.27",
		ReleaseTarball:          tarball,
		BootstrapBinary:         bootstrap,
		CosignBinary:            cosign,
		PKISource:               pkiDir,
		SignKeySource:           signKey,
		VerifyKeySource:         verifyKey,
		ComputeDBEnvSource:      computeDBEnv,
		StorageEnvSource:        storageEnv,
		ImagedStorageEnvSource:  imagedStorageEnv,
		RuntimeBasesEnvSource:   runtimeBasesEnv,
		FleetAgeKeySource:       fleetAgeKey,
		FleetAgeRecipientSource: fleetAgeRecipient,
		RepoRoot:                repo,
		PostgresOverlapNodes:    4,
		SkipFleetPreflight:      true,
	})
	if err != nil {
		t.Fatalf("deployJoinValidate: %v", err)
	}
	if code, err := deployJoinApply(&deployJoinOptions{
		ManifestFile:            manifestPath,
		Node:                    "fsn-2",
		SSHHost:                 "203.0.113.27",
		SSHUser:                 "root",
		SSHPort:                 22,
		ReleaseTarball:          tarball,
		BootstrapBinary:         bootstrap,
		CosignBinary:            cosign,
		PKISource:               pkiDir,
		SignKeySource:           signKey,
		VerifyKeySource:         verifyKey,
		ComputeDBEnvSource:      computeDBEnv,
		StorageEnvSource:        storageEnv,
		ImagedStorageEnvSource:  imagedStorageEnv,
		RuntimeBasesEnvSource:   runtimeBasesEnv,
		FleetAgeKeySource:       fleetAgeKey,
		FleetAgeRecipientSource: fleetAgeRecipient,
		RepoRoot:                repo,
		PostgresOverlapNodes:    4,
		SkipFleetPreflight:      true,
	}, &report); err != nil || code != 0 {
		t.Fatalf("deployJoinApply: code=%d err=%v", code, err)
	}
	if len(calls) != 3 {
		t.Fatalf("Ansible calls = %d, want control-plane convergence, limited join, and baseline acceptance", len(calls))
	}
	if rolloutOverlap != 4 {
		t.Fatalf("faas_postgres_rollout_overlap_nodes = %v, want 4", rolloutOverlap)
	}
	controlPlane := strings.Join(calls[0], " ")
	if !strings.Contains(controlPlane, "--limit control_plane") || !strings.Contains(controlPlane, "node_join_control_plane.yml") {
		t.Fatalf("Ansible args missing control-plane limit/playbook: %v", calls[0])
	}
	joined := strings.Join(calls[1], " ")
	if !strings.Contains(joined, "--limit fsn-2") || !strings.Contains(joined, "node_join.yml") {
		t.Fatalf("Ansible args missing node limit/playbook: %v", calls[1])
	}
	accepted := strings.Join(calls[2], " ")
	if !strings.Contains(accepted, "--limit fsn-2") || !strings.Contains(accepted, "node_join_accept_release.yml") {
		t.Fatalf("Ansible args missing post-activation baseline acceptance: %v", calls[2])
	}
	if !report.Applied {
		t.Fatal("apply report was not marked applied")
	}
}
