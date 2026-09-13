package main

// `deploy prepare-node` turns the provider handoff into a reusable, verified
// join directory. It deliberately stops before SSH: creating the machine and
// establishing the first trusted connection remain provider/operator work,
// while release, topology, PKI, and secret staging stay identical everywhere.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/manifest"
	"github.com/onebox-faas/faas/pkg/nodeclaim"
	"github.com/onebox-faas/faas/pkg/releaseinstall"
	"gopkg.in/yaml.v3"
)

const (
	defaultPrepareReleaseRepo = "poyrazK/faas"
	defaultPrepareAPIBaseURL  = "https://api.github.com"
)

var prepareReleaseTagPattern = regexp.MustCompile(`^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$`)

var prepareReleaseAssetNames = []string{
	"release.tar.gz",
	"release.cosign.bundle",
	"release.sbom.json",
	"release-manifest.json",
	"production-manifest.yaml",
	"SHA256SUMS",
}

var prepareChecksumAssetNames = []string{
	"release.tar.gz",
	"release.cosign.bundle",
	"release.sbom.json",
	"production-manifest.yaml",
}

type deployPrepareOptions struct {
	ClaimFile    string
	NodesFile    string
	ManifestFile string
	ReleaseTag   string
	ReleaseRepo  string
	SecretsDir   string
	OutputDir    string
	CacheDir     string
	CosignBinary string
}

type prepareNodeReport struct {
	ReleaseTag     string                `json:"release_tag"`
	ReleaseGitSHA  string                `json:"release_git_sha"`
	ManifestHash   string                `json:"manifest_hash"`
	OutputDir      string                `json:"output_dir"`
	CacheDir       string                `json:"cache_dir"`
	NodesFile      string                `json:"nodes_file"`
	Claims         []prepareClaimReport  `json:"claims"`
	Assets         []prepareAssetReport  `json:"assets"`
	CosignCacheHit bool                  `json:"cosign_cache_hit"`
	CosignSHA256   string                `json:"cosign_sha256"`
	JoinCommand    string                `json:"join_command"`
	Timings        []prepareTimingReport `json:"timings"`
}

type prepareClaimReport struct {
	Node          string `json:"node"`
	SSHHost       string `json:"ssh_host"`
	SSHUser       string `json:"ssh_user"`
	SSHPort       int    `json:"ssh_port"`
	HostKeySHA256 string `json:"host_key_sha256,omitempty"`
	StorageDevice string `json:"storage_device,omitempty"`
	FormatStorage bool   `json:"format_storage,omitempty"`
}

type prepareAssetReport struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	SHA256   string `json:"sha256"`
	CacheHit bool   `json:"cache_hit"`
}

type prepareTimingReport struct {
	Phase      string `json:"phase"`
	DurationMS int64  `json:"duration_ms"`
}

type prepareReleaseOptions struct {
	Repo       string
	Tag        string
	CacheDir   string
	Token      string
	APIBaseURL string
	HTTPClient *http.Client
}

type prepareReleaseResult struct {
	Paths  map[string]string
	Assets []prepareAssetReport
}

