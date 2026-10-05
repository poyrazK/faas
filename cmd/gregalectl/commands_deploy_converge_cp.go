package main

// deploy converge-control-plane (ADR-580) runs the control-plane play of
// deploy/ansible/bootstrap.yml from CD when its inputs changed. Before it,
// control-plane roles (packages, role-rendered drop-ins, Alertmanager,
// Caddy, nftables, PostgreSQL settings) only changed when an operator ran
// `make bootstrap-control-plane` from a workstation, so merged role fixes
// silently never reached production.
//
// It reuses the join pipeline's pieces: the manifest-rendered inventory,
// DNS-resolved private peer addresses (no SSH to compute hosts), and a
// verified known_hosts file. The contract hash covers every input the play
// reads, including the rendered inventory and the operator vars file, so a
// variable-only change converges too. control_plane_converge.yml ends early
// on an unchanged contract and records the hash only after success.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
)

const controlPlaneConvergePlaybook = "control_plane_converge.yml"

type deployConvergeControlPlaneOptions struct {
	ManifestFile      string
	SSHKnownHostsFile string
	SSHUser           string
	SSHKey            string
	AnsibleVarsFile   string
	RepoRoot          string
}

type deployConvergeControlPlaneReport struct {
	Host       string `json:"host"`
	Contract   string `json:"contract_sha256"`
	DurationMS int64  `json:"duration_ms"`
}

func cmdDeployConvergeControlPlane(args []string) int {
	fs := flag.NewFlagSet("deploy converge-control-plane", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestFile := fs.String("manifest-file", "", "signed split-box manifest (required)")
	knownHosts := fs.String("ssh-known-hosts-file", "", "verified known_hosts covering the control-plane host (required)")
	sshUser := fs.String("ssh-user", "root", "SSH user for the control-plane host")
	sshKey := fs.String("ssh-key", "", "SSH private key (default: Ansible's own selection)")
	varsFile := fs.String("ansible-vars-file", "", "control-plane Ansible variables, e.g. prod-vars.yml (required)")
	repoRoot := fs.String("repo-root", "", "repository root containing deploy/ansible (default: detected)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	opts := deployConvergeControlPlaneOptions{
		ManifestFile:      strings.TrimSpace(*manifestFile),
		SSHKnownHostsFile: strings.TrimSpace(*knownHosts),
		SSHUser:           strings.TrimSpace(*sshUser),
		SSHKey:            strings.TrimSpace(*sshKey),
		AnsibleVarsFile:   strings.TrimSpace(*varsFile),
		RepoRoot:          strings.TrimSpace(*repoRoot),
	}
	if err := opts.validate(); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy converge-control-plane: %v\n", err)
		return 2
	}
	report, err := convergeControlPlane(context.Background(), opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy converge-control-plane: %v\n", err)
		return 3
	}
	if *jsonOut || jsonOutput {
		jsonEmit(os.Stdout, report)
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "deploy converge-control-plane: host=%s contract=%s duration=%dms\n", report.Host, report.Contract, report.DurationMS)
	return 0
}

func (o deployConvergeControlPlaneOptions) validate() error {
	for flagName, path := range map[string]string{
		"--manifest-file":        o.ManifestFile,
		"--ssh-known-hosts-file": o.SSHKnownHostsFile,
		"--ansible-vars-file":    o.AnsibleVarsFile,
	} {
		if path == "" {
			return fmt.Errorf("%s is required", flagName)
		}
		if _, err := os.Stat(path); err != nil {
			return fmt.Errorf("%s: %w", flagName, err)
		}
	}
	if o.SSHKey != "" {
		if _, err := os.Stat(o.SSHKey); err != nil {
			return fmt.Errorf("--ssh-key: %w", err)
		}
	}
	if o.SSHUser == "" {
		return errors.New("--ssh-user must not be empty")
	}
	return nil
}

