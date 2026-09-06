package main

// Provider-neutral node adoption. A provider creates a machine; this command
// turns that machine into a manifest-declared Gregale compute node without
// editing the repository or teaching the CLI about GCP, Hetzner, OVH, or any
// other infrastructure API.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
	"github.com/onebox-faas/faas/pkg/nodejoin"
	"github.com/onebox-faas/faas/pkg/pki"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"github.com/onebox-faas/faas/pkg/storage"
)

type deployJoinOptions struct {
	ManifestFile          string
	Node                  string
	SSHHost               string
	SSHUser               string
	SSHPort               int
	SSHHostKeySHA256      string
	SSHKey                string
	FleetBundleFile       string
	FleetBundleSignature  string
	FleetReplayState      string
	SSHKnownHostsFile     string
	ReleaseTarball        string
	ReleaseGitSHA         string
	BootstrapBinary       string
	CosignBinary          string
	PKISource             string
	SignKeySource         string
	VerifyKeySource       string
	ComputeDBEnvSource    string
	StorageEnvSource      string
	RuntimeBasesEnvSource string
	StorageDevice         string
	FormatStorage         bool
	BoxAgeKeySource       string
	RcloneEnvelope        string
	ArchiveEnvelope       string
	ArtifactDir           string
	AnsibleVarsFile       string
	RepoRoot              string
	SkipFleetPreflight    bool
	Resume                bool
	Timeout               time.Duration
	LeaseTTL              time.Duration
	DryRun                bool
	Yes                   bool
	JSON                  bool
}

type deployJoinReport struct {
	Node           string       `json:"node"`
	DatabaseNode   string       `json:"database_node"`
	SSHHost        string       `json:"ssh_host"`
	ManifestFile   string       `json:"manifest_file"`
	ReleaseGitSHA  string       `json:"release_git_sha"`
	FleetPreflight bool         `json:"fleet_preflight"`
	Applied        bool         `json:"applied"`
	Steps          []string     `json:"steps"`
	Timings        []joinTiming `json:"timings,omitempty"`
}

type joinTiming struct {
	Phase      string `json:"phase"`
	DurationMS int64  `json:"duration_ms"`
}

var nodeJoinStoreOpener = openNodeJoinStore

var joinControlPlaneVerifier = verifyAndActivateJoinedNode

func openNodeJoinStore() (nodejoin.Store, func(), error) {
	pool, err := openPgPoolFromEnv(context.Background())
	if err != nil {
		return nil, func() {}, fmt.Errorf("gregalectl deploy join-node: %w", err)
	}
	return nodejoin.NewPGStore(pool), pool.Close, nil
}

// ansiblePlaybookRunner is a process seam for CLI tests. The production path
// streams Ansible's output so the operator sees exactly which phase failed.
var ansiblePlaybookRunner = defaultAnsiblePlaybookRunner

// joinReleaseBundleRegistrar is a seam for CLI tests. Production joins
// register the signed bundle metadata before Ansible can install it, so the
// host-side release installer can stamp applied_at without racing a missing
// release_bundles row.
var joinReleaseBundleRegistrar = registerJoinReleaseBundle