type githubReleaseResponse struct {
	Assets []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

func cmdDeployPrepareNode(args []string) int {
	fs := flag.NewFlagSet("deploy prepare-node", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	claimFile := fs.String("claim-file", "", "provider-produced ComputeNodeClaim YAML/JSON (alternative to --nodes-file)")
	nodesFile := fs.String("nodes-file", "", "provider connection list YAML/JSON (alternative to --claim-file)")
	manifestFile := fs.String("manifest-file", "", "signed production manifest (required)")
	releaseTag := fs.String("release-tag", "", "signed release tag, for example v0.1.18-rc.15 (required)")
	releaseRepo := fs.String("release-repo", defaultPrepareReleaseRepo, "GitHub owner/repository containing the release")
	secretsDir := fs.String("secrets-dir", "", "directory containing compute-ssh-key, compute-db.env, storage.env, signing keys, and pki/ (required)")
	outputDir := fs.String("output-dir", "", "prepared join artifact directory (required)")
	cacheDir := fs.String("cache-dir", "", "persistent cache for verified public release assets")
	cosignBinary := fs.String("cosign-binary", "", "Linux/amd64 cosign binary to verify and stage (default: COSIGN_BINARY or PATH)")
	jsonOut := fs.Bool("json", false, "emit structured JSON")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "gregalectl deploy prepare-node: unexpected positional argument")
		return 2
	}
	if (*claimFile == "") == (*nodesFile == "") {
		fmt.Fprintln(os.Stderr, "gregalectl deploy prepare-node: exactly one of --claim-file or --nodes-file is required")
		return 2
	}
	if *manifestFile == "" || *releaseTag == "" || *secretsDir == "" || *outputDir == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy prepare-node: --manifest-file, --release-tag, --secrets-dir, and --output-dir are required")
		return 2
	}
	if !prepareReleaseTagPattern.MatchString(*releaseTag) {
		fmt.Fprintf(os.Stderr, "gregalectl deploy prepare-node: invalid --release-tag %q\n", *releaseTag)
		return 2
	}
	if *cacheDir == "" {
		*cacheDir = defaultPrepareCacheDir()
	}
	if *releaseRepo == "" {
		fmt.Fprintln(os.Stderr, "gregalectl deploy prepare-node: --release-repo cannot be empty")
		return 2
	}

	report, err := prepareNode(context.Background(), deployPrepareOptions{
		ClaimFile: *claimFile, NodesFile: *nodesFile, ManifestFile: *manifestFile,
		ReleaseTag: *releaseTag, ReleaseRepo: *releaseRepo, SecretsDir: *secretsDir,
		OutputDir: *outputDir, CacheDir: *cacheDir, CosignBinary: *cosignBinary,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "gregalectl deploy prepare-node: %v\n", err)
		return 1
	}
	if *jsonOut || jsonOutput {
		jsonEmit(os.Stdout, report)
		return 0
	}
	_, _ = fmt.Fprintf(os.Stdout, "deploy prepare-node: ready release=%s git_sha=%s claims=%d\n", report.ReleaseTag, report.ReleaseGitSHA, len(report.Claims))
	_, _ = fmt.Fprintf(os.Stdout, "  artifacts: %s\n", report.OutputDir)
	_, _ = fmt.Fprintf(os.Stdout, "  cache: %s\n", report.CacheDir)
	for _, asset := range report.Assets {
		state := "downloaded"
		if asset.CacheHit {
			state = "cached"
		}
		_, _ = fmt.Fprintf(os.Stdout, "  %s: %s (%s)\n", asset.Name, state, asset.SHA256[:12])
	}
	for _, timing := range report.Timings {
		_, _ = fmt.Fprintf(os.Stdout, "  timing %s=%dms\n", timing.Phase, timing.DurationMS)
	}
	_, _ = fmt.Fprintf(os.Stdout, "\nNext:\n  %s\n", report.JoinCommand)
	return 0
}

