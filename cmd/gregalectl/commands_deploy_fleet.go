package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
	"gopkg.in/yaml.v3"
)

// joinFleetFile is intentionally provider-neutral. Provisioning systems only
// need to write this small file after creating machines; all release, PKI,
// role, and control-plane details remain in the shared manifest/artifact set.
type joinFleetFile struct {
	Nodes []joinFleetNode `yaml:"nodes" json:"nodes"`
}

type joinFleetNode struct {
	Node          string `yaml:"node" json:"node"`
	SSHHost       string `yaml:"ssh_host" json:"ssh_host"`
	SSHUser       string `yaml:"ssh_user,omitempty" json:"ssh_user,omitempty"`
	SSHPort       int    `yaml:"ssh_port,omitempty" json:"ssh_port,omitempty"`
	SSHKey        string `yaml:"ssh_key,omitempty" json:"ssh_key,omitempty"`
	HostKeySHA256 string `yaml:"host_key_sha256,omitempty" json:"host_key_sha256,omitempty"`
	StorageDevice string `yaml:"storage_device,omitempty" json:"storage_device,omitempty"`
	FormatStorage bool   `yaml:"format_storage,omitempty" json:"format_storage,omitempty"`
}

type joinFleetResult struct {
	Reports []deployJoinReport `json:"reports"`
	Errors  map[string]string  `json:"errors,omitempty"`
}

