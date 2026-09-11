// commands_deploy.go — `gregalectl deploy` subcommand dispatcher.
//
// Closes multi-host scale-out gap #2 (companion to gap #1 closed
// by `gregalectl compute-nodes add` in commands_compute_nodes.go).
// Today, adding a compute node is a 5-step hand-curated runbook:
//
//	1. POST /v1/compute-nodes via curl (closed by gap #1 / PR-A)
//	2. Edit deploy/ansible/inventory/hosts.ini to add <fqdn>
//	3. Write deploy/ansible/host_vars/<fqdn>.yml from the
//	   existing faas-fsn-{1,2}.yml template
//	4. git commit the inventory delta
//	5. ssh <fqdn> 'sudo make bootstrap-{control-plane,compute}'
//
// This dispatcher collapses steps 2-5 into a single Go-side
// coordinator (`gregalectl deploy add-node <fqdn>`) that:
//
//   - writes the per-host host_vars file from a Go-side template
//     literal (single source of truth for the YAML shape; both
//     existing files drift-tested against it via assertHostVarsShape)
//   - updates hosts.ini in place to add <fqdn> to the right group
//     (idempotent: re-running with the same <fqdn> no-ops)
//   - commits the repo delta (`git commit -m "feat(ansible): add <fqdn> <role>"`)
//   - invokes `ssh <ssh-target> 'sudo make bootstrap-<role>'` over
//     the wire (streams ansible output to stderr so the operator
//     sees every task result)
//   - on bootstrap success, POSTs the compute_nodes row via the
//     PR-A `gregalectl compute-nodes add --from-file=...` bridge
//     so the operator's target_url wins on the vmmd-side UPSERT
//
// Rollback: bootstrap failure leaves the repo committed but the
// box unbootstrapped. Operator runs `git revert <commit>` and
// SSHs in by hand. The CLI does NOT auto-revert — partial-success
// states need operator eyes (per CLAUDE.md "a half-bootstrapped
// box is worse than a known-bad one").

package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// dispatchDeploy is wired into cmd/gregalectl/main.go:switch
// alongside the other dispatch* consts.
const dispatchDeploy = "deploy"

// roleComputeOnly and roleControlPlane are the canonical role
// strings used throughout deploy add-node. Extracted as consts so
// goconst doesn't flag the 6-occurrence literal; also matches
// the `ansibleGroup` mapping in commands_deploy_files.go which
// already keys on these values.
const (
	roleControlPlane = "control-plane"
	roleComputeOnly  = "compute-only"
)

// cmdDeployDispatch fans to the legacy add-node path and the
// provider-neutral join-node pipeline. Matches the
// (args []string) int signature every other dispatch* arm uses.
func cmdDeployDispatch(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy: missing subcommand; want claim|fleet-bundle|prepare-node|join-node|join-fleet|add-node")
		return 2
	}
	switch args[0] {
	case "claim":
		return cmdDeployClaim(args[1:])
	case "fleet-bundle":
		return cmdDeployFleetBundle(args[1:])
	case "prepare-node":
		return cmdDeployPrepareNode(args[1:])
	case "join-node":
		return cmdDeployJoinNode(args[1:])
	case "join-fleet":
		return cmdDeployJoinFleet(args[1:])
	case "rollback-node":
		return cmdDeployRollbackNode(args[1:])
	case "add-node":
		return cmdDeployAddNode(args[1:])
	default:
		fmt.Fprintf(os.Stderr, "gregalectl deploy: unknown subcommand %q\n", args[0])
		return 2
	}
}

// sshRunner is the seam tests use to swap in a fake ssh command
// (e.g. one that exits 1 to exercise the bootstrap-failure path).
// Production wires this to the real ssh binary; tests assign
// `sshRunner = func(...)` directly (the var is package-private;
// there's no setSSHRunner — keeping the swap surface minimal).
// Indirection matches the state-seam pattern in
// commands_compute_nodes.go.
var sshRunner = defaultSSHRunner