func prepareNode(ctx context.Context, opts deployPrepareOptions) (prepareNodeReport, error) {
	started := time.Now()
	report := prepareNodeReport{
		ReleaseTag: opts.ReleaseTag,
		OutputDir:  opts.OutputDir,
		CacheDir:   opts.CacheDir,
	}
	addTiming := func(phase string, since time.Time) {
		report.Timings = append(report.Timings, prepareTimingReport{Phase: phase, DurationMS: time.Since(since).Milliseconds()})
	}

	m, err := manifest.Load(opts.ManifestFile)
	if err != nil {
		return report, fmt.Errorf("load manifest: %w", err)
	}
	if errs := m.Validate(); errs != nil {
		return report, fmt.Errorf("invalid manifest: %w", errs)
	}

	inputs, err := loadJoinFleetInputs(opts.NodesFile, opts.ClaimFile)
	if err != nil {
		return report, fmt.Errorf("load provider handoff: %w", err)
	}
	if err := validatePreparedNodes(m, inputs.Nodes); err != nil {
		return report, err
	}

	cosignPath, err := resolvePrepareCosignBinary(opts.CosignBinary)
	if err != nil {
		return report, err
	}
	releaseStarted := time.Now()
	release, err := prepareReleaseAssets(ctx, prepareReleaseOptions{
		Repo: opts.ReleaseRepo, Tag: opts.ReleaseTag, CacheDir: opts.CacheDir,
		Token: prepareGitHubToken(), APIBaseURL: defaultPrepareAPIBaseURL,
	})
	if err != nil {
		return report, err
	}
	addTiming("release_cache", releaseStarted)

	verifyStarted := time.Now()
	releaseManifest, err := verifyPreparedRelease(ctx, release.Paths, cosignPath, opts.ManifestFile)
	if err != nil {
		return report, err
	}
	addTiming("release_verify", verifyStarted)
	if m.Release.GitSHA != releaseManifest.GitSHA {
		return report, fmt.Errorf("topology manifest release.git_sha=%s does not match signed release %s", m.Release.GitSHA, releaseManifest.GitSHA)
	}
	report.ReleaseGitSHA = releaseManifest.GitSHA
	report.ManifestHash = releaseManifest.ManifestHash
	report.Assets = release.Assets

	stageStarted := time.Now()
	if err := os.MkdirAll(opts.OutputDir, 0o700); err != nil {
		return report, fmt.Errorf("create output directory: %w", err)
	}
	if err := os.Chmod(opts.OutputDir, 0o700); err != nil {
		return report, fmt.Errorf("lock output directory: %w", err)
	}
	for _, name := range prepareReleaseAssetNames {
		mode := os.FileMode(0o644)
		if err := prepareCopyFile(release.Paths[name], filepath.Join(opts.OutputDir, name), mode); err != nil {
			return report, fmt.Errorf("stage release asset %s: %w", name, err)
		}
	}
	tarballBody, err := os.ReadFile(release.Paths["release.tar.gz"])
	if err != nil {
		return report, fmt.Errorf("read cached release tarball: %w", err)
	}
	cliBytes, err := extractTarballMember(tarballBody, "gregalectl")
	if err != nil {
		return report, fmt.Errorf("extract released gregalectl: %w", err)
	}
	if err := writePrepareFile(filepath.Join(opts.OutputDir, "gregalectl-linux-amd64"), cliBytes, 0o755); err != nil {
		return report, fmt.Errorf("stage released gregalectl: %w", err)
	}
	cosignCacheHit, cosignCachedPath, cosignDigest, err := cachePrepareTool(opts.CacheDir, cosignPath)
	if err != nil {
		return report, fmt.Errorf("cache cosign: %w", err)
	}
	if err := prepareCopyFile(cosignCachedPath, filepath.Join(opts.OutputDir, "cosign-linux-amd64"), 0o755); err != nil {
		return report, fmt.Errorf("stage cosign: %w", err)
	}
	if err := stagePrepareSecrets(opts.SecretsDir, opts.OutputDir); err != nil {
		return report, err
	}

	preparedNodes := make([]joinFleetNode, len(inputs.Nodes))
	for i, node := range inputs.Nodes {
		preparedNodes[i] = node
		if preparedNodes[i].SSHUser == "" {
			preparedNodes[i].SSHUser = "root"
		}
		if preparedNodes[i].SSHPort == 0 {
			preparedNodes[i].SSHPort = 22
		}
		// The prepared directory is the portable handoff. It carries one
		// shared operator key rather than leaking a workstation-specific path
		// into a nodes file that may be moved to the fleet runner.
		preparedNodes[i].SSHKey = filepath.Join(opts.OutputDir, "compute-ssh-key")
	}
	nodesPath := filepath.Join(opts.OutputDir, "nodes.yaml")
	nodesBody, err := yaml.Marshal(joinFleetFile{Nodes: preparedNodes})
	if err != nil {
		return report, fmt.Errorf("encode prepared nodes: %w", err)
	}
	if err := writePrepareFile(nodesPath, nodesBody, 0o600); err != nil {
		return report, fmt.Errorf("write prepared nodes: %w", err)
	}
	addTiming("artifact_stage", stageStarted)

	report.NodesFile = nodesPath
	report.CosignCacheHit = cosignCacheHit
	report.CosignSHA256 = cosignDigest
	for _, node := range preparedNodes {
		report.Claims = append(report.Claims, prepareClaimReport{
			Node: node.Node, SSHHost: node.SSHHost, SSHUser: node.SSHUser, SSHPort: node.SSHPort,
			HostKeySHA256: node.HostKeySHA256, StorageDevice: node.StorageDevice, FormatStorage: node.FormatStorage,
		})
	}
	report.JoinCommand = strings.Join([]string{
		"gregalectl deploy join-fleet",
		"--nodes-file", shellQuote(nodesPath),
		"--manifest-file", shellQuote(filepath.Join(opts.OutputDir, "production-manifest.yaml")),
		"--artifact-dir", shellQuote(opts.OutputDir),
		"--yes",
	}, " ")
	addTiming("total", started)
	return report, nil
}