func cmdDeployJoinFleet(args []string) int {
	fs := flag.NewFlagSet("deploy join-fleet", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	nodesFile := fs.String("nodes-file", "", "YAML/JSON node list containing node and ssh_host")
	claimFile := fs.String("claim-file", "", "single ComputeNodeClaim YAML/JSON file (alternative to --nodes-file)")
	manifestFile := fs.String("manifest-file", "", "split-box manifest (required)")
	artifactDir := fs.String("artifact-dir", "", "standard directory containing shared join assets")
	releaseTarball := fs.String("release-tarball", "", "signed release.tar.gz")
	bootstrapBinary := fs.String("bootstrap-binary", "", "Linux bootstrap gregalectl")
	cosignBinary := fs.String("cosign-binary", "", "cosign verifier")
	pkiSource := fs.String("pki-dir", "", "compute trust-bundle directory")
	signKey := fs.String("sign-key", "", "image-signing private key")
	verifyKey := fs.String("verify-key", "", "image-signing public key")
	computeDBEnv := fs.String("compute-db-env", "", "root-only compute DB environment")
	storageEnv := fs.String("storage-env", "", "shared OCI storage.env source")
	runtimeBasesEnv := fs.String("runtime-bases-env", "", "release-bound digest-pinned runtime base refs")
	ansibleVars := fs.String("ansible-vars-file", "", "optional provider/overlay Ansible vars")
	repoRoot := fs.String("repo-root", "", "path to the faas repository")
	maxParallel := fs.Int("max-parallel", 4, "maximum number of nodes converged at once")
	skipPreflight := fs.Bool("skip-fleet-preflight", false, "skip the one shared complete-fleet preflight")
	resume := fs.Bool("resume", false, "resume failed/interrupted jobs")
	timeout := fs.Duration("timeout", 20*time.Minute, "maximum time per node")
	leaseTTL := fs.Duration("lease-ttl", 30*time.Minute, "database lease per node")
	continueOnError := fs.Bool("continue-on-error", false, "continue other nodes after one join fails")
	dryRun := fs.Bool("dry-run", false, "validate the complete fleet plan without contacting hosts")
	yes := fs.Bool("yes", false, "approve the remote adoption")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-fleet: unexpected positional argument")
		return 2
	}
	if (*nodesFile == "" && *claimFile == "") || (*nodesFile != "" && *claimFile != "") || *manifestFile == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-fleet: exactly one of --nodes-file or --claim-file and --manifest-file are required")
		return 2
	}
	if *maxParallel < 1 || *timeout <= 0 || *leaseTTL <= 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-fleet: max-parallel, timeout, and lease-ttl must be positive")
		return 2
	}
	file, err := loadJoinFleetInputs(*nodesFile, *claimFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy join-fleet: %v\n", err)
		return 1
	}
	if *repoRoot == "" {
		*repoRoot = defaultRepoRoot()
	}
	opts := make([]deployJoinOptions, 0, len(file.Nodes))
	reports := make([]deployJoinReport, 0, len(file.Nodes))
	seen := make(map[string]bool)
	for _, n := range file.Nodes {
		if n.Node == "" || n.SSHHost == "" {
			fmt.Fprintln(os.Stderr, "gregalectl deploy join-fleet: every node needs node and ssh_host")
			return 1
		}
		if seen[n.Node] {
			fmt.Fprintf(os.Stderr, "gregalectl deploy join-fleet: duplicate node %q\n", n.Node)
			return 1
		}
		seen[n.Node] = true
		if n.SSHUser == "" {
			n.SSHUser = "root"
		}
		if n.SSHPort == 0 {
			n.SSHPort = 22
		}
		o := deployJoinOptions{
			ManifestFile: *manifestFile, Node: n.Node, SSHHost: n.SSHHost,
			SSHUser: n.SSHUser, SSHPort: n.SSHPort, SSHKey: n.SSHKey,
			SSHHostKeySHA256: n.HostKeySHA256,
			StorageDevice:    n.StorageDevice, FormatStorage: n.FormatStorage,
			ReleaseTarball: *releaseTarball, BootstrapBinary: *bootstrapBinary,
			CosignBinary: *cosignBinary, PKISource: *pkiSource,
			SignKeySource: *signKey, VerifyKeySource: *verifyKey,
			ComputeDBEnvSource: *computeDBEnv, StorageEnvSource: *storageEnv, RuntimeBasesEnvSource: *runtimeBasesEnv, ArtifactDir: *artifactDir,
			AnsibleVarsFile: *ansibleVars, RepoRoot: *repoRoot,
			SkipFleetPreflight: *skipPreflight, Resume: *resume,
			Timeout: *timeout, LeaseTTL: *leaseTTL, DryRun: *dryRun, Yes: *yes,
			JSON: *jsonOut || jsonOutput,
		}
		resolveJoinArtifacts(&o)
		if !o.DryRun && o.Yes {
			if code, handled := maybeBootstrapReleaseCLIFromTarball(o.ReleaseTarball, o.ReleaseGitSHA); handled {
				return code
			}
		}
		report, validateErr := deployJoinValidate(o)
		if validateErr != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy join-fleet: node %s: %v\n", n.Node, validateErr)
			return 1
		}
		opts = append(opts, o)
		reports = append(reports, report)
	}
	if *dryRun {
		if err := emitJoinFleetResult(joinFleetResult{Reports: reports}, *jsonOut || jsonOutput); err != nil {
			return 1
		}
		return 0
	}
	if !*yes {
		fmt.Fprintln(os.Stderr, "gregalectl deploy join-fleet: re-run with --yes to adopt the listed hosts")
		return 2
	}
	if !*skipPreflight {
		if err := runJoinFleetPreflight(context.Background(), opts); err != nil {
			fmt.Fprintf(os.Stderr, "gregalectl deploy join-fleet: shared fleet preflight: %v\n", err)
			return 3
		}
		for i := range opts {
			opts[i].SkipFleetPreflight = true
			reports[i].FleetPreflight = true
		}
	}

	result := joinFleetResult{Reports: reports, Errors: make(map[string]string)}
	var mu sync.Mutex
	stopAfterFailure := false
	queue := make(chan int)
	workers := *maxParallel
	if workers > len(opts) {
		workers = len(opts)
	}
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range queue {
				mu.Lock()
				stopped := stopAfterFailure
				if stopped {
					result.Errors[opts[i].Node] = "not attempted after an earlier join failure"
				}
				mu.Unlock()
				if stopped {
					continue
				}
				code, joinErr := executeDeployJoin(opts[i], &result.Reports[i])
				if joinErr == nil && code == 0 {
					continue
				}
				mu.Lock()
				result.Errors[opts[i].Node] = joinErrString(code, joinErr)
				if !*continueOnError {
					stopAfterFailure = true
				}
				mu.Unlock()
			}
		}()
	}
	for i := range opts {
		queue <- i
	}
	close(queue)
	wg.Wait()
	if len(result.Errors) == 0 {
		result.Errors = nil
	}
	if err := emitJoinFleetResult(result, *jsonOut || jsonOutput); err != nil {
		return 1
	}
	if len(result.Errors) > 0 {
		return 3
	}
	return 0
}