// defaultSSHRunner runs `ssh <target> <args...>` and streams
// stdout+stderr to the caller. Returns (stdout, stderr, err).
func defaultSSHRunner(target string, args []string) ([]byte, []byte, error) {
	cmd := exec.Command("ssh", append([]string{target}, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return stdout.Bytes(), stderr.Bytes(), err
}

// gitRunner is the seam tests use to swap in a fake git binary.
// Production wires this to the real git; tests wire to a
// no-op stub via setGitRunner. The seam returns (stdout, err)
// so the report's commit_sha can be captured without the
// previous bypass-through-exec.Command (which broke tests).
var gitRunner = defaultGitRunner

func setGitRunner(fn func(repoRoot string, args ...string) (stdout []byte, err error)) {
	gitRunner = fn
}

// defaultGitRunner runs `git -C <repoRoot> <args...>` in the
// caller's repo and captures stdout. Stderr still streams to
// os.Stderr so the operator sees git errors in their terminal.
func defaultGitRunner(repoRoot string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", repoRoot}, args...)...)
	cmd.Stderr = os.Stderr
	return cmd.Output()
}

// sshRunnerWithOverride is the seam addNodeExecute calls. It
// delegates to sshRunner (the test-swappable var) — keeping the
// seam in one place so tests don't have to register two layers.
func sshRunnerWithOverride(target string, args []string) ([]byte, []byte, error) {
	return sshRunner(target, args)
}

// cmdDeployAddNode is the operator-side coordinator for adding a
// node to the fleet. The four-step flow is in addNodeExecute; this
// function is the flag parser + pre-flight gate.
func cmdDeployAddNode(args []string) int {
	fs := flag.NewFlagSet("deploy add-node", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	role := fs.String("role", roleComputeOnly, "control-plane or compute-only (default: compute-only)")
	ansibleHost := fs.String("ansible-host", "", "cross-box dial target (Layer-3 mesh endpoint; required)")
	publicIface := fs.String("public-iface", "", "nftables substitution iface (compute-only only; e.g. ens5)")
	masqCIDR := fs.String("masquerade-cidr", "", "per-host overlay CIDR (compute-only only; e.g. 10.102.0.0/16)")
	masqCIDRv6 := fs.String("masquerade-cidr-v6", "", "ULA pool (compute-only only; e.g. fc00::/7)")
	overlayCIDRs := fs.String("overlay-cidrs", "", "comma-separated multi-host mesh entries (compute-only only)")
	targetURL := fs.String("target-url", "", "compute_nodes target_url (compute-only only; e.g. tcp://vmmd-N.faas:50051)")
	vpcpus := fs.Int("vpcpus", 0, "compute_nodes row vCPU count (compute-only only)")
	memMB := fs.Int("mem-mb", 0, "compute_nodes row RAM MB (compute-only only)")
	maxConc := fs.Int("max-concurrency", 0, "compute_nodes row max concurrent live instances (compute-only only)")
	admCeil := fs.Int("admission-ceiling-mb", 0, "compute_nodes row tenant RAM admission ceiling (compute-only only)")
	sshTarget := fs.String("ssh", "", "SSH target for bootstrap (default: gregale@<fqdn>)")
	skipBootstrap := fs.Bool("skip-bootstrap", false, "write host_vars + commit but do not SSH bootstrap")
	skipPOST := fs.Bool("skip-compute-nodes-post", false, "do not POST the compute_nodes row after bootstrap")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	yes := fs.Bool("yes", false, "skip the pre-flight confirmation prompt")
	repoRoot := fs.String("repo-root", "", "path to the cloned faas repo (default: ascend two levels from the binary's location)")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	// Positionals: the <fqdn> is the only positional, required.
	positional := fs.Args()
	if len(positional) != 1 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: <fqdn> positional required (e.g. faas-fsn-3)")
		return 2
	}
	fqdn := positional[0]

	// Validation mirrors the apid handler's 400 surface for the
	// compute-node-shaped fields. The role is the load-bearing
	// switch: control-plane adds to [control_plane], compute-only
	// to [compute_nodes], and the host_vars shape differs.
	switch *role {
	case roleControlPlane, roleComputeOnly:
	default:
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: --role must be control-plane or compute-only (got %q)\n", *role)
		return 2
	}
	if *ansibleHost == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: --ansible-host required")
		return 2
	}
	if *role == roleComputeOnly {
		if *publicIface == "" {
			fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: --public-iface required for compute-only")
			return 2
		}
		if *masqCIDR == "" {
			fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: --masquerade-cidr required for compute-only")
			return 2
		}
		if *targetURL == "" {
			fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: --target-url required for compute-only")
			return 2
		}
		if *vpcpus <= 0 || *memMB <= 0 || *maxConc <= 0 || *admCeil <= 0 {
			fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: --vpcpus, --mem-mb, --max-concurrency, --admission-ceiling-mb must all be > 0 for compute-only")
			return 2
		}
	}

	// Resolve repo root. Default: ascend from the binary's
	// location (e.g. ./bin/gregalectl → ../..). Operators can
	// override via --repo-root for non-standard layouts.
	rr := *repoRoot
	if rr == "" {
		rr = defaultRepoRoot()
	}
	if _, err := os.Stat(filepath.Join(rr, "deploy/ansible/bootstrap.yml")); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: repo root %q does not contain deploy/ansible/bootstrap.yml (use --repo-root): %v\n", rr, err)
		return 2
	}
	// Idempotency check: if host_vars/<fqdn>.yml already exists AND
	// matches what we'd write, no-op. If it differs, refuse — the
	// operator must git revert or delete the file first.
	hostVarsPath := filepath.Join(rr, "deploy/ansible/host_vars", fqdn+".yml")
	desiredHostVars := renderHostVarsYAMLWithTargetURL(fqdn, *role, *ansibleHost, *publicIface, *masqCIDR, *masqCIDRv6, *overlayCIDRs, *targetURL)
	if existing, err := os.ReadFile(hostVarsPath); err == nil {
		if !bytes.Equal(existing, []byte(desiredHostVars)) {
			fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: host_vars/%s.yml already exists with different content; refusing to overwrite (git revert <commit> or delete the file first)\n", fqdn)
			return 1
		}
		// Same content — idempotent no-op.
	} else if !os.IsNotExist(err) {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: stat host_vars/%s.yml: %v\n", fqdn, err)
		return 1
	}

	// Pre-flight summary. The operator sees the diff that will
	// land before confirming (unless --yes).
	if !*yes {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: will perform the following steps:\n")
		fmt.Fprintf(os.Stderr, "  1. write deploy/ansible/host_vars/%s.yml\n", fqdn)
		fmt.Fprintf(os.Stderr, "  2. update deploy/ansible/inventory/hosts.ini (add %s to [%s])\n", fqdn, ansibleGroup(*role))
		fmt.Fprintf(os.Stderr, "  3. git commit in %s\n", rr)
		if !*skipBootstrap {
			fmt.Fprintf(os.Stderr, "  4. ssh %s 'sudo make bootstrap-%s'\n", defaultSSHTarget(fqdn, *sshTarget), bootstrapTarget(*role))
			if !*skipPOST {
				fmt.Fprintf(os.Stderr, "  5. (after bootstrap) gregalectl compute-nodes add --from-file=<scratch>\n")
			}
		}
		fmt.Fprintf(os.Stderr, "Re-run with --yes to proceed.\n")
		return 2
	}

	report, code := addNodeExecute(addNodeParams{
		FQDN:            fqdn,
		Role:            *role,
		AnsibleHost:     *ansibleHost,
		PublicIface:     *publicIface,
		MasqCIDR:        *masqCIDR,
		MasqCIDRv6:      *masqCIDRv6,
		OverlayCIDRs:    *overlayCIDRs,
		TargetURL:       *targetURL,
		VPCPUs:          *vpcpus,
		MemMB:           *memMB,
		MaxConcurrency:  *maxConc,
		AdmCeil:         *admCeil,
		SSHTarget:       *sshTarget,
		SkipBootstrap:   *skipBootstrap,
		SkipPOST:        *skipPOST,
		JSONOut:         *jsonOut,
		RepoRoot:        rr,
		HostVarsPath:    hostVarsPath,
		DesiredHostVars: desiredHostVars,
	})
	if code != 0 {
		return code
	}
	if *jsonOut {
		jsonEmit(os.Stdout, report)
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "OK fqdn=%s role=%s host_vars=%s commit=%s bootstrap_passed=%t compute_node_row=%s\n",
		report.FQDN, report.Role, report.HostVarsPath, report.CommitSHA, report.BootstrapPassed, report.ComputeNodeRowID)
	return 0
}

