package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Per-role compute convergence (docs/adr/20261115120000-full-release-converge-speed.md):
// CD names the compute roles whose content hash differs from the node's
// record, and node_join converges only those on a managed host, then
// records the release's hashes beside the bootstrap contract.

// splitJoinRoles parses --roles: comma-separated, blanks dropped.
func splitJoinRoles(value string) []string {
	var roles []string
	for _, role := range strings.Split(value, ",") {
		if role = strings.TrimSpace(role); role != "" {
			roles = append(roles, role)
		}
	}
	return roles
}

// joinRolesVar is faas_join_converge_roles_requested: the role list, or an
// empty list, which node_join reads as every role.
func joinRolesVar(roles []string) []string {
	if roles == nil {
		return []string{}
	}
	return roles
}

// requireComputeRoles refuses a role the compute play does not run:
// bootstrap.yml would skip it silently and node_join would record its hash
// as converged.
func requireComputeRoles(ansibleDir string, roles []string) error {
	if len(roles) == 0 {
		return nil
	}
	body, err := os.ReadFile(filepath.Join(ansibleDir, "bootstrap.yml"))
	if err != nil {
		return fmt.Errorf("read compute bootstrap playbook: %w", err)
	}
	_, known, err := computeBootstrapContract(body)
	if err != nil {
		return fmt.Errorf("select compute bootstrap contract: %w", err)
	}
	set := make(map[string]struct{}, len(known))
	for _, role := range known {
		set[role] = struct{}{}
	}
	for _, role := range roles {
		if !controlPlaneRoleName.MatchString(role) {
			return fmt.Errorf("--roles: %q is not a role name", role)
		}
		if _, ok := set[role]; !ok {
			return fmt.Errorf("--roles: the compute play does not run role %q", role)
		}
	}
	return nil
}

// readComputeRoleRecord reads --role-record-file: the compute group's role
// hashes as scripts/ci/host_roles.py writes them. It returns the file as
// node_join records it, or "" without one.
func readComputeRoleRecord(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read --role-record-file: %w", err)
	}
	var record struct {
		FormatVersion json.RawMessage   `json:"format_version"`
		Group         string            `json:"group"`
		Globals       string            `json:"globals"`
		Roles         map[string]string `json:"roles"`
		Order         []string          `json:"order"`
	}
	if err := json.Unmarshal(body, &record); err != nil {
		return "", fmt.Errorf("--role-record-file is not JSON: %w", err)
	}
	if record.Group != "compute_nodes" || len(record.FormatVersion) == 0 || record.Globals == "" || len(record.Roles) == 0 || len(record.Order) == 0 {
		return "", errors.New("--role-record-file is not a compute_nodes role-hash record (scripts/ci/host_roles.py hashes --group compute_nodes)")
	}
	return string(body), nil
}

// joinContractWithEnvironment is the compute bootstrap contract: the
// repository contract (joinBootstrapContractHash) and the environment hash
// (controlPlaneEnvironmentHash over the manifest-rendered inventory and the
// operator vars). Without the environment a manifest-only change, such as
// rc.364 moving the apps domain to gregale.app, left the contract current:
// the compute roles never re-rendered the gatewayd domain layout, the
// serving probe URL or the edge control-plane target.
func joinContractWithEnvironment(repoContract, environment string) string {
	hash := sha256.New()
	for _, field := range []string{"gregale-compute-contract/2", repoContract, environment} {
		_, _ = io.WriteString(hash, field)
		_, _ = hash.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil))
}

// withJoinRecordEnvironment adds the environment hash to the role record
// node_join persists, so the next per-role convergence can tell that the
// environment moved since the record and converge every role instead of
// only the roles whose repository content changed.
func withJoinRecordEnvironment(record, environment string) (string, error) {
	if record == "" {
		return "", nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(record), &fields); err != nil {
		return "", fmt.Errorf("--role-record-file is not JSON: %w", err)
	}
	value, err := json.Marshal(environment)
	if err != nil {
		return "", fmt.Errorf("encode role record environment: %w", err)
	}
	fields["environment"] = value
	body, err := json.MarshalIndent(fields, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode role record: %w", err)
	}
	return string(body) + "\n", nil
}