func convergeControlPlane(ctx context.Context, opts deployConvergeControlPlaneOptions) (deployConvergeControlPlaneReport, error) {
	started := time.Now()
	var report deployConvergeControlPlaneReport
	if opts.RepoRoot == "" {
		opts.RepoRoot = defaultRepoRoot()
	}
	ansibleDir := filepath.Join(opts.RepoRoot, "deploy/ansible")
	if _, err := os.Stat(filepath.Join(ansibleDir, controlPlaneConvergePlaybook)); err != nil {
		return report, fmt.Errorf("repository has no %s: %w", controlPlaneConvergePlaybook, err)
	}

	m, err := manifest.Load(opts.ManifestFile)
	if err != nil {
		return report, err
	}
	controlPlane, err := soleControlPlaneHost(m)
	if err != nil {
		return report, err
	}
	report.Host = controlPlane.Name

	tempRoot, err := os.MkdirTemp("", "gregale-cp-converge-")
	if err != nil {
		return report, fmt.Errorf("create temporary inventory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()

	// Only the control plane is contacted, so its key is the one that must
	// be verified; peers are reached through DNS, not SSH.
	cpOnly := *m
	cpOnly.Fleet.Hosts = []manifest.Host{controlPlane}
	if err := requireFleetKnownHosts(opts.SSHKnownHostsFile, &cpOnly, "", "", 0); err != nil {
		return report, err
	}

	files, err := renderManifestAnsibleFiles(m, tempRoot)
	if err != nil {
		return report, fmt.Errorf("render temporary inventory: %w", err)
	}
	privateAddresses, err := resolveJoinPrivateAddresses(ctx, m, joinPrivateAddressLookup)
	if err != nil {
		return report, fmt.Errorf("resolve fleet private addresses: %w", err)
	}
	seedJoinPrivateAddressFacts(files, privateAddresses)
	for _, file := range files {
		if err := writeGeneratedAnsibleFile(file.Path, file.Body, true); err != nil {
			return report, fmt.Errorf("write temporary inventory: %w", err)
		}
	}

	report.Contract, err = controlPlaneBootstrapContractHash(ansibleDir, tempRoot, files, opts.AnsibleVarsFile)
	if err != nil {
		return report, err
	}
	convergeVars := map[string]any{
		"faas_cp_bootstrap_contract_sha256": report.Contract,
		// The deploy stage restarts and verifies every FaaS daemon after it
		// activates the release. Converging first must not restart them on
		// the outgoing binaries against already-staged new configuration.
		"faas_join_defer_service_handlers": true,
	}
	convergeVarsPath := filepath.Join(tempRoot, "converge-vars.json")
	body, err := json.Marshal(convergeVars)
	if err != nil {
		return report, fmt.Errorf("encode converge variables: %w", err)
	}
	if err := os.WriteFile(convergeVarsPath, body, 0o600); err != nil {
		return report, fmt.Errorf("write converge variables: %w", err)
	}

	args := []string{
		"-i", filepath.Join(tempRoot, "inventory", "hosts.ini"),
		"--ssh-common-args", "-o UserKnownHostsFile=" + opts.SSHKnownHostsFile + " -o StrictHostKeyChecking=yes",
		"--user", opts.SSHUser,
	}
	if opts.SSHKey != "" {
		args = append(args, "--private-key", opts.SSHKey)
	}
	args = append(args,
		"-e", "@"+opts.AnsibleVarsFile,
		"-e", "@"+convergeVarsPath,
		"--limit", "control_plane",
		filepath.Join(ansibleDir, controlPlaneConvergePlaybook),
	)
	if err := ansiblePlaybookRunner(ctx, ansibleDir, args); err != nil {
		return report, fmt.Errorf("control-plane convergence: %w", err)
	}
	report.DurationMS = time.Since(started).Milliseconds()
	return report, nil
}

func soleControlPlaneHost(m *manifest.Manifest) (manifest.Host, error) {
	var found []manifest.Host
	for _, host := range m.Fleet.Hosts {
		if host.Role == roleControlPlane {
			found = append(found, host)
		}
	}
	if len(found) != 1 {
		return manifest.Host{}, fmt.Errorf("manifest declares %d control-plane hosts; want exactly 1", len(found))
	}
	return found[0], nil
}

// controlPlaneBootstrapContractHash covers every input the control-plane
// play reads: its plays and roles, the shared task libraries, the Ansible
// configuration and collection pins, the manifest-rendered inventory
// (relative paths, so the temporary directory does not change the hash),
// and the operator vars file. The release itself is not an input; release
// activation stays with the deploy stage.
func controlPlaneBootstrapContractHash(ansibleDir, inventoryRoot string, inventory []manifestAnsibleFile, varsFile string) (string, error) {
	bootstrapBody, err := os.ReadFile(filepath.Join(ansibleDir, "bootstrap.yml"))
	if err != nil {
		return "", fmt.Errorf("read control-plane bootstrap playbook: %w", err)
	}
	plays, roleNames, err := bootstrapPlayContract(bootstrapBody, "control_plane")
	if err != nil {
		return "", fmt.Errorf("select control-plane bootstrap contract: %w", err)
	}
	roots := append([]string{controlPlaneConvergePlaybook, "ansible.cfg", "requirements.yml"}, bootstrapSharedContractRoots("control_plane")...)
	for _, roleName := range roleNames {
		roots = append(roots, filepath.Join("roles", roleName))
	}
	paths, err := ansibleContractFiles(ansibleDir, roots)
	if err != nil {
		return "", err
	}

	hash := sha256.New()
	writeField := func(name string, body []byte) {
		_, _ = io.WriteString(hash, name)
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(body)
		_, _ = hash.Write([]byte{0})
	}
	writeField("bootstrap.control_plane.yml", plays)
	for _, path := range paths {
		body, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read control-plane contract input %s: %w", path, readErr)
		}
		rel, relErr := filepath.Rel(ansibleDir, path)
		if relErr != nil {
			return "", fmt.Errorf("relativize control-plane contract input %s: %w", path, relErr)
		}
		writeField(filepath.ToSlash(rel), body)
	}
	rendered := append([]manifestAnsibleFile(nil), inventory...)
	sort.Slice(rendered, func(i, j int) bool { return rendered[i].Path < rendered[j].Path })
	for _, file := range rendered {
		rel, relErr := filepath.Rel(inventoryRoot, file.Path)
		if relErr != nil {
			return "", fmt.Errorf("relativize rendered inventory %s: %w", file.Path, relErr)
		}
		writeField("rendered/"+filepath.ToSlash(rel), file.Body)
	}
	vars, err := os.ReadFile(varsFile)
	if err != nil {
		return "", fmt.Errorf("read control-plane Ansible variables: %w", err)
	}
	writeField("operator-vars", vars)
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}
