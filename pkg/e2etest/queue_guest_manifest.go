package e2etest

import (
	"debug/buildinfo"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"

	"github.com/onebox-faas/faas/pkg/e2etest/queuestarter"
	"github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

// QueueGuestAppArtifacts binds the function layer and exact source to its build
// and immutable published Node base, rather than the minimal full-OCI base.
func QueueGuestAppArtifacts(root, source string, app state.App, dep state.Deployment, build state.Build, provenance state.BuildProvenance, release state.RuntimeRelease) (map[string]string, error) {
	image, imageErr := hex.DecodeString(strings.TrimPrefix(dep.ImageDigest, "sha256:"))
	if app.Type != state.AppTypeFunction || app.Runtime != "node22" || app.ID != dep.AppID ||
		dep.RootfsKey != sched.AppLayerKey(app.Slug, dep.ID) || !strings.HasPrefix(dep.ImageDigest, "sha256:") || imageErr != nil || len(image) != 32 ||
		release.Validate() != nil || release.Runtime != app.Runtime || release.Architecture != "amd64" ||
		build.Status != state.BuildSucceeded || build.DeploymentID != dep.ID || provenance.BuildID != build.ID || provenance.RuntimeBaseRef != release.SourceRef {
		return nil, errors.New("queue function build/runtime identity missing")
	}
	digest, err := PostgresGuestArtifactDigest(source)
	if err != nil || digest != dep.SourceSHA256 || digest != provenance.SourceSHA256 {
		return nil, errors.New("queue build did not use the exact source archive")
	}
	return map[string]string{"node22_base": filepath.Join(root, release.BaseKey()), "app_layer": dep.RootfsPath, "source_archive": source}, nil
}

func QueueGuestRuntimeManifest(paths map[string]string, revision string, complete bool) (queuestarter.RuntimeManifest, error) {
	c := queuestarter.SuiteContract()
	m := queuestarter.RuntimeManifest{Version: c.Version, SourceRevision: revision, Template: c.Template, Runtime: c.Runtime, Artifacts: map[string]string{}}
	allowed := map[string]bool{}
	for _, name := range c.Artifacts {
		allowed[name] = true
	}
	for name := range paths {
		if !allowed[name] {
			return m, errors.New("unexpected queue acceptance artifact")
		}
	}
	for _, name := range c.Artifacts {
		deployed := name == "node22_base" || name == "source_archive" || strings.HasSuffix(name, "_app_layer")
		path := paths[name]
		if !complete && deployed && path == "" {
			continue
		}
		if path == "" {
			return m, errors.New("missing queue acceptance artifact")
		}
		if !deployed && name != "kernel" && name != "builder_base" && name != "firecracker" && name != "jailer" {
			info, err := buildinfo.ReadFile(path)
			if err != nil || !postgresGuestBinaryRevision(info, revision) {
				return m, errors.New("queue acceptance binary revision mismatch")
			}
		}
		digest, err := PostgresGuestArtifactDigest(path)
		if err != nil {
			return m, err
		}
		m.Artifacts[name] = digest
	}
	if complete && !m.Valid() {
		return m, errors.New("invalid queue acceptance manifest")
	}
	return m, nil
}