func defaultAnsiblePlaybookRunner(ctx context.Context, workingDir string, args []string) error {
	cmd := exec.CommandContext(ctx, "ansible-playbook", args...)
	cmd.Dir = workingDir
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func cmdDeployJoinNode(args []string) int {
	fs := flag.NewFlagSet("deploy join-node", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	manifestFile := fs.String("manifest-file", "", "split-box manifest containing the new compute-only node (required)")
	node := fs.String("node", "", "manifest host name to adopt (required)")
	sshHost := fs.String("ssh-host", "", "SSH address of the already-created machine (required; provider boundary)")
	sshUser := fs.String("ssh-user", "", "SSH user for the adopted machine (default: root without --fleet-bundle-file)")
	sshPort := fs.Int("ssh-port", 0, "SSH port for the adopted machine (default: 22 without --fleet-bundle-file)")
	sshHostKey := fs.String("ssh-host-key-sha256", "", "expected OpenSSH SHA256 host-key fingerprint")
	sshKey := fs.String("ssh-key", "", "optional SSH private key used by Ansible")
	fleetBundleFile := fs.String("fleet-bundle-file", "", "signed FleetEnrollmentBundle YAML/JSON authorization")
	fleetBundleSignature := fs.String("fleet-bundle-signature", "", "detached cosign signature for --fleet-bundle-file")
	fleetReplayState := fs.String("fleet-replay-state", "", "durable single-use enrollment state directory (required with --fleet-bundle-file for apply)")
	releaseTarball := fs.String("release-tarball", "", "signed release.tar.gz (required for apply)")
	releaseGitSHA := fs.String("release-git-sha", "", "optional signed release SHA override when the manifest still points at the prior release")
	bootstrapBinary := fs.String("bootstrap-binary", "", "Linux/amd64 gregalectl used before the release is installed (required for apply)")
	cosignBinary := fs.String("cosign-binary", "", "cosign verifier binary staged on the adopted host (required for apply)")
	pkiSource := fs.String("pki-dir", "", "compute trust-bundle directory containing ca/ca.crt and the compute-only leaves (required for apply)")
	signKey := fs.String("sign-key", "", "image-signing private key (required for apply)")
	verifyKey := fs.String("verify-key", "", "image-signing public key (required for apply)")
	computeDBEnv := fs.String("compute-db-env", "", "root-only compute-db.env source (required for apply)")
	storageEnv := fs.String("storage-env", "", "shared OCI storage.env source (required for multi-box apply)")
	runtimeBasesEnv := fs.String("runtime-bases-env", "", "release-bound digest-pinned runtime base refs (required for apply)")
	storageDevice := fs.String("storage-device", "", "optional fast-root block device (must be an absolute path; manifest host value is used when omitted)")
	formatStorage := fs.Bool("format-storage", false, "format an explicitly supplied blank storage device as XFS with reflink support")
	boxAgeKey := fs.String("box-age-key", "", "optional box-age identity source (artifact-dir convention: box-age-key)")
	rcloneEnvelope := fs.String("rclone-envelope", "", "optional encrypted rclone.conf envelope (artifact-dir convention: rclone.conf.age)")
	archiveEnvelope := fs.String("archive-creds-envelope", "", "optional encrypted archive credentials envelope (artifact-dir convention: archive-creds.json.age)")
	artifactDir := fs.String("artifact-dir", "", "directory containing the standard release, key, trust-bundle, and bootstrap assets")
	ansibleVars := fs.String("ansible-vars-file", "", "optional provider/overlay Ansible vars file")
	repoRoot := fs.String("repo-root", "", "path to the faas repository (default: inferred from gregalectl)")
	skipPreflight := fs.Bool("skip-fleet-preflight", false, "skip the complete-fleet preflight (only for a previously validated fleet)")
	resume := fs.Bool("resume", false, "resume a failed or interrupted join job for this exact desired state")
	timeout := fs.Duration("timeout", 20*time.Minute, "maximum time allowed for the remote adoption")
	leaseTTL := fs.Duration("lease-ttl", 30*time.Minute, "database lease held by this join worker")
	dryRun := fs.Bool("dry-run", false, "validate and print the adoption plan without contacting the host")
	yes := fs.Bool("yes", false, "approve the remote adoption")
	jsonOut := fs.Bool("json", false, "emit structured JSON to stdout")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-node: unexpected positional argument")
		return 2
	}

	opts := deployJoinOptions{
		ManifestFile:          *manifestFile,
		Node:                  *node,
		SSHHost:               *sshHost,
		SSHUser:               *sshUser,
		SSHPort:               *sshPort,
		SSHHostKeySHA256:      *sshHostKey,
		SSHKey:                *sshKey,
		FleetBundleFile:       *fleetBundleFile,
		FleetBundleSignature:  *fleetBundleSignature,
		FleetReplayState:      *fleetReplayState,
		ReleaseTarball:        *releaseTarball,
		ReleaseGitSHA:         *releaseGitSHA,
		BootstrapBinary:       *bootstrapBinary,
		CosignBinary:          *cosignBinary,
		PKISource:             *pkiSource,
		SignKeySource:         *signKey,
		VerifyKeySource:       *verifyKey,
		ComputeDBEnvSource:    *computeDBEnv,
		StorageEnvSource:      *storageEnv,
		RuntimeBasesEnvSource: *runtimeBasesEnv,
		StorageDevice:         *storageDevice,
		FormatStorage:         *formatStorage,
		BoxAgeKeySource:       *boxAgeKey,
		RcloneEnvelope:        *rcloneEnvelope,
		ArchiveEnvelope:       *archiveEnvelope,
		ArtifactDir:           *artifactDir,
		AnsibleVarsFile:       *ansibleVars,
		RepoRoot:              *repoRoot,
		SkipFleetPreflight:    *skipPreflight,
		Resume:                *resume,
		Timeout:               *timeout,
		LeaseTTL:              *leaseTTL,
		DryRun:                *dryRun,
		Yes:                   *yes,
		JSON:                  *jsonOut || jsonOutput,
	}
	if opts.FleetBundleFile == "" {
		if opts.SSHUser == "" {
			opts.SSHUser = "root"
		}
		if opts.SSHPort == 0 {
			opts.SSHPort = 22
		}
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = defaultRepoRoot()
	}
	resolveJoinArtifacts(&opts)
	if err := resolveFleetBundleInputs(&opts); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy join-node: %v\n", err)
		return 1
	}
	if !opts.DryRun && opts.Yes {
		if code, handled := maybeBootstrapReleaseCLIFromTarball(opts.ReleaseTarball, opts.ReleaseGitSHA); handled {
			return code
		}
	}

	report, err := deployJoinValidate(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy join-node: %v\n", err)
		return 1
	}
	if opts.DryRun {
		return emitDeployJoinReport(report, false, opts.JSON)
	}
	if !opts.Yes {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-node: this will bootstrap and start services on the remote host")
		fmt.Fprintln(os.Stderr, "Re-run with --yes to proceed.")
		return 2
	}

	code, err := executeDeployJoin(opts, &report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy join-node: %v\n", err)
		return code
	}
	if err := markFleetBundleConsumed(opts); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy join-node: %v\n", err)
		return 3
	}
	return emitDeployJoinReport(report, true, opts.JSON)
}

// cmdDeployRollbackNode is the operator-safe rollback boundary for a join.
// It drains the control-plane row and records rolled_back, but deliberately
// leaves remote files and services untouched for forensics. A later
// `join-node --resume` can converge the same desired state again.
func cmdDeployRollbackNode(args []string) int {
	fs := flag.NewFlagSet("deploy rollback-node", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	node := fs.String("node", "", "manifest node whose join job should be rolled back (required)")
	leaseTTL := fs.Duration("lease-ttl", 5*time.Minute, "rollback coordination lease")
	yes := fs.Bool("yes", false, "confirm draining the node")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 || *node == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy rollback-node: --node is required")
		return 2
	}
	if *leaseTTL <= 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy rollback-node: --lease-ttl must be positive")
		return 2
	}
	if !*yes {
		fmt.Fprintln(os.Stderr, "gregalectl deploy rollback-node: re-run with --yes to drain and mark the join rolled back")
		return 2
	}
	jobs, closeJobs, err := nodeJoinStoreOpener()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer closeJobs()
	job, err := jobs.Get(context.Background(), *node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: %v\n", err)
		return 1
	}
	owner := fmt.Sprintf("gregalectl-rollback-%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err := jobs.AcquireLease(context.Background(), *node, owner, *leaseTTL); err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: acquire lease: %v\n", err)
		return 1
	}
	store, closeStore, err := computeNodesStoreOpener()
	if err != nil {
		_ = jobs.ReleaseLease(context.Background(), *node, owner)
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: open control-plane state: %v\n", err)
		return 1
	}
	defer closeStore()
	row, err := store.ComputeNodeByName(context.Background(), job.DatabaseNode)
	if err != nil {
		_ = jobs.ReleaseLease(context.Background(), *node, owner)
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: lookup row: %v\n", err)
		return 1
	}
	if err := store.SetComputeNodeActive(context.Background(), row.ID, false); err != nil {
		_ = jobs.ReleaseLease(context.Background(), *node, owner)
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: drain row: %v\n", err)
		return 3
	}
	if err := jobs.MarkRolledBack(context.Background(), *node, owner, errors.New("operator requested rollback")); err != nil {
		_ = jobs.ReleaseLease(context.Background(), *node, owner)
		fmt.Fprintf(os.Stderr, "gregalectl deploy rollback-node: record rollback: %v\n", err)
		return 3
	}
	if *jsonOut || jsonOutput {
		jsonEmit(os.Stdout, map[string]any{"node": *node, "database_node": job.DatabaseNode, "phase": nodejoin.PhaseRolledBack, "active": false})
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "deploy rollback-node: node=%s database_node=%s phase=%s active=false\n", *node, job.DatabaseNode, nodejoin.PhaseRolledBack)
	return 0
}