func loadJoinFleetFile(path string) (joinFleetFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return joinFleetFile{}, err
	}
	var file joinFleetFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return joinFleetFile{}, fmt.Errorf("parse nodes file: %w", err)
	}
	if len(file.Nodes) == 0 {
		return joinFleetFile{}, errors.New("nodes file has no nodes")
	}
	return file, nil
}

func loadJoinFleetInputs(nodesPath, claimPath string) (joinFleetFile, error) {
	if claimPath == "" {
		return loadJoinFleetFile(nodesPath)
	}
	claim, err := nodeclaim.Load(claimPath)
	if err != nil {
		return joinFleetFile{}, err
	}
	if errs := claim.Validate(); errs != nil {
		return joinFleetFile{}, errs
	}
	n := claim.Normalize()
	return joinFleetFile{Nodes: []joinFleetNode{{
		Node: n.Name, SSHHost: n.SSHHost, SSHUser: n.SSHUser, SSHPort: n.SSHPort,
		HostKeySHA256: n.HostKeySHA256,
		StorageDevice: n.StorageDevice, FormatStorage: n.FormatStorage,
	}}}, nil
}

func runJoinFleetPreflight(ctx context.Context, opts []deployJoinOptions) error {
	if len(opts) == 0 {
		return errors.New("no nodes to preflight")
	}
	tempRoot, err := os.MkdirTemp("", "gregale-fleet-preflight-")
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(tempRoot) }()
	m, err := manifest.Load(opts[0].ManifestFile)
	if err != nil {
		return err
	}
	files, err := renderManifestAnsibleFiles(m, tempRoot)
	if err != nil {
		return fmt.Errorf("render inventory: %w", err)
	}
	byNode := make(map[string]deployJoinOptions, len(opts))
	for _, o := range opts {
		byNode[o.Node] = o
	}
	for i := range files {
		name := filepath.Base(files[i].Path)
		if o, ok := byNode[name[:len(name)-len(filepath.Ext(name))]]; ok {
			files[i].Body = overrideJoinHostVars(files[i].Body, &o)
		}
		if err := writeGeneratedAnsibleFile(files[i].Path, files[i].Body, true); err != nil {
			return err
		}
	}
	args := []string{"-i", filepath.Join(tempRoot, "inventory", "hosts.ini")}
	if opts[0].AnsibleVarsFile != "" {
		args = append(args, "-e", "@"+opts[0].AnsibleVarsFile)
	}
	args = append(args, filepath.Join(opts[0].RepoRoot, "deploy/ansible", "preflight.yml"))
	return ansiblePlaybookRunner(ctx, filepath.Join(opts[0].RepoRoot, "deploy/ansible"), args)
}

func joinErrString(code int, err error) string {
	if err != nil {
		return fmt.Sprintf("exit=%d: %v", code, err)
	}
	return fmt.Sprintf("exit=%d", code)
}

func emitJoinFleetResult(result joinFleetResult, jsonOut bool) error {
	if jsonOut {
		jsonEmit(os.Stdout, result)
		return nil
	}
	for _, report := range result.Reports {
		state := "active"
		if !report.Applied {
			state = "failed"
		}
		_, _ = fmt.Fprintf(os.Stdout, "deploy join-fleet: %s node=%s release=%s ssh=%s\n", state, report.DatabaseNode, report.ReleaseGitSHA, report.SSHHost)
	}
	for node, errText := range result.Errors {
		_, _ = fmt.Fprintf(os.Stdout, "  error %s: %s\n", node, errText)
	}
	return nil
}
