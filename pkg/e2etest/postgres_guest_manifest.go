package e2etest

import (
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"

	"github.com/onebox-faas/faas/pkg/managedpostgres/guestqualification"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// PostgresGuestAppArtifacts selects the full-OCI workloads' actual deployed
// layers and retained source archives, never function-runtime release bases.
func PostgresGuestAppArtifacts(workload, storageRoot, sourceArchive string, app state.App, deployment state.Deployment, build state.Build, provenance state.BuildProvenance) (map[string]string, error) {
	supported := false
	for _, candidate := range guestqualification.SuiteContract().Workloads {
		supported = supported || workload == candidate
	}
	if !supported {
		return nil, errors.New("unsupported PostgreSQL acceptance workload")
	}
	return guestAppArtifacts(workload+"_app_layer", workload+"_source_archive", storageRoot, sourceArchive, app, deployment, build, provenance)
}

func guestAppArtifacts(appArtifact, sourceArtifact, storageRoot, sourceArchive string, app state.App, deployment state.Deployment, build state.Build, provenance state.BuildProvenance) (map[string]string, error) {
	imageDigest, digestErr := hex.DecodeString(strings.TrimPrefix(deployment.ImageDigest, "sha256:"))
	if app.Type != state.AppTypeApp || app.Runtime != "" || app.ID != deployment.AppID ||
		deployment.RootfsKey != sched.AppLayerKey(app.Slug, deployment.ID) ||
		!strings.HasPrefix(deployment.ImageDigest, "sha256:") || digestErr != nil || len(imageDigest) != sha256.Size {
		return nil, errors.New("guest deployment OCI artifact identity missing")
	}
	if build.Status != state.BuildSucceeded || build.DeploymentID != deployment.ID || provenance.BuildID != build.ID {
		return nil, errors.New("guest source build provenance missing")
	}
	source, err := PostgresGuestArtifactDigest(sourceArchive)
	if err != nil || source != deployment.SourceSHA256 || source != provenance.SourceSHA256 {
		return nil, errors.New("guest build did not use the exact source archive")
	}
	return map[string]string{
		"deploy_base":  filepath.Join(storageRoot, sched.BaseKeyForArch("", "amd64")),
		appArtifact:    deployment.RootfsPath,
		sourceArtifact: sourceArchive,
	}, nil
}

// PostgresGuestRuntimeManifest fingerprints the files the isolated harness
// actually uses. Cached Go binaries must carry this clean checkout's revision.
func PostgresGuestRuntimeManifest(paths map[string]string, revision string, complete bool) (guestqualification.RuntimeManifest, error) {
	result := guestqualification.RuntimeManifest{Version: guestqualification.Version, SourceRevision: revision, Artifacts: make(map[string]string)}
	contract := guestqualification.SuiteContract()
	allowed := make(map[string]bool, len(contract.Artifacts))
	for _, name := range contract.Artifacts {
		allowed[name] = true
	}
	for name := range paths {
		if !allowed[name] {
			return result, errors.New("unexpected guest qualification artifact")
		}
	}
	for _, name := range contract.Artifacts {
		path := paths[name]
		deployed := name == "deploy_base" || strings.HasSuffix(name, "_app_layer") || strings.HasSuffix(name, "_source_archive")
		if !complete && path == "" && deployed {
			continue
		}
		if path == "" {
			return result, errors.New("missing guest qualification artifact")
		}
		if !deployed && name != "kernel" && name != "builder_base" && name != "firecracker" && name != "jailer" {
			info, err := buildinfo.ReadFile(path)
			if err != nil || !postgresGuestBinaryRevision(info, revision) {
				return result, errors.New("guest qualification binary revision mismatch")
			}
		}
		digest, err := PostgresGuestArtifactDigest(path)
		if err != nil {
			return result, err
		}
		result.Artifacts[name] = digest
	}
	if complete && !result.Valid() {
		return result, errors.New("guest qualification manifest invalid")
	}
	return result, nil
}

func PostgresGuestArtifactDigest(path string) (string, error) {
	pathInfo, err := os.Stat(path)
	if err != nil || !pathInfo.Mode().IsRegular() {
		return "", errors.New("guest qualification artifact invalid")
	}
	// Paths name the acceptance harness's own fixture files; only hashes escape.
	f, err := os.Open(path) //nolint:forbidigo
	if err != nil {
		return "", errors.New("guest qualification artifact unreadable")
	}
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() == 0 {
		_ = f.Close()
		return "", errors.New("guest qualification artifact invalid")
	}
	digest := sha256.New()
	_, err = io.Copy(digest, f)
	closeErr := f.Close()
	if err != nil || closeErr != nil {
		return "", errors.New("guest qualification artifact unreadable")
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func postgresGuestBinaryRevision(info *debug.BuildInfo, revision string) bool {
	if info == nil {
		return false
	}
	settings := make(map[string]string)
	for _, setting := range info.Settings {
		settings[setting.Key] = setting.Value
	}
	return settings["vcs"] == "git" && settings["vcs.revision"] == revision && settings["vcs.modified"] == "false" && settings["GOOS"] == "linux" && settings["GOARCH"] == "amd64"
}