// addNodeParams is the parameter bundle for the executor — kept
// out of the flag parser so the function signature is stable
// across CLI changes.
type addNodeParams struct {
	FQDN            string
	Role            string
	AnsibleHost     string
	PublicIface     string
	MasqCIDR        string
	MasqCIDRv6      string
	OverlayCIDRs    string
	TargetURL       string
	VPCPUs          int
	MemMB           int
	MaxConcurrency  int
	AdmCeil         int
	SSHTarget       string
	SkipBootstrap   bool
	SkipPOST        bool
	JSONOut         bool
	RepoRoot        string
	HostVarsPath    string
	DesiredHostVars string
}

// addNodeReport is the structured-JSON output shape. Returned by
// the executor; non-JSON path prints a one-line summary.
type addNodeReport struct {
	FQDN              string `json:"fqdn"`
	Role              string `json:"role"`
	HostVarsPath      string `json:"host_vars_path"`
	CommitSHA         string `json:"commit_sha"`
	BootstrapPassed   bool   `json:"bootstrap_passed"`
	ComputeNodeRowID  string `json:"compute_node_row_id,omitempty"`
	BootstrapSkipped  bool   `json:"bootstrap_skipped"`
	POSTSkipped       bool   `json:"post_skipped"`
	BootstrapErrorMsg string `json:"bootstrap_error,omitempty"`
}