func validatePreparedNodes(m *manifest.Manifest, nodes []joinFleetNode) error {
	hosts := make(map[string]manifest.Host, len(m.Fleet.Hosts))
	for _, host := range m.Fleet.Hosts {
		hosts[host.Name] = host
	}
	seen := make(map[string]bool, len(nodes))
	for _, node := range nodes {
		if node.Node == "" || node.SSHHost == "" {
			return errors.New("provider handoff: every node needs node and ssh_host")
		}
		if seen[node.Node] {
			return fmt.Errorf("provider handoff: duplicate node %q", node.Node)
		}
		seen[node.Node] = true
		host, ok := hosts[node.Node]
		if !ok {
			return fmt.Errorf("manifest does not declare node %q", node.Node)
		}
		if host.Role != roleComputeOnly {
			return fmt.Errorf("manifest node %q has role %q; prepare-node requires compute-only", node.Node, host.Role)
		}
		if node.SSHPort < 0 || node.SSHPort > 65535 {
			return fmt.Errorf("node %q has invalid ssh_port %d", node.Node, node.SSHPort)
		}
		if node.HostKeySHA256 != "" {
			if err := nodeclaim.ValidateHostKeyFingerprint(node.HostKeySHA256); err != nil {
				return fmt.Errorf("node %q host_key_sha256: %w", node.Node, err)
			}
		}
		if node.StorageDevice != "" && !filepath.IsAbs(node.StorageDevice) {
			return fmt.Errorf("node %q storage_device %q is not an absolute path", node.Node, node.StorageDevice)
		}
	}
	return nil
}

func defaultPrepareCacheDir() string {
	if dir, err := os.UserCacheDir(); err == nil && dir != "" {
		return filepath.Join(dir, "gregale", "deploy")
	}
	return filepath.Join(".gregale-cache", "deploy")
}

func prepareGitHubToken() string {
	if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
		return token
	}
	return strings.TrimSpace(os.Getenv("GITHUB_TOKEN"))
}