func executeDeployJoin(opts deployJoinOptions, report *deployJoinReport) (int, error) {
	if opts.Timeout <= 0 {
		return 2, errors.New("--timeout must be positive")
	}
	if opts.LeaseTTL <= 0 {
		return 2, errors.New("--lease-ttl must be positive")
	}
	manifestHash, err := joinManifestHash(opts.ManifestFile)
	if err != nil {
		return 1, err
	}
	jobStore, closeJobStore, err := nodeJoinStoreOpener()
	if err != nil {
		return 1, err
	}
	defer closeJobStore()
	spec := nodejoin.Spec{NodeName: opts.Node, DatabaseNode: report.DatabaseNode, SSHHost: opts.SSHHost, ManifestHash: manifestHash, ReleaseGitSHA: report.ReleaseGitSHA}
	if _, err := jobStore.CreateOrResume(context.Background(), spec, opts.Resume); err != nil {
		return 1, fmt.Errorf("prepare durable join job: %w", err)
	}
	owner := fmt.Sprintf("gregalectl-%d-%d", os.Getpid(), time.Now().UnixNano())
	if _, err := jobStore.AcquireLease(context.Background(), opts.Node, owner, opts.LeaseTTL); err != nil {
		return 1, fmt.Errorf("acquire durable join lease: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), opts.Timeout)
	defer cancel()
	refreshDone := make(chan struct{})
	defer close(refreshDone)
	go refreshNodeJoinLease(ctx, jobStore, opts.Node, owner, opts.LeaseTTL, refreshDone)
	progress := func(phase nodejoin.Phase) error {
		return jobStore.UpdatePhase(ctx, opts.Node, owner, phase, "")
	}
	code, err := deployJoinApplyWithContext(ctx, &opts, report, progress)
	if err != nil {
		_ = jobStore.MarkFailed(context.Background(), opts.Node, owner, err)
		return code, err
	}
	if err := jobStore.MarkComplete(context.Background(), opts.Node, owner); err != nil {
		return 3, fmt.Errorf("mark active: %w", err)
	}
	return 0, nil
}

func deployJoinValidate(opts deployJoinOptions) (deployJoinReport, error) {
	report := deployJoinReport{
		Node:           opts.Node,
		DatabaseNode:   canonicalComputeNodeName(opts.Node, roleComputeOnly),
		SSHHost:        opts.SSHHost,
		ManifestFile:   opts.ManifestFile,
		FleetPreflight: !opts.SkipFleetPreflight,
		Steps: []string{
			"validate manifest and locate the compute-only host",
			"generate an ephemeral Ansible inventory without changing git",
			"adopt the provider SSH target for this host only",
			"run complete-fleet preflight unless explicitly skipped",
			"converge control-plane peer access from the complete manifest",
			"stage trust material, signed release assets, and manifest",
			"converge or verify the dedicated XFS fast-root filesystem",
			"converge the production compute-only Ansible role",
			"install the signed release while the database row remains drained",
			"render configuration, initialize host identity, and unseal supplied backup envelopes",
			"wait for sockets, gateway, and systemd readiness",
			"verify every active compute daemon executes the installed release",
			"run the node-scoped doctor and verify the control-plane row before activation",
		},
	}
	if opts.ManifestFile == "" {
		return report, errors.New("--manifest-file is required")
	}
	if opts.Node == "" {
		return report, errors.New("--node is required")
	}
	if opts.SSHHost == "" {
		return report, errors.New("--ssh-host is required")
	}
	if opts.SSHUser == "" {
		// Keep direct callers and programmatic tests aligned with the
		// flag parser's production default.
		opts.SSHUser = "root"
	}
	if opts.SSHPort == 0 {
		opts.SSHPort = 22
	}
	if opts.SSHPort < 1 || opts.SSHPort > 65535 {
		return report, fmt.Errorf("--ssh-port %d is outside 1..65535", opts.SSHPort)
	}
	if opts.SSHHostKeySHA256 != "" {
		if err := nodeclaim.ValidateHostKeyFingerprint(opts.SSHHostKeySHA256); err != nil {
			return report, fmt.Errorf("--ssh-host-key-sha256: %w", err)
		}
	}
	if opts.StorageDevice != "" && !filepath.IsAbs(opts.StorageDevice) {
		return report, fmt.Errorf("--storage-device %q must be an absolute device path", opts.StorageDevice)
	}
	m, err := manifest.Load(opts.ManifestFile)
	if err != nil {
		return report, err
	}
	if errs := m.Validate(); errs != nil {
		return report, fmt.Errorf("invalid manifest: %w", errs)
	}
	var hostFound bool
	for _, host := range m.Fleet.Hosts {
		if host.Name != opts.Node {
			continue
		}
		hostFound = true
		if host.Role != roleComputeOnly {
			return report, fmt.Errorf("manifest host %q has role %q; join-node requires compute-only", opts.Node, host.Role)
		}
		if opts.StorageDevice == "" {
			opts.StorageDevice = host.StorageDevice
		}
	}
	if !hostFound {
		return report, fmt.Errorf("manifest does not declare host %q", opts.Node)
	}
	if opts.StorageDevice != "" && !filepath.IsAbs(opts.StorageDevice) {
		return report, fmt.Errorf("storage device %q must be an absolute device path", opts.StorageDevice)
	}
	if opts.FormatStorage && opts.StorageDevice == "" {
		return report, errors.New("--format-storage requires --storage-device or manifest fleet.hosts[].storage_device")
	}
	if m.Release.GitSHA == "" {
		return report, errors.New("manifest release.git_sha is empty")
	}
	report.ReleaseGitSHA = strings.TrimSpace(opts.ReleaseGitSHA)
	if report.ReleaseGitSHA == "" {
		report.ReleaseGitSHA = m.Release.GitSHA
	} else if !releaseinstall.ValidGitSHA(report.ReleaseGitSHA) {
		return report, fmt.Errorf("--release-git-sha %q is not a 40-character lowercase SHA", report.ReleaseGitSHA)
	}
	if opts.RepoRoot == "" {
		opts.RepoRoot = defaultRepoRoot()
	}
	if _, err := os.Stat(filepath.Join(opts.RepoRoot, "deploy/ansible/node_join.yml")); err != nil {
		return report, fmt.Errorf("repo root %q is missing deploy/ansible/node_join.yml: %w", opts.RepoRoot, err)
	}
	if opts.DryRun {
		return report, nil
	}
	for name, path := range map[string]string{
		"release-tarball":   opts.ReleaseTarball,
		"bootstrap-binary":  opts.BootstrapBinary,
		"cosign-binary":     opts.CosignBinary,
		"pki-dir":           opts.PKISource,
		"sign-key":          opts.SignKeySource,
		"verify-key":        opts.VerifyKeySource,
		"compute-db-env":    opts.ComputeDBEnvSource,
		"storage-env":       opts.StorageEnvSource,
		"runtime-bases-env": opts.RuntimeBasesEnvSource,
	} {
		if path == "" {
			return report, fmt.Errorf("--%s is required for apply", name)
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return report, fmt.Errorf("--%s: %w", name, statErr)
		}
		if name != "pki-dir" && !info.Mode().IsRegular() {
			return report, fmt.Errorf("--%s must be a regular file", name)
		}
	}
	if err := validateSharedStorageEnv(opts.StorageEnvSource); err != nil {
		return report, fmt.Errorf("--storage-env: %w", err)
	}
	for name, path := range map[string]string{
		"box-age-key":            opts.BoxAgeKeySource,
		"rclone-envelope":        opts.RcloneEnvelope,
		"archive-creds-envelope": opts.ArchiveEnvelope,
	} {
		if path == "" {
			continue
		}
		info, statErr := os.Stat(path)
		if statErr != nil {
			return report, fmt.Errorf("--%s: %w", name, statErr)
		}
		if !info.Mode().IsRegular() {
			return report, fmt.Errorf("--%s must be a regular file", name)
		}
	}
	if (opts.RcloneEnvelope != "" || opts.ArchiveEnvelope != "") && opts.BoxAgeKeySource == "" {
		return report, errors.New("--rclone-envelope/--archive-creds-envelope require --box-age-key")
	}
	if (opts.RcloneEnvelope == "") != (opts.ArchiveEnvelope == "") {
		return report, errors.New("--rclone-envelope and --archive-creds-envelope must be supplied together")
	}
	if info, err := os.Stat(opts.BootstrapBinary); err == nil && info.Mode()&0o111 == 0 {
		return report, fmt.Errorf("--bootstrap-binary is not executable: %s", opts.BootstrapBinary)
	}
	if info, err := os.Stat(opts.CosignBinary); err == nil && info.Mode()&0o111 == 0 {
		return report, fmt.Errorf("--cosign-binary is not executable: %s", opts.CosignBinary)
	}
	// A PKI source may be either a ready trust bundle or the operator-side
	// issuance root. Do not reject missing leaves here: the latter is allowed
	// to issue/refresh the compute-only leaves in copyTrustBundle below. The
	// trust-bundle and issuance-material validators are the authoritative
	// checks for those two supported input shapes.
	caCert, _ := pki.CARoot(opts.PKISource)
	if _, err := os.Stat(caCert); err != nil {
		return report, fmt.Errorf("--pki-dir missing %s: %w", caCert, err)
	}
	manifestSANs, err := joinHostSANs(m, opts.Node)
	if err != nil {
		return report, err
	}
	if err := pki.ValidateTrustBundleForNode(opts.PKISource, roleComputeOnly, manifestSANs, report.DatabaseNode); err != nil {
		if issuanceErr := pki.ValidateIssuanceMaterial(opts.PKISource, roleComputeOnly); issuanceErr != nil {
			return report, fmt.Errorf("--pki-dir is neither a valid compute trust bundle nor issuable operator PKI: trust validation failed; issuance=%w", issuanceErr)
		}
	}
	if !hasComputeDatabaseEnv(opts.ComputeDBEnvSource) {
		return report, errors.New("--compute-db-env must contain non-empty DATABASE_URL and FAAS_VMMD_DBURL entries")
	}
	if err := validateRuntimeBasesEnv(opts.RuntimeBasesEnvSource, m.Release.RuntimeBaseRefs); err != nil {
		return report, fmt.Errorf("--runtime-bases-env: %w", err)
	}
	if opts.AnsibleVarsFile != "" {
		if _, err := os.Stat(opts.AnsibleVarsFile); err != nil {
			return report, fmt.Errorf("--ansible-vars-file: %w", err)
		}
	}
	if _, err := releaseAssetPath(opts.ReleaseTarball, releaseSigName); err != nil {
		return report, err
	}
	if _, err := releaseAssetPath(opts.ReleaseTarball, releaseSBOMName); err != nil {
		return report, err
	}
	return report, nil
}

func deployJoinApply(opts *deployJoinOptions, report *deployJoinReport) (int, error) {
	return deployJoinApplyWithContext(context.Background(), opts, report, nil)
}

func deployJoinApplyWithContext(ctx context.Context, opts *deployJoinOptions, report *deployJoinReport, progress func(nodejoin.Phase) error) (int, error) {
	joinStarted := time.Now()
	defer func() {
		report.Timings = append(report.Timings, joinTiming{Phase: "total", DurationMS: time.Since(joinStarted).Milliseconds()})
	}()
	if opts.RepoRoot == "" {
		opts.RepoRoot = defaultRepoRoot()
	}
	ansibleDir := filepath.Join(opts.RepoRoot, "deploy/ansible")
	tempRoot, err := os.MkdirTemp("", "gregale-node-join-")
	if err != nil {
		return 3, fmt.Errorf("create temporary inventory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()
	if opts.SSHHostKeySHA256 != "" {
		knownHostsPath := filepath.Join(tempRoot, "known_hosts")
		if err := verifySSHHostKey(ctx, *opts, knownHostsPath); err != nil {
			return 3, fmt.Errorf("verify SSH host key: %w", err)
		}
		opts.SSHKnownHostsFile = knownHostsPath
	}

	m, err := manifest.Load(opts.ManifestFile)
	if err != nil {
		return 1, err
	}
	expectedManifestHash, err := joinManifestHash(opts.ManifestFile)
	if err != nil {
		return 3, err
	}
	if err := joinReleaseBundleRegistrar(ctx, opts.ReleaseTarball, report.ReleaseGitSHA, expectedManifestHash); err != nil {
		return 3, fmt.Errorf("register release bundle: %w", err)
	}
	builderBaseRef := ""
	if digest := strings.TrimSpace(m.Release.BuilderBaseDigest); digest != "" {
		// The release manifest stores the immutable image digest; the
		// public repository name is the platform's documented builder-base
		// contract. Operators can override this through ansible-vars-file
		// with faas_builder_base_ref when using a mirror.
		builderBaseRef = "ghcr.io/poyrazk/builder-base@sha256:" + digest
	}
	manifestSANs, err := joinHostSANs(m, opts.Node)
	if err != nil {
		return 1, err
	}
	trustRoot := filepath.Join(tempRoot, "pki-trust")
	if err := copyTrustBundle(opts.PKISource, trustRoot, roleComputeOnly, manifestSANs, report.DatabaseNode); err != nil {
		return 3, fmt.Errorf("prepare compute trust bundle: %w", err)
	}
	files, err := renderManifestAnsibleFiles(m, tempRoot)
	if err != nil {
		return 1, fmt.Errorf("render temporary inventory: %w", err)
	}
	for i := range files {
		if filepath.Base(files[i].Path) == opts.Node+".yml" {
			files[i].Body = overrideJoinHostVars(files[i].Body, opts)
		}
		if err := writeGeneratedAnsibleFile(files[i].Path, files[i].Body, true); err != nil {
			return 3, fmt.Errorf("write temporary inventory: %w", err)
		}
	}

	nodeKeyDir := filepath.Join(tempRoot, "node-key")
	if err := os.MkdirAll(nodeKeyDir, 0o700); err != nil {
		return 3, fmt.Errorf("create temporary node key directory: %w", err)
	}
	priv, pub, err := generateNodeKeyPEM()
	if err != nil {
		return 3, err
	}
	nodeKeySource := filepath.Join(nodeKeyDir, "node.key")
	nodePubSource := filepath.Join(nodeKeyDir, "node.pub")
	if err := os.WriteFile(nodeKeySource, priv, 0o400); err != nil {
		return 3, fmt.Errorf("write temporary node key: %w", err)
	}
	if err := os.WriteFile(nodePubSource, pub, 0o444); err != nil {
		return 3, fmt.Errorf("write temporary node public key: %w", err)
	}

	signature, err := releaseAssetPath(opts.ReleaseTarball, releaseSigName)
	if err != nil {
		return 3, err
	}
	sbom, err := releaseAssetPath(opts.ReleaseTarball, releaseSBOMName)
	if err != nil {
		return 3, err
	}
	vars := map[string]any{
		"faas_join_inventory_name":           opts.Node,
		"faas_join_database_node":            report.DatabaseNode,
		"faas_join_release_git_sha":          report.ReleaseGitSHA,
		"faas_join_manifest_source":          opts.ManifestFile,
		"faas_join_bootstrap_binary_source":  opts.BootstrapBinary,
		"faas_join_cosign_binary_source":     opts.CosignBinary,
		"faas_join_pki_source":               trustRoot,
		"faas_join_sign_key_source":          opts.SignKeySource,
		"faas_join_verify_key_source":        opts.VerifyKeySource,
		"faas_join_compute_db_env_source":    opts.ComputeDBEnvSource,
		"faas_join_storage_env_source":       opts.StorageEnvSource,
		"faas_join_runtime_bases_env_source": opts.RuntimeBasesEnvSource,
		"faas_join_storage_device":           opts.StorageDevice,
		"faas_join_format_storage":           opts.FormatStorage,
		"faas_join_box_age_key_source":       opts.BoxAgeKeySource,
		"faas_join_rclone_envelope_source":   opts.RcloneEnvelope,
		"faas_join_archive_envelope_source":  opts.ArchiveEnvelope,
		"faas_join_node_key_source":          nodeKeySource,
		"faas_join_node_pub_source":          nodePubSource,
		"faas_join_release_tarball_source":   opts.ReleaseTarball,
		"faas_join_release_signature_source": signature,
		"faas_join_release_sbom_source":      sbom,
		"faas_join_builder_base_ref":         builderBaseRef,
		// A clean provider-created host does not have the release binary or
		// rendered daemon configuration yet. Defer only the service restart
		// handlers until node_join.yml has installed and rendered both.
		"faas_join_defer_service_handlers": true,
	}
	varsPath := filepath.Join(tempRoot, "join-vars.json")
	body, err := json.Marshal(vars)
	if err != nil {
		return 3, fmt.Errorf("encode Ansible variables: %w", err)
	}
	if err := os.WriteFile(varsPath, body, 0o600); err != nil {
		return 3, fmt.Errorf("write Ansible variables: %w", err)
	}
	localPrepareDuration := time.Since(joinStarted)
	report.Timings = append(report.Timings, joinTiming{Phase: "prepare_local", DurationMS: localPrepareDuration.Milliseconds()})

	common := []string{"-i", filepath.Join(tempRoot, "inventory", "hosts.ini")}
	if opts.AnsibleVarsFile != "" {
		common = append(common, "-e", "@"+opts.AnsibleVarsFile)
	}
	common = append(common, "-e", "@"+varsPath)
	if !opts.SkipFleetPreflight {
		if progress != nil {
			if err := progress(nodejoin.PhasePreflight); err != nil {
				return 3, fmt.Errorf("record preflight phase: %w", err)
			}
		}
		preflightArgs := append(append([]string{}, common...), filepath.Join(ansibleDir, "preflight.yml"))
		phaseStarted := time.Now()
		preflightErr := ansiblePlaybookRunner(ctx, ansibleDir, preflightArgs)
		report.Timings = append(report.Timings, joinTiming{Phase: "preflight", DurationMS: time.Since(phaseStarted).Milliseconds()})
		if preflightErr != nil {
			return 3, fmt.Errorf("fleet preflight: %w", preflightErr)
		}
	}
	if progress != nil {
		if err := progress(nodejoin.PhaseConverging); err != nil {
			return 3, fmt.Errorf("record converging phase: %w", err)
		}
	}
	// A node join changes the control-plane's peer allowlists as well as the
	// adopted host. Keep this as a separate, narrowly limited play so the
	// existing compute fleet is never rebooted or reconfigured by --limit.
	controlPlaneArgs := append(append([]string{}, common...), "--limit", "control_plane", filepath.Join(ansibleDir, "node_join_control_plane.yml"))
	phaseStarted := time.Now()
	controlPlaneErr := ansiblePlaybookRunner(ctx, ansibleDir, controlPlaneArgs)
	report.Timings = append(report.Timings, joinTiming{Phase: "control_plane_convergence", DurationMS: time.Since(phaseStarted).Milliseconds()})
	if controlPlaneErr != nil {
		return 3, fmt.Errorf("control-plane topology convergence: %w", controlPlaneErr)
	}
	joinArgs := append(append([]string{}, common...), "--limit", opts.Node, filepath.Join(ansibleDir, "node_join.yml"))
	phaseStarted = time.Now()
	joinErr := ansiblePlaybookRunner(ctx, ansibleDir, joinArgs)
	report.Timings = append(report.Timings, joinTiming{Phase: "node_convergence", DurationMS: time.Since(phaseStarted).Milliseconds()})
	if joinErr != nil {
		return 3, fmt.Errorf("node adoption: %w", joinErr)
	}
	if progress != nil {
		if err := progress(nodejoin.PhaseVerifying); err != nil {
			return 3, fmt.Errorf("record verifying phase: %w", err)
		}
	}
	phaseStarted = time.Now()
	verifyErr := joinControlPlaneVerifier(ctx, report, expectedManifestHash)
	report.Timings = append(report.Timings, joinTiming{Phase: "verify_activate", DurationMS: time.Since(phaseStarted).Milliseconds()})
	if verifyErr != nil {
		return 3, fmt.Errorf("control-plane readiness gate: %w", verifyErr)
	}
	report.Applied = true
	return 0, nil
}

func registerJoinReleaseBundle(ctx context.Context, tarballPath, expectedGitSHA, expectedManifestHash string) error {
	packed, err := os.ReadFile(tarballPath)
	if err != nil {
		return fmt.Errorf("read tarball: %w", err)
	}
	manifestBytes, err := extractTarballMember(packed, releaseinstall.ManifestName)
	if err != nil {
		return fmt.Errorf("read embedded release manifest: %w", err)
	}
	var releaseManifest releaseinstall.Manifest
	if err := json.Unmarshal(manifestBytes, &releaseManifest); err != nil {
		return fmt.Errorf("decode embedded release manifest: %w", err)
	}
	if err := releaseinstall.ValidateManifest(releaseManifest); err != nil {
		return fmt.Errorf("validate embedded release manifest: %w", err)
	}
	if releaseManifest.GitSHA != expectedGitSHA {
		return fmt.Errorf("embedded release git_sha=%s does not match requested %s", releaseManifest.GitSHA, expectedGitSHA)
	}
	if releaseManifest.ManifestHash != expectedManifestHash {
		return fmt.Errorf("embedded release manifest_hash=%s does not match topology manifest %s", releaseManifest.ManifestHash, expectedManifestHash)
	}

	pool, err := openPgPoolFromEnv(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	store := releaseinstall.NewStore(pool)
	existing, err := store.GetByGitSHA(ctx, expectedGitSHA)
	if err == nil {
		if existing.ManifestHash != releaseManifest.ManifestHash || !sameReleaseDaemonHashes(existing.DaemonHashes, releaseManifest.DaemonHashes) {
			return fmt.Errorf("existing release_bundles row for %s does not match the signed release manifest", expectedGitSHA)
		}
		return nil
	}
	if !errors.Is(err, releaseinstall.ErrNotFound) {
		return fmt.Errorf("look up release_bundles: %w", err)
	}
	if _, err := store.Insert(ctx, releaseinstall.FromManifest(releaseManifest)); err != nil {
		return fmt.Errorf("insert release_bundles: %w", err)
	}
	return nil
}

func sameReleaseDaemonHashes(left, right map[string]string) bool {
	if len(left) != len(right) {
		return false
	}
	for name, hash := range right {
		if left[name] != hash {
			return false
		}
	}
	return true
}

func refreshNodeJoinLease(ctx context.Context, store nodejoin.Store, nodeName, owner string, ttl time.Duration, done <-chan struct{}) {
	interval := ttl / 3
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = store.RefreshLease(ctx, nodeName, owner, ttl)
		}
	}
}

func verifyAndActivateJoinedNode(ctx context.Context, report *deployJoinReport, expectedManifestHash string) error {
	store, closeFn, err := computeNodesStoreOpener()
	if err != nil {
		return err
	}
	defer closeFn()
	row, err := store.ComputeNodeByName(ctx, report.DatabaseNode)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", report.DatabaseNode, err)
	}
	if row.Role == nil || *row.Role != roleComputeOnly {
		return fmt.Errorf("row role is %q, want %q", pointerString(row.Role), roleComputeOnly)
	}
	if row.ReleaseID == nil || *row.ReleaseID != report.ReleaseGitSHA {
		return fmt.Errorf("row release_id is %q, want %q", pointerString(row.ReleaseID), report.ReleaseGitSHA)
	}
	if row.ManifestHash == nil || *row.ManifestHash != expectedManifestHash {
		return fmt.Errorf("row manifest_hash is %q, want %q", pointerString(row.ManifestHash), expectedManifestHash)
	}
	if row.HostCertificate == nil || strings.TrimSpace(*row.HostCertificate) == "" {
		return fmt.Errorf("row host_certificate is empty; the node identity was not stamped")
	}
	if row.CertFingerprint == nil || strings.TrimSpace(*row.CertFingerprint) == "" {
		return fmt.Errorf("row cert_fingerprint is empty; the node identity was not stamped")
	}
	if err := validateComputeTargetURL(row.TargetURL); err != nil {
		return err
	}
	if !row.Active {
		if err := store.SetComputeNodeActive(ctx, row.ID, true); err != nil {
			return fmt.Errorf("activate row %s: %w", row.ID, err)
		}
	}
	return nil
}

func validateComputeTargetURL(raw string) error {
	if !strings.HasPrefix(raw, "tcp://") {
		return fmt.Errorf("compute target_url %q is not a tcp endpoint", raw)
	}
	hostPort := strings.TrimPrefix(raw, "tcp://")
	host, _, err := net.SplitHostPort(hostPort)
	if err != nil || host == "" {
		return fmt.Errorf("compute target_url %q is not a valid host:port", raw)
	}
	if host == "0.0.0.0" || host == "::" || host == "127.0.0.1" || host == "localhost" {
		return fmt.Errorf("compute target_url %q is not a stable routable endpoint", raw)
	}
	return nil
}

// verifySSHHostKey resolves the provider connection address once and pins the
// observed key to the fingerprint authorized by the signed enrollment
// bundle. The resulting known_hosts file is passed to Ansible with strict
// checking enabled; no operator workstation state is implicitly trusted.
func verifySSHHostKey(ctx context.Context, opts deployJoinOptions, knownHostsPath string) error {
	if opts.SSHHostKeySHA256 == "" {
		return nil
	}
	keyscan := exec.CommandContext(ctx, "ssh-keyscan", "-T", "10", "-p", strconv.Itoa(opts.SSHPort), opts.SSHHost)
	keys, err := keyscan.Output()
	if err != nil {
		return fmt.Errorf("ssh-keyscan %s:%d: %w", opts.SSHHost, opts.SSHPort, err)
	}
	if len(keys) == 0 {
		return fmt.Errorf("ssh-keyscan %s:%d returned no host keys", opts.SSHHost, opts.SSHPort)
	}
	if err := os.WriteFile(knownHostsPath, keys, 0o600); err != nil {
		return fmt.Errorf("write temporary known_hosts: %w", err)
	}
	fingerprint := exec.CommandContext(ctx, "ssh-keygen", "-lf", knownHostsPath, "-E", "sha256")
	fingerprints, err := fingerprint.Output()
	if err != nil {
		return fmt.Errorf("ssh-keygen fingerprint: %w", err)
	}
	if fingerprintMatches(string(fingerprints), opts.SSHHostKeySHA256) {
		return nil
	}
	return fmt.Errorf("observed host key fingerprint does not match signed %s", opts.SSHHostKeySHA256)
}

func fingerprintMatches(output, expected string) bool {
	for _, line := range strings.Split(output, "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 && fields[1] == expected {
			return true
		}
	}
	return false
}

func pointerString(v *string) string {
	if v == nil {
		return "<nil>"
	}
	return *v
}

func resolveJoinArtifacts(opts *deployJoinOptions) {
	if opts.ArtifactDir == "" {
		return
	}
	resolve := func(current *string, name string) {
		if *current == "" {
			*current = filepath.Join(opts.ArtifactDir, name)
		}
	}
	resolveIfPresent := func(current *string, name string) {
		if *current != "" {
			return
		}
		candidate := filepath.Join(opts.ArtifactDir, name)
		if _, err := os.Stat(candidate); err == nil {
			*current = candidate
		}
	}
	resolve(&opts.ReleaseTarball, releaseTarballName)
	resolve(&opts.BootstrapBinary, "gregalectl-linux-amd64")
	resolve(&opts.CosignBinary, "cosign-linux-amd64")
	resolve(&opts.PKISource, "pki")
	resolve(&opts.SignKeySource, "sign.key")
	resolve(&opts.VerifyKeySource, "sign-pub.pem")
	resolve(&opts.ComputeDBEnvSource, "compute-db.env")
	resolve(&opts.StorageEnvSource, "storage.env")
	resolve(&opts.RuntimeBasesEnvSource, "runtime-bases.env")
	resolveIfPresent(&opts.BoxAgeKeySource, "box-age-key")
	resolveIfPresent(&opts.RcloneEnvelope, "rclone.conf.age")
	resolveIfPresent(&opts.ArchiveEnvelope, "archive-creds.json.age")
}

func joinManifestHash(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read manifest for durable join state: %w", err)
	}
	sum := sha256.Sum256(body)
	return "sha256:" + hex.EncodeToString(sum[:]), nil
}