// addNodeExecute runs the four-step coordination. The PR-A
// compute-nodes add is invoked via --from-file so the validation
// in cmdComputeNodesAdd stays the source of truth.
func addNodeExecute(p addNodeParams) (addNodeReport, int) {
	report := addNodeReport{
		FQDN:             p.FQDN,
		Role:             p.Role,
		HostVarsPath:     p.HostVarsPath,
		BootstrapSkipped: p.SkipBootstrap,
		POSTSkipped:      p.SkipPOST,
	}

	// Step 1: write host_vars file.
	if err := os.MkdirAll(filepath.Dir(p.HostVarsPath), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: mkdir host_vars: %v\n", err)
		return report, 1
	}
	if err := os.WriteFile(p.HostVarsPath, []byte(p.DesiredHostVars), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: write host_vars: %v\n", err)
		return report, 1
	}

	// Step 2: update hosts.ini (idempotent — adds only if missing).
	hostsPath := filepath.Join(p.RepoRoot, "deploy/ansible/inventory/hosts.ini")
	hostsBody, err := updateHostsINIAddNode(hostsPath, p.FQDN, p.Role)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: update hosts.ini: %v\n", err)
		return report, 1
	}
	if err := os.WriteFile(hostsPath, hostsBody, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: write hosts.ini: %v\n", err)
		return report, 1
	}

	// Idempotency check: if neither host_vars nor hosts.ini
	// changed (re-run with the same fqdn and identical flags),
	// the prior commit already covers the step. Skip git
	// add/commit (which would otherwise fail with "nothing to
	// commit") and skip the SSH + POST steps (the previous run
	// already did them). Surface the existing commit SHA so the
	// operator can confirm the no-op.
	dirty, dirtyErr := gitDirty(p.RepoRoot, p.FQDN)
	if dirtyErr != nil {
		// A real git error (not repo, etc.) is fatal — fall
		// through to the commit step which will surface a clearer
		// message.
		dirty = true
	}
	if !dirty {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: no repo changes (host_vars + hosts.ini already match); treating as no-op\n")
		sha, shaErr := gitHeadSHA(p.RepoRoot)
		if shaErr != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: git rev-parse HEAD on no-op: %v\n", shaErr)
			return report, 1
		}
		report.CommitSHA = sha
		// A re-run is, by definition, a no-op. The previous run
		// already executed the bootstrap + POST; we don't repeat
		// them (the operator would otherwise see a second SSH
		// bootstrap for a node that's already bootstrapped).
		// Surface the no-op result in the report.
		return report, 0
	}

	// Step 3: git add + commit.
	if _, err := gitRunner(p.RepoRoot, "add", "deploy/ansible/host_vars/"+p.FQDN+".yml", "deploy/ansible/inventory/hosts.ini"); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: git add: %v\n", err)
		return report, 1
	}
	commitMsg := fmt.Sprintf("feat(ansible): add %s %s\n\nGenerated by gregalectl deploy add-node (issue #911 / ADR-110).", p.FQDN, p.Role)
	if _, err := gitRunner(p.RepoRoot, "commit", "-m", commitMsg); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: git commit: %v\n", err)
		return report, 1
	}
	// Capture the commit SHA for the report so the operator can
	// `git revert <sha>` if needed. Goes through the seam so a
	// test override can return the expected SHA — the previous
	// direct exec.Command bypass left the report's commit_sha
	// empty in whitebox tests.
	out, err := gitRunner(p.RepoRoot, "rev-parse", "HEAD")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: git rev-parse HEAD: %v\n", err)
		return report, 1
	}
	report.CommitSHA = strings.TrimSpace(string(out))

	// Step 4: SSH bootstrap (unless --skip-bootstrap).
	if p.SkipBootstrap {
		return report, 0
	}
	sshTgt := defaultSSHTarget(p.FQDN, p.SSHTarget)
	// ANSIBLE_LIMIT scopes the bootstrap to the single new node.
	// Without it, `make bootstrap-compute` runs the playbook with
	// `--limit compute_nodes` (a GROUP), which would re-bootstrap
	// every existing compute node in the fleet — cold-evicting
	// running tenant instances on the live production node. The
	// Makefile honours $ANSIBLE_LIMIT (defaulting to the group)
	// so the same shape works for hand-run + scripted paths.
	bootstrapCmd := []string{"sudo", "ANSIBLE_LIMIT=" + p.FQDN, "make", bootstrapTarget(p.Role)}
	_, stderrBytes, sshErr := sshRunnerWithOverride(sshTgt, bootstrapCmd)
	if sshErr != nil {
		report.BootstrapPassed = false
		report.BootstrapErrorMsg = strings.TrimSpace(string(stderrBytes))
		if report.BootstrapErrorMsg == "" {
			report.BootstrapErrorMsg = sshErr.Error()
		}
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: bootstrap failed on %s: %s\n", sshTgt, report.BootstrapErrorMsg)
		fmt.Fprintf(os.Stderr, "  (repo delta is committed at %s; manually SSH+debug or git revert <commit> + retry)\n", report.CommitSHA)
		return report, 1
	}
	report.BootstrapPassed = true

	// Step 5: POST compute_nodes row (unless --skip-compute-nodes-post).
	if p.SkipPOST || p.Role != roleComputeOnly {
		return report, 0
	}
	payload := computeNodePayload{
		Name:               p.FQDN,
		TargetURL:          p.TargetURL,
		VPCPUs:             p.VPCPUs,
		MemMB:              p.MemMB,
		MaxConcurrency:     p.MaxConcurrency,
		AdmissionCeilingMB: p.AdmCeil,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: marshal payload: %v\n", err)
		return report, 1
	}
	scratch, err := os.CreateTemp("", "compute-node-payload-*.json")
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: create scratch: %v\n", err)
		return report, 1
	}
	defer func() { _ = os.Remove(scratch.Name()) }()
	if _, err := scratch.Write(payloadBytes); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: write scratch: %v\n", err)
		return report, 1
	}
	if err := scratch.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: close scratch: %v\n", err)
		return report, 1
	}
	// Invoke the same authenticated enrollment path as the standalone command.
	// Capture its JSON response so the coordinator can report the row ID without
	// opening PostgreSQL for a follow-up read.
	var enrollment bytes.Buffer
	if code := cmdComputeNodesAddTo([]string{
		"--from-file=" + scratch.Name(),
		"--reason=deploy_add_node",
		"--json",
	}, &enrollment); code != 0 {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: compute-nodes add exited %d (repo+bootstrap ok; row not inserted)\n", code)
		return report, 1
	}
	var enrolled computeNodeAddedJSON
	if err := json.Unmarshal(enrollment.Bytes(), &enrolled); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy add-node: decode compute-node response: %v\n", err)
		return report, 1
	}
	if enrolled.ID == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy add-node: compute-node response omitted row id")
		return report, 1
	}
	report.ComputeNodeRowID = enrolled.ID
	return report, 0
}