func prepareReleaseAssets(ctx context.Context, opts prepareReleaseOptions) (prepareReleaseResult, error) {
	if !strings.Contains(opts.Repo, "/") {
		return prepareReleaseResult{}, fmt.Errorf("release repository %q must be owner/repository", opts.Repo)
	}
	if !prepareReleaseTagPattern.MatchString(opts.Tag) {
		return prepareReleaseResult{}, fmt.Errorf("release tag %q is not a supported release tag", opts.Tag)
	}
	if opts.CacheDir == "" {
		return prepareReleaseResult{}, errors.New("release cache directory is empty")
	}
	apiBase := strings.TrimRight(opts.APIBaseURL, "/")
	if apiBase == "" {
		apiBase = defaultPrepareAPIBaseURL
	}
	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 2 * time.Minute}
	}
	parts := strings.Split(opts.Repo, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return prepareReleaseResult{}, fmt.Errorf("release repository %q must be owner/repository", opts.Repo)
	}
	metadataURL := apiBase + "/repos/" + url.PathEscape(parts[0]) + "/" + url.PathEscape(parts[1]) + "/releases/tags/" + url.PathEscape(opts.Tag)
	var metadata githubReleaseResponse
	if err := prepareGETJSON(ctx, client, metadataURL, opts.Token, &metadata); err != nil {
		return prepareReleaseResult{}, fmt.Errorf("load GitHub release %s: %w", opts.Tag, err)
	}
	assetURLs := make(map[string]string, len(metadata.Assets))
	for _, asset := range metadata.Assets {
		if asset.Name != "" && asset.BrowserDownloadURL != "" {
			assetURLs[asset.Name] = asset.BrowserDownloadURL
		}
	}
	for _, name := range prepareReleaseAssetNames {
		if assetURLs[name] == "" {
			return prepareReleaseResult{}, fmt.Errorf("release %s is missing asset %s", opts.Tag, name)
		}
	}

	// Keep repositories isolated even when two repositories publish the same
	// tag. The digest also prevents an untrusted repository string from being
	// interpreted as a local path component.
	repoCacheKey := sha256HexString([]byte(opts.Repo))[:16]
	releaseDir := filepath.Join(opts.CacheDir, "releases", repoCacheKey, opts.Tag)
	if err := os.MkdirAll(releaseDir, 0o750); err != nil {
		return prepareReleaseResult{}, fmt.Errorf("create release cache: %w", err)
	}
	paths := make(map[string]string, len(prepareReleaseAssetNames))
	for _, name := range prepareReleaseAssetNames {
		paths[name] = filepath.Join(releaseDir, name)
	}

	// Fetch the checksum index on every invocation. It is tiny, detects a
	// mutable/replaced release tag, and lets all large assets remain cached.
	sumsBody, err := prepareGETBytes(ctx, client, assetURLs["SHA256SUMS"], opts.Token)
	if err != nil {
		return prepareReleaseResult{}, fmt.Errorf("download SHA256SUMS: %w", err)
	}
	if existing, readErr := os.ReadFile(paths["SHA256SUMS"]); readErr == nil && !bytes.Equal(existing, sumsBody) {
		return prepareReleaseResult{}, fmt.Errorf("cached release %s changed its SHA256SUMS; use a new tag or clear %s", opts.Tag, releaseDir)
	}
	if err := writePrepareFile(paths["SHA256SUMS"], sumsBody, 0o644); err != nil {
		return prepareReleaseResult{}, fmt.Errorf("cache SHA256SUMS: %w", err)
	}
	expected, err := parsePrepareChecksums(sumsBody)
	if err != nil {
		return prepareReleaseResult{}, err
	}

	result := prepareReleaseResult{Paths: paths}
	for _, name := range prepareReleaseAssetNames {
		if name == "SHA256SUMS" {
			continue
		}
		path := paths[name]
		expectedHash := expected[name]
		cacheHit := false
		if info, statErr := os.Stat(path); statErr == nil && info.Mode().IsRegular() {
			got, hashErr := prepareFileSHA256(path)
			cacheHit = hashErr == nil && (name == "release-manifest.json" || got == expectedHash)
		}
		// release-manifest.json is intentionally not in SHA256SUMS, but it is
		// still fetched on each run so a mutable tag cannot silently change the
		// external manifest while the signed tarball remains the authority.
		if name == "release-manifest.json" {
			cacheHit = false
		}
		if !cacheHit {
			body, downloadErr := prepareGETBytes(ctx, client, assetURLs[name], opts.Token)
			if downloadErr != nil {
				return prepareReleaseResult{}, fmt.Errorf("download %s: %w", name, downloadErr)
			}
			if name == "release-manifest.json" {
				if existing, readErr := os.ReadFile(path); readErr == nil {
					if !bytes.Equal(existing, body) {
						return prepareReleaseResult{}, fmt.Errorf("cached release %s changed its release-manifest.json; use a new tag or clear %s", opts.Tag, releaseDir)
					}
					cacheHit = true
				}
			}
			if name != "release-manifest.json" {
				got := sha256HexString(body)
				if got != expectedHash {
					return prepareReleaseResult{}, fmt.Errorf("release asset %s sha256=%s, want %s", name, got, expectedHash)
				}
			}
			if err := writePrepareFile(path, body, 0o644); err != nil {
				return prepareReleaseResult{}, fmt.Errorf("cache %s: %w", name, err)
			}
		}
		hash, hashErr := prepareFileSHA256(path)
		if hashErr != nil {
			return prepareReleaseResult{}, fmt.Errorf("hash cached %s: %w", name, hashErr)
		}
		result.Assets = append(result.Assets, prepareAssetReport{Name: name, Path: path, SHA256: hash, CacheHit: cacheHit})
	}
	sort.Slice(result.Assets, func(i, j int) bool { return result.Assets[i].Name < result.Assets[j].Name })
	return result, nil
}