func joinHostSANs(m *manifest.Manifest, node string) (pki.AltNames, error) {
	for _, host := range m.Fleet.Hosts {
		if host.Name != node {
			continue
		}
		address, _, err := manifest.ParseHostPort(host.Address)
		if err != nil {
			return pki.AltNames{}, fmt.Errorf("manifest host %s endpoint SAN: %w", node, err)
		}
		if ip := net.ParseIP(address); ip != nil {
			return pki.AltNames{IPAddresses: []net.IP{ip}}, nil
		}
		return pki.AltNames{DNSNames: []string{address}}, nil
	}
	return pki.AltNames{}, fmt.Errorf("manifest does not declare host %q", node)
}

func copyTrustBundle(source, destination, hostRole string, extraSANs pki.AltNames, nodeCN string) error {
	if err := pki.ValidateTrustBundleForNode(source, hostRole, extraSANs, nodeCN); err != nil {
		if issuanceErr := pki.ValidateIssuanceMaterial(source, hostRole); issuanceErr != nil {
			return err
		}
		caCert, caKey, issuanceErr := pki.EnsureCA(source, false)
		if issuanceErr != nil {
			return fmt.Errorf("issue trust bundle CA: %w", issuanceErr)
		}
		for _, role := range pki.RolesForBox(hostRole) {
			var issuanceErr error
			if hostRole == roleComputeOnly && role.Directory == "vmmd" {
				issuanceErr = pki.EnsureLeafWithCNAndSANs(source, role, nodeCN, caCert, caKey, false, extraSANs)
			} else {
				issuanceErr = pki.EnsureLeafWithSANs(source, role, caCert, caKey, false, extraSANs)
			}
			if issuanceErr != nil && !errors.Is(issuanceErr, pki.ErrLeafNotExpiringSoon) {
				return fmt.Errorf("issue trust bundle leaf %s/%s: %w", role.Directory, role.Filename, issuanceErr)
			}
		}
		if err := pki.ValidateTrustBundleForNode(source, hostRole, extraSANs, nodeCN); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(filepath.Join(destination, "ca"), 0o755); err != nil {
		return err
	}
	caCert, _ := pki.CARoot(source)
	caDest, _ := pki.CARoot(destination)
	if err := copyTrustFile(caCert, caDest, 0o444); err != nil {
		return err
	}
	for _, role := range pki.RolesForBox(hostRole) {
		cert, key := pki.LeafPaths(source, role)
		certDest, keyDest := pki.LeafPaths(destination, role)
		if err := os.MkdirAll(filepath.Dir(certDest), 0o755); err != nil {
			return err
		}
		if err := copyTrustFile(cert, certDest, 0o444); err != nil {
			return err
		}
		if err := copyTrustFile(key, keyDest, 0o400); err != nil {
			return err
		}
	}
	return nil
}

func copyTrustFile(source, destination string, mode os.FileMode) error {
	body, err := os.ReadFile(source)
	if err != nil {
		return fmt.Errorf("read trust file %s: %w", source, err)
	}
	if err := os.WriteFile(destination, body, mode); err != nil {
		return fmt.Errorf("write trust file %s: %w", destination, err)
	}
	return os.Chmod(destination, mode)
}

func overrideJoinHostVars(body []byte, opts *deployJoinOptions) []byte {
	lines := strings.Split(string(body), "\n")
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "ansible_host:") {
			lines[i] = "ansible_host: " + yamlQuote(opts.SSHHost)
		}
	}
	lines = append(lines,
		"ansible_user: "+yamlQuote(opts.SSHUser),
		"ansible_port: "+strconv.Itoa(opts.SSHPort),
	)
	if opts.StorageDevice != "" {
		lines = append(lines, "faas_storage_device: "+yamlQuote(opts.StorageDevice))
	}
	if opts.FormatStorage {
		lines = append(lines, "faas_storage_format: true")
	}
	if opts.SSHKey != "" {
		lines = append(lines, "ansible_ssh_private_key_file: "+yamlQuote(opts.SSHKey))
	}
	if opts.SSHKnownHostsFile != "" {
		lines = append(lines, "ansible_ssh_common_args: "+yamlQuote("-o UserKnownHostsFile="+opts.SSHKnownHostsFile+" -o StrictHostKeyChecking=yes"))
	}
	return []byte(strings.Join(lines, "\n"))
}

