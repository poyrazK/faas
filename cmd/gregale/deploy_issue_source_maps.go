package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/issues"
)

// A deployment may start building before publication finishes. Keep production
// traffic at zero and do not enable exporters or promote on the caller's behalf.
func validateDeployIssueSourceMaps(explicit map[string]bool, manifest, directory string, zeroTraffic, unsupported bool) error {
	if !explicit["issue-source-maps"] && !explicit["issue-source-map-directory"] {
		return nil
	}
	if manifest == "" || directory == "" {
		return errors.New("--issue-source-maps and --issue-source-map-directory must both be supplied")
	}
	if !zeroTraffic {
		return errors.New("source-map publication requires --no-traffic or --traffic-percent=0; promote only after artifacts are ready")
	}
	if unsupported {
		return errors.New("source-map publication requires a single local-source deployment; image, repo, template, project, preview, create-only, developer-sync and safe/canary modes are unsupported")
	}
	if !filepath.IsLocal(manifest) || filepath.ToSlash(filepath.Clean(manifest)) != manifest || strings.ContainsAny(manifest, "\\:\x00") {
		return errors.New("--issue-source-maps must be a clean relative manifest path inside the selected source root")
	}
	if !path.IsAbs(directory) || path.Clean(directory) != directory || strings.HasPrefix(directory, "//") ||
		strings.ContainsAny(directory, "\\\x00") || len(directory) > api.IssueMaxFrameBytes || !utf8.ValidString(directory) {
		return errors.New("--issue-source-map-directory must be a clean absolute POSIX directory such as /app/dist")
	}
	return nil
}

// Read only the final, snapshotted upload, including packing exclusions and the
// selected HEAD/worktree/workspace root. The manifest lives beside the bundles
// relative to which its portable generatedFile entries were produced.
func prepareDeployIssueSourceMaps(archive, sourceRoot, manifest, directory string) (*issues.SourceMapSet, string, func(), error) {
	if sourceRoot != "" && (!filepath.IsLocal(sourceRoot) || strings.ContainsAny(sourceRoot, "\\:\x00")) {
		return nil, "", nil, errors.New("source-map build root must remain inside the upload archive")
	}
	snapshot, root, cleanup, err := materializeDeployArchive(archive)
	if err != nil {
		return nil, "", nil, err
	}
	set, err := loadDeployIssueSourceMaps(filepath.Join(root, filepath.FromSlash(sourceRoot)), manifest, directory)
	if err != nil {
		cleanup()
		return nil, "", nil, err
	}
	return set, snapshot, cleanup, nil
}

func loadDeployIssueSourceMaps(root, manifest, directory string) (*issues.SourceMapSet, error) {
	fs, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("open source root: %w", err)
	}
	defer func() { _ = fs.Close() }()
	file, err := fs.Open(filepath.FromSlash(manifest))
	if err != nil {
		return nil, fmt.Errorf("manifest must be included in the uploaded source: %w", err)
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, api.IssueSourceMapsUploadMaxBytes+1))
	if err != nil || len(data) > api.IssueSourceMapsUploadMaxBytes {
		return nil, errors.New("source-map manifest exceeds its byte bound or could not be read")
	}
	var in api.PublishIssueSourceMapsRequest
	in.Directory = directory
	if err := json.Unmarshal(data, &in.Manifest); err != nil {
		return nil, fmt.Errorf("decode source-map manifest: %w", err)
	}
	set, err := issues.CompileSourceMaps(in)
	if err != nil {
		return nil, fmt.Errorf("invalid source-map manifest: %w", err)
	}
	remaining := int64(api.IssueSourceMapBundlesMaxBytes)
	for _, artifact := range set.Request.Manifest.Artifacts {
		name := path.Join(path.Dir(manifest), artifact.GeneratedFile)
		size, err := verifyDeployIssueBundle(fs, name, artifact.SHA256, remaining)
		if err != nil {
			return nil, err
		}
		remaining -= size
	}
	return set, nil
}

func verifyDeployIssueBundle(root *os.Root, name, expected string, remaining int64) (int64, error) {
	file, err := root.Open(filepath.FromSlash(name))
	if err != nil {
		return 0, fmt.Errorf("source-map bundle %q must be included in the uploaded source: %w", name, err)
	}
	defer func() { _ = file.Close() }()
	maximum := min(remaining, int64(api.IssueSourceMapBundleMaxBytes))
	digest := sha256.New()
	size, err := io.Copy(digest, io.LimitReader(file, maximum+1))
	if err != nil {
		return 0, fmt.Errorf("read source-map bundle %q: %w", name, err)
	}
	if size > maximum {
		return 0, fmt.Errorf("source-map bundle %q exceeds its byte bound", name)
	}
	if hex.EncodeToString(digest.Sum(nil)) != expected {
		return 0, fmt.Errorf("source-map bundle %q digest mismatch; rebuild the manifest for the source being uploaded", name)
	}
	return size, nil
}

type deployIssueSourceMapsBindingError struct{}