func prepareGETJSON(ctx context.Context, client *http.Client, endpoint, token string, dst any) error {
	body, err := prepareGETBytesWithAccept(ctx, client, endpoint, token, "application/vnd.github+json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func prepareGETBytes(ctx context.Context, client *http.Client, endpoint, token string) ([]byte, error) {
	return prepareGETBytesWithAccept(ctx, client, endpoint, token, "application/octet-stream")
}

func prepareGETBytesWithAccept(ctx context.Context, client *http.Client, endpoint, token, accept string) ([]byte, error) {
	u, err := url.Parse(endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return nil, fmt.Errorf("invalid HTTPS asset URL %q", endpoint)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "gregalectl/prepare-node")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: HTTP %s", endpoint, resp.Status)
	}
	if resp.Request == nil || resp.Request.URL.Scheme != "https" || resp.Request.URL.Host == "" {
		return nil, fmt.Errorf("HTTPS request redirected to an invalid URL for %s", endpoint)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 512<<20))
	if err != nil {
		return nil, err
	}
	return body, nil
}

func parsePrepareChecksums(body []byte) (map[string]string, error) {
	checksums := make(map[string]string)
	for _, line := range strings.Split(string(body), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || strings.HasPrefix(fields[0], "#") {
			continue
		}
		if len(fields[0]) != sha256.Size*2 {
			return nil, fmt.Errorf("invalid SHA256SUMS entry %q", line)
		}
		if _, err := hex.DecodeString(fields[0]); err != nil {
			return nil, fmt.Errorf("invalid SHA256SUMS digest %q: %w", fields[0], err)
		}
		checksums[strings.TrimPrefix(fields[1], "*")] = fields[0]
	}
	for _, name := range prepareChecksumAssetNames {
		if checksums[name] == "" {
			return nil, fmt.Errorf("SHA256SUMS is missing %s", name)
		}
	}
	return checksums, nil
}

func verifyPreparedRelease(ctx context.Context, paths map[string]string, cosignPath, manifestPath string) (releaseinstall.Manifest, error) {
	for _, name := range prepareChecksumAssetNames {
		if _, err := os.Stat(paths[name]); err != nil {
			return releaseinstall.Manifest{}, fmt.Errorf("prepared release missing %s: %w", name, err)
		}
	}
	if err := verifyPrepareChecksums(paths); err != nil {
		return releaseinstall.Manifest{}, err
	}
	cfg := releaseinstall.DefaultGitHubOIDC()
	cfg.CosignPath = cosignPath
	verifier := releaseinstall.NewExecCosignVerifier(cfg)
	if _, err := verifier.VerifyBlob(ctx, paths["release.tar.gz"], paths["release.cosign.bundle"]); err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("verify signed release tarball: %w", err)
	}
	packed, err := os.ReadFile(paths["release.tar.gz"])
	if err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("read release tarball: %w", err)
	}
	embedded, err := extractTarballMember(packed, releaseinstall.ManifestName)
	if err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("read embedded release manifest: %w", err)
	}
	releaseBody, err := os.ReadFile(paths["release-manifest.json"])
	if err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("read release-manifest.json: %w", err)
	}
	if !bytes.Equal(embedded, releaseBody) {
		return releaseinstall.Manifest{}, errors.New("release-manifest.json does not match the signed tarball")
	}
	var releaseManifest releaseinstall.Manifest
	if err := json.Unmarshal(releaseBody, &releaseManifest); err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("decode release-manifest.json: %w", err)
	}
	if err := releaseinstall.ValidateManifest(releaseManifest); err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("validate release manifest: %w", err)
	}
	productionBody, err := os.ReadFile(paths["production-manifest.yaml"])
	if err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("read production manifest asset: %w", err)
	}
	productionHash := "sha256:" + sha256HexString(productionBody)
	if releaseManifest.ManifestHash != productionHash {
		return releaseinstall.Manifest{}, fmt.Errorf("release manifest hash=%s, production manifest hash=%s", releaseManifest.ManifestHash, productionHash)
	}
	operatorManifest, err := os.ReadFile(manifestPath)
	if err != nil {
		return releaseinstall.Manifest{}, fmt.Errorf("read operator manifest: %w", err)
	}
	if !bytes.Equal(operatorManifest, productionBody) {
		return releaseinstall.Manifest{}, errors.New("operator manifest does not match the signed release production-manifest.yaml")
	}
	return releaseManifest, nil
}