func hasComputeDatabaseEnv(path string) bool {
	body, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	seen := map[string]bool{
		"DATABASE_URL":    false,
		"FAAS_VMMD_DBURL": false,
	}
	for _, line := range strings.Split(string(body), "\n") {
		trimmed := strings.TrimSpace(line)
		for key := range seen {
			prefix := key + "="
			if strings.HasPrefix(trimmed, prefix) && len(strings.TrimSpace(strings.TrimPrefix(trimmed, prefix))) > 0 {
				seen[key] = true
			}
		}
	}
	return seen["DATABASE_URL"] && seen["FAAS_VMMD_DBURL"]
}

// validateRuntimeBasesEnv is the controller-side gate for the file that is
// copied to /etc/faas/runtime-bases.env. The Ansible roles repeat the check on
// the host, but rejecting a malformed release artifact here produces a fast,
// provider-neutral error and prevents a partial join from reaching bootstrap.
func validateRuntimeBasesEnv(path string, expected map[string]string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	required := map[string]string{
		"FAAS_DEPLOY_BASE_REF_MINIMAL":      "minimal",
		"FAAS_DEPLOY_BASE_REF_NODE22":       "node22",
		"FAAS_DEPLOY_BASE_REF_PYTHON312":    "python312",
		"FAAS_DEPLOY_BASE_REF_GO124":        "go124",
		"FAAS_DEPLOY_BASE_REF_GO124_ALPINE": "go124_alpine",
		"FAAS_DEPLOY_BASE_REF_NODE24":       "node24",
		"FAAS_DEPLOY_BASE_REF_PYTHON313":    "python313",
	}
	values := make(map[string]string, len(required))
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if _, ok := required[key]; !ok {
			continue
		}
		if _, duplicate := values[key]; duplicate {
			return fmt.Errorf("duplicate %s entry", key)
		}
		values[key] = strings.TrimSpace(value)
	}
	for envKey, manifestKey := range required {
		value, ok := values[envKey]
		if !ok {
			return fmt.Errorf("missing %s entry", envKey)
		}
		if !isDigestPinnedRuntimeRef(value) {
			return fmt.Errorf("%s must be an OCI reference pinned by @sha256:<64hex>", envKey)
		}
		if len(expected) != 0 && expected[manifestKey] != value {
			return fmt.Errorf("%s does not match release.runtime_base_refs.%s", envKey, manifestKey)
		}
	}
	return nil
}