func (*deployIssueSourceMapsBindingError) Error() string {
	return "deployment receipt did not match the verified source archive, build root and zero production traffic; inspect it before publishing artifacts"
}

func matchingDeployIssueSource(deployment api.DeploymentResponse, sourceSHA256, sourceRoot string) bool {
	return deploymentIDPattern.MatchString(deployment.ID) && deployment.TrafficPercent == 0 &&
		sourceSHA256 != "" && deployment.SourceSHA256 == sourceSHA256 && deployment.SourceRoot == sourceRoot
}

func publishDeployIssueSourceMaps(ctx context.Context, client *Client, slug string, dep api.DeploymentResponse, sourceSHA256, sourceRoot string, set *issues.SourceMapSet) (*api.IssueSourceMapsMetadata, error) {
	if set == nil {
		return nil, nil
	}
	if !matchingDeployIssueSource(dep, sourceSHA256, sourceRoot) {
		return nil, &deployIssueSourceMapsBindingError{}
	}
	// A replayed submission receipt can predate a later traffic promotion.
	// Recheck current deployment state rather than trusting that old snapshot.
	current, err := client.GetDeployment(ctx, dep.ID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", &deployIssueSourceMapsBindingError{}, err)
	}
	if !sameBindingDeployment(current.ID, dep.ID) || !matchingDeployIssueSource(current, sourceSHA256, sourceRoot) {
		return nil, &deployIssueSourceMapsBindingError{}
	}
	metadata, err := client.PublishIssueSourceMaps(ctx, slug, dep.ID, set.Request)
	if err != nil {
		var remote *APIError
		// A read can reconcile a lost PUT response. Definitive rejection,
		// conflicts and authorization errors remain errors; never mask them.
		if errors.As(err, &remote) && remote.Problem.Status < 500 && remote.Problem.Status != 408 {
			return nil, err
		}
		observed, readErr := client.GetIssueSourceMaps(ctx, slug, dep.ID)
		if readErr != nil || !matchingDeployIssueSourceMaps(observed, dep.ID, set) {
			return nil, err
		}
		metadata = observed
	}
	if !matchingDeployIssueSourceMaps(metadata, dep.ID, set) {
		return nil, errors.New("source-map readiness, deployment, count or manifest digest did not match the prepared build")
	}
	return &metadata, nil
}

func matchingDeployIssueSourceMaps(metadata api.IssueSourceMapsMetadata, deployment string, set *issues.SourceMapSet) bool {
	return sameBindingDeployment(metadata.DeploymentID, deployment) && metadata.Ready &&
		metadata.RetiredAt == nil && metadata.ManifestSHA256 == set.Digest && metadata.MapCount == len(set.Request.Manifest.Artifacts)
}

func printDeployIssueSourceMapsError(err error, slug, deployment string, set *issues.SourceMapSet) int {
	recovery := newDeployRecovery("source_maps", slug, deployment, "", 0, false)
	var binding *deployIssueSourceMapsBindingError
	if errors.As(err, &binding) {
		recovery.Stage, recovery.ResumeCommand = "source_maps_binding", ""
		recovery.Hint = "The deployment receipt did not confirm this source and zero production traffic. Inspect the deployment; do not publish artifacts or promote it until the discrepancy is resolved."
		return printErr("Source-map deployment binding failed", &deployRecoveryError{err: err, recovery: recovery})
	}
	recovery.InspectCommand = recoveryCLI() + " issues source-maps status --app " + recoveryArgument(slug) + " --deployment " + recoveryArgument(deployment)
	recovery.ResumeCommand = ""
	recovery.Hint = "The deployment was accepted, but source-map readiness was not confirmed. Keep production traffic at zero. Recover publication for this deployment; do not submit another deployment or promote until artifacts are ready."
	file, saveErr := saveDeployIssueSourceMapsRecovery(set)
	if saveErr == nil {
		recovery.ResumeCommand = recoveryCLI() + " issues source-maps publish --app " + recoveryArgument(slug) + " --deployment " + recoveryArgument(deployment) +
			" --directory " + recoveryArgument(set.Request.Directory) + " --manifest-file " + recoveryArgument(file)
		recovery.Hint += " The resume command uses a private sanitized manifest snapshot. Remove that file and its directory after recovery."
	} else {
		recovery.Hint += " The recovery manifest could not be saved; use the exact manifest from the uploaded build with the existing publish command."
	}
	return printErr("Source-map publication failed", &deployRecoveryError{err: err, recovery: recovery})
}

func saveDeployIssueSourceMapsRecovery(set *issues.SourceMapSet) (string, error) {
	dir, err := os.MkdirTemp("", "gregale-source-maps-recovery-")
	if err != nil {
		return "", err
	}
	file := filepath.Join(dir, "manifest.json")
	data, err := json.Marshal(set.Request.Manifest)
	if err == nil {
		err = os.WriteFile(file, data, 0o600)
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return "", err
	}
	return file, nil
}