func verifyPrepareChecksums(paths map[string]string) error {
	sums, err := os.ReadFile(paths["SHA256SUMS"])
	if err != nil {
		return fmt.Errorf("read SHA256SUMS: %w", err)
	}
	checksums, err := parsePrepareChecksums(sums)
	if err != nil {
		return err
	}
	for _, name := range prepareChecksumAssetNames {
		got, err := prepareFileSHA256(paths[name])
		if err != nil {
			return fmt.Errorf("hash %s: %w", name, err)
		}
		if got != checksums[name] {
			return fmt.Errorf("checksum mismatch for %s: got %s, want %s", name, got, checksums[name])
		}
	}
	return nil
}

func resolvePrepareCosignBinary(explicit string) (string, error) {
	candidate := strings.TrimSpace(explicit)
	if candidate == "" {
		candidate = strings.TrimSpace(os.Getenv("COSIGN_BINARY"))
	}
	if candidate == "" {
		found, err := exec.LookPath("cosign")
		if err != nil {
			return "", errors.New("cosign is required; pass --cosign-binary with the pinned Linux/amd64 binary or install cosign on PATH")
		}
		candidate = found
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", fmt.Errorf("resolve cosign binary: %w", err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("cosign binary: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&0o111 == 0 {
		return "", fmt.Errorf("cosign binary %q is not an executable regular file", candidate)
	}
	return resolved, nil
}

func cachePrepareTool(cacheDir, source string) (bool, string, string, error) {
	digest, err := prepareFileSHA256(source)
	if err != nil {
		return false, "", "", err
	}
	toolDir := filepath.Join(cacheDir, "tools")
	if err := os.MkdirAll(toolDir, 0o750); err != nil {
		return false, "", "", err
	}
	destination := filepath.Join(toolDir, "cosign-linux-amd64-"+digest)
	if info, statErr := os.Stat(destination); statErr == nil && info.Mode().IsRegular() {
		if got, hashErr := prepareFileSHA256(destination); hashErr == nil && got == digest {
			return true, destination, digest, nil
		}
	}
	if err := prepareCopyFile(source, destination, 0o755); err != nil {
		return false, "", "", err
	}
	return false, destination, digest, nil
}

func stagePrepareSecrets(sourceDir, outputDir string) error {
	if info, err := os.Stat(sourceDir); err != nil || !info.IsDir() {
		if err == nil {
			err = errors.New("not a directory")
		}
		return fmt.Errorf("secrets directory: %w", err)
	}
	if err := validateFleetAgePair(filepath.Join(sourceDir, "fleet.age"), filepath.Join(sourceDir, "fleet.age.pub")); err != nil {
		return fmt.Errorf("fleet seal identity: %w", err)
	}
	required := []struct {
		name string
		mode os.FileMode
	}{
		{name: "fleet.age", mode: 0o400},
		{name: "fleet.age.pub", mode: 0o444},
		{name: "compute-ssh-key", mode: 0o600},
		{name: "compute-db.env", mode: 0o600},
		{name: "storage.env", mode: 0o600},
		{name: "sign.key", mode: 0o600},
		{name: "sign-pub.pem", mode: 0o644},
	}
	for _, item := range required {
		source := filepath.Join(sourceDir, item.name)
		body, err := os.ReadFile(source)
		if err != nil {
			return fmt.Errorf("read secret %s: %w", item.name, err)
		}
		if len(bytes.TrimSpace(body)) == 0 {
			return fmt.Errorf("secret %s is empty", item.name)
		}
		if err := prepareCopyFile(source, filepath.Join(outputDir, item.name), item.mode); err != nil {
			return fmt.Errorf("stage secret %s: %w", item.name, err)
		}
	}
	if err := hasComputeDatabaseEnv(filepath.Join(sourceDir, "compute-db.env")); !err {
		return errors.New("compute-db.env must contain non-empty DATABASE_URL and FAAS_VMMD_DBURL entries")
	}
	if err := validateSharedStorageEnv(filepath.Join(sourceDir, "storage.env")); err != nil {
		return fmt.Errorf("storage.env: %w", err)
	}

	pkiSource := filepath.Join(sourceDir, "pki")
	if _, err := os.Stat(filepath.Join(pkiSource, "ca", "ca.crt")); err != nil {
		return fmt.Errorf("pki/ca/ca.crt: %w", err)
	}
	if _, err := os.Stat(filepath.Join(pkiSource, "ca", "ca.key")); err == nil {
		return errors.New("secrets-dir pki/ must be a trust bundle; refusing to copy ca/ca.key into prepared artifacts")
	}
	if err := copyPrepareTree(pkiSource, filepath.Join(outputDir, "pki")); err != nil {
		return fmt.Errorf("stage pki: %w", err)
	}

	optional := []struct {
		name string
		mode os.FileMode
	}{
		{name: "ansible-vars.yml", mode: 0o600},
		{name: "box-age-key", mode: 0o600},
		{name: "rclone.conf.age", mode: 0o600},
		{name: "archive-creds.json.age", mode: 0o600},
	}
	for _, item := range optional {
		source := filepath.Join(sourceDir, item.name)
		if _, err := os.Stat(source); errors.Is(err, os.ErrNotExist) {
			continue
		} else if err != nil {
			return fmt.Errorf("inspect optional secret %s: %w", item.name, err)
		}
		if err := prepareCopyFile(source, filepath.Join(outputDir, item.name), item.mode); err != nil {
			return fmt.Errorf("stage optional secret %s: %w", item.name, err)
		}
	}
	if (fileExists(filepath.Join(sourceDir, "rclone.conf.age")) || fileExists(filepath.Join(sourceDir, "archive-creds.json.age"))) &&
		!fileExists(filepath.Join(sourceDir, "box-age-key")) {
		return errors.New("backup envelopes require box-age-key")
	}
	return nil
}

func copyPrepareTree(sourceDir, destinationDir string) error {
	return filepath.Walk(sourceDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("symlink %s is not allowed", path)
		}
		rel, err := filepath.Rel(sourceDir, path)
		if err != nil {
			return err
		}
		destination := filepath.Join(destinationDir, rel)
		if info.IsDir() {
			return os.MkdirAll(destination, 0o700)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("%s is not a regular file", path)
		}
		// A trust bundle is portable input, not an executable tree. Preserve
		// readability for the owner/group/world but never carry write bits from
		// a permissive source directory into the prepared artifact.
		mode := info.Mode().Perm() & 0o444
		if mode == 0 {
			mode = 0o400
		}
		return prepareCopyFile(path, destination, mode)
	})
}

func prepareCopyFile(source, destination string, mode os.FileMode) error {
	info, err := os.Lstat(source)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%s is not a regular file", source)
	}
	body, err := os.ReadFile(source)
	if err != nil {
		return err
	}
	return writePrepareFile(destination, body, mode)
}

func writePrepareFile(destination string, body []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(destination), ".prepare-*")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	defer func() { _ = os.Remove(tmpPath) }()
	if err := tmp.Chmod(mode); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(body); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpPath, destination)
}

func prepareFileSHA256(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return sha256HexString(body), nil
}

func sha256HexString(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\\''") + "'"
}
