package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

func TestJoinFleetWorkerCount(t *testing.T) {
	tests := []struct {
		maxParallel int
		nodeCount   int
		want        int
	}{
		{maxParallel: 4, nodeCount: 10, want: 4},
		{maxParallel: 8, nodeCount: 3, want: 3},
		{maxParallel: 1, nodeCount: 12, want: 1},
	}
	for _, tt := range tests {
		if got := joinFleetWorkerCount(tt.maxParallel, tt.nodeCount); got != tt.want {
			t.Fatalf("joinFleetWorkerCount(%d, %d) = %d, want %d", tt.maxParallel, tt.nodeCount, got, tt.want)
		}
	}
}

func TestRunJoinFleetPreflightChecksEffectivePostgresOverlap(t *testing.T) {
	manifestYAML := strings.Replace(validManifestYAML,
		"    - name: fsn-1\n      role: control-plane\n",
		"    - name: fsn-1\n      role: control-plane\n      address: fsn-1.gregale.dev:7100\n    - name: fsn-2\n      role: compute-only\n      address: fsn-2.gregale.dev:50051\n", 1)
	manifestPath := writeSplitboxManifest(t, manifestYAML)

	oldRunner := ansiblePlaybookRunner
	t.Cleanup(func() { ansiblePlaybookRunner = oldRunner })
	var calls [][]string
	ansiblePlaybookRunner = func(_ context.Context, _ string, args []string) error {
		calls = append(calls, append([]string(nil), args...))
		return nil
	}

	opts := []deployJoinOptions{{
		ManifestFile: manifestPath, Node: "fsn-2", SSHHost: "203.0.113.2",
		RepoRoot: "/repo", PostgresOverlapNodes: 4,
	}}
	if err := runJoinFleetPreflight(context.Background(), opts); err != nil {
		t.Fatalf("runJoinFleetPreflight: %v", err)
	}
	if len(calls) != 2 {
		t.Fatalf("Ansible calls = %d, want capacity check plus host preflight", len(calls))
	}
	for i, call := range calls {
		joined := strings.Join(call, " ")
		if !strings.Contains(joined, "faas_postgres_rollout_overlap_nodes=4") {
			t.Fatalf("Ansible call %d lacks rollout overlap: %v", i, call)
		}
	}
	if got := strings.Join(calls[0], " "); !strings.Contains(got, "--check") || !strings.Contains(got, "scale_check.yml") {
		t.Fatalf("first Ansible call is not the capacity check: %v", calls[0])
	}
	if got := strings.Join(calls[1], " "); !strings.Contains(got, "preflight.yml") {
		t.Fatalf("second Ansible call is not the host preflight: %v", calls[1])
	}
}

func TestLoadJoinFleetFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.yaml")
	if err := os.WriteFile(path, []byte("nodes:\n  - node: fsn-3\n    ssh_host: 203.0.113.3\n  - node: fsn-4\n    ssh_host: 203.0.113.4\n    ssh_port: 2222\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := loadJoinFleetFile(path)
	if err != nil {
		t.Fatalf("loadJoinFleetFile: %v", err)
	}
	if len(file.Nodes) != 2 || file.Nodes[1].SSHPort != 2222 {
		t.Fatalf("nodes = %#v", file.Nodes)
	}
}

func TestLoadJoinFleetInputsFromClaim(t *testing.T) {
	path := filepath.Join(t.TempDir(), "claim.yaml")
	claim := `api_version: gregale.dev/v1alpha1
kind: ComputeNodeClaim
metadata:
  name: fsn-3
spec:
  ssh:
    host: 203.0.113.27
    user: deploy
    port: 2222
  storage:
    device: /dev/disk/by-id/data
    format: true
`
	if err := os.WriteFile(path, []byte(claim), 0o600); err != nil {
		t.Fatal(err)
	}
	file, err := loadJoinFleetInputs("", path)
	if err != nil {
		t.Fatalf("loadJoinFleetInputs: %v", err)
	}
	if len(file.Nodes) != 1 {
		t.Fatalf("nodes = %#v", file.Nodes)
	}
	n := file.Nodes[0]
	if n.Node != "fsn-3" || n.SSHHost != "203.0.113.27" || n.SSHUser != "deploy" || n.SSHPort != 2222 {
		t.Fatalf("connection = %#v", n)
	}
	if n.StorageDevice != "/dev/disk/by-id/data" || !n.FormatStorage {
		t.Fatalf("storage = %#v", n)
	}
}

func TestResolveJoinArtifactsDoesNotOverrideExplicitPaths(t *testing.T) {
	artifactDir := t.TempDir()
	explicit := filepath.Join(t.TempDir(), "custom.tar.gz")
	opts := deployJoinOptions{ArtifactDir: artifactDir, ReleaseTarball: explicit}
	resolveJoinArtifacts(&opts)
	if opts.ReleaseTarball != explicit {
		t.Fatalf("explicit release tarball was replaced: %q", opts.ReleaseTarball)
	}
	if opts.BootstrapBinary != filepath.Join(artifactDir, "gregalectl-linux-amd64") {
		t.Fatalf("bootstrap binary = %q", opts.BootstrapBinary)
	}
	if opts.PKISource != filepath.Join(artifactDir, "pki") {
		t.Fatalf("PKI source = %q", opts.PKISource)
	}
}

func TestResolveJoinArtifactsTwelveNodesShareFleetSealPair(t *testing.T) {
	artifactDir := t.TempDir()
	fleet, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	keyPath := filepath.Join(artifactDir, "fleet.age")
	recipientPath := filepath.Join(artifactDir, "fleet.age.pub")
	if err := os.WriteFile(keyPath, []byte(fleet.String()), 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(recipientPath, []byte(fleet.Recipient().String()), 0o444); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 12; i++ {
		opts := deployJoinOptions{ArtifactDir: artifactDir, Node: fmt.Sprintf("compute-%02d", i)}
		resolveJoinArtifacts(&opts)
		if opts.FleetAgeKeySource != keyPath || opts.FleetAgeRecipientSource != recipientPath {
			t.Fatalf("node %d resolved different fleet artifacts: %+v", i, opts)
		}
		if err := validateFleetAgePair(opts.FleetAgeKeySource, opts.FleetAgeRecipientSource); err != nil {
			t.Fatalf("node %d fleet pair: %v", i, err)
		}
	}
}