// defaultRepoRoot walks up from the current working directory
// until it finds a Makefile + deploy/ansible/bootstrap.yml. Used
// when the operator didn't pass --repo-root explicitly.
func defaultRepoRoot() string {
	cwd, err := os.Getwd()
	if err != nil {
		return "."
	}
	dir := cwd
	for i := 0; i < 5; i++ {
		if _, err := os.Stat(filepath.Join(dir, "deploy/ansible/bootstrap.yml")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return cwd
		}
		dir = parent
	}
	return cwd
}

// defaultSSHTarget returns gregale@<fqdn> unless the operator
// passed --ssh
func defaultSSHTarget(fqdn, sshTarget string) string {
	if sshTarget != "" {
		return sshTarget
	}
	return "gregale@" + fqdn
}

// bootstrapTarget returns the make-target name for the role.
// control-plane → bootstrap-control-plane, compute-only →
// bootstrap-compute. Matches the Makefile mappings.
func bootstrapTarget(role string) string {
	switch role {
	case roleControlPlane:
		return "bootstrap-control-plane"
	default:
		return "bootstrap-compute"
	}
}

// ansibleGroup returns the inventory group name for the role.
func ansibleGroup(role string) string {
	switch role {
	case roleControlPlane:
		return "control_plane"
	default:
		return "compute_nodes"
	}
}