func isDigestPinnedRuntimeRef(value string) bool {
	marker := "@sha256:"
	at := strings.LastIndex(value, marker)
	if at <= 0 || len(value[at+len(marker):]) != 64 {
		return false
	}
	if strings.ContainsAny(value[:at], "#= \t\r\n") {
		return false
	}
	for _, char := range value[at+len(marker):] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') && (char < 'A' || char > 'F') {
			return false
		}
	}
	return true
}

// validateSharedStorageEnv enforces the multi-box storage contract at the
// provider-neutral join boundary. The file is still copied verbatim to both
// hosts; parsing here only prevents a join that would silently run with
// host-local snapshots, stale-cache fallback, or without a shared registry.
func validateSharedStorageEnv(path string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	values := make(map[string]string)
	seen := make(map[string]bool)
	for _, line := range strings.Split(string(body), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		value = strings.Trim(strings.TrimSpace(value), "\"'")
		values[key] = value
		seen[key] = true
	}
	if values["FAAS_STORAGE_BACKEND"] != "oci" {
		return errors.New("must set FAAS_STORAGE_BACKEND=oci")
	}
	if !strings.HasPrefix(values["FAAS_OCI_REGISTRY"], "https://") {
		return errors.New("FAAS_OCI_REGISTRY must use https://")
	}
	if raw := values["FAAS_STORAGE_LOCAL_PREFIXES"]; raw != "" {
		for _, prefix := range strings.Split(raw, ",") {
			prefix = strings.TrimSpace(prefix)
			if !strings.HasSuffix(prefix, "/") {
				prefix += "/"
			}
			if prefix == "snap/" {
				return errors.New("FAAS_STORAGE_LOCAL_PREFIXES must not include snap/")
			}
		}
	}
	if values["FAAS_STORAGE_LOCAL_PREFIXES"] != "none" {
		return errors.New("must set FAAS_STORAGE_LOCAL_PREFIXES=none")
	}
	if values["FAAS_REQUIRE_SHARED_ARTIFACTS"] != "1" {
		return errors.New("must set FAAS_REQUIRE_SHARED_ARTIFACTS=1")
	}
	if values["FAAS_STORAGE_CACHE_SERVE_STALE"] != "0" {
		return errors.New("must set FAAS_STORAGE_CACHE_SERVE_STALE=0")
	}
	if seen["FAAS_STORAGE_CACHE_DIR"] && strings.TrimSpace(values["FAAS_STORAGE_CACHE_DIR"]) == "" {
		return errors.New("FAAS_STORAGE_CACHE_DIR must not be empty; the node-local cache is required for prepositioned wakes")
	}
	if cacheDir := strings.TrimSpace(values["FAAS_STORAGE_CACHE_DIR"]); cacheDir != "" && cacheDir != storage.DefaultOCICacheDir {
		return fmt.Errorf("FAAS_STORAGE_CACHE_DIR=%q is not supported by the managed systemd units; use %s", cacheDir, storage.DefaultOCICacheDir)
	}
	return nil
}

func releaseAssetPath(tarballPath, name string) (string, error) {
	candidates := []string{
		filepath.Join(filepath.Dir(tarballPath), name),
		tarballPath + "." + strings.TrimPrefix(name, "release."),
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path, nil
		}
	}
	return "", fmt.Errorf("release tarball is missing %s (tried %s)", name, strings.Join(candidates, ", "))
}

func emitDeployJoinReport(report deployJoinReport, applied bool, jsonOut bool) int {
	report.Applied = applied
	if jsonOut {
		jsonEmit(os.Stdout, report)
		return 0
	}
	state := "plan"
	if applied {
		state = "active"
	}
	_, _ = fmt.Fprintf(os.Stdout, "deploy join-node: %s node=%s release=%s ssh=%s\n", state, report.DatabaseNode, report.ReleaseGitSHA, report.SSHHost)
	for i, step := range report.Steps {
		_, _ = fmt.Fprintf(os.Stdout, "  %d. %s\n", i+1, step)
	}
	for _, timing := range report.Timings {
		_, _ = fmt.Fprintf(os.Stdout, "  timing %s=%dms\n", timing.Phase, timing.DurationMS)
	}
	return 0
}