// gitHeadSHA returns the HEAD commit's SHA. Goes through the
// gitRunner seam (matches the rest of the deploy flow) so tests
// can swap it for a fake — the previous direct exec.Command
// invocation bypassed the seam and the commit_sha field of the
// report was empty in tests.
func gitHeadSHA(repoRoot string) (string, error) {
	out, err := gitRunner(repoRoot, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// gitDirty returns true if the repo has uncommitted changes to
// the two files deploy add-node touches. Used by the idempotency
// check: if neither host_vars nor hosts.ini changed (re-run with
// the same fqdn and identical flags), the prior commit already
// covers the step and we skip the git add/commit to avoid
// `nothing to commit` exit 1.
//
// Goes through the gitRunner seam so tests can stub the status.
// We parse `git status --porcelain` output: any non-empty line
// for either of the two paths means dirty.
func gitDirty(repoRoot, fqdn string) (bool, error) {
	out, err := gitRunner(repoRoot, "status", "--porcelain", "--",
		"deploy/ansible/host_vars/"+fqdn+".yml",
		"deploy/ansible/inventory/hosts.ini")
	if err != nil {
		// `git status` on a non-repo returns 128; treat as
		// "not dirty" plus the error so the caller can decide.
		return false, err
	}
	return len(strings.TrimSpace(string(out))) > 0, nil
}
