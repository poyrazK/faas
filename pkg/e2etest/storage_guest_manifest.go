package e2etest

import (
	"debug/buildinfo"
	"errors"

	"github.com/onebox-faas/faas/pkg/e2etest/storagestarter"
	"github.com/onebox-faas/faas/pkg/state"
)

// StorageGuestAppArtifacts binds a full-OCI starter's source and deployed layer
// to the real successful build, rather than expecting a function runtime release.
func StorageGuestAppArtifacts(template, storageRoot, sourceArchive string, app state.App, deployment state.Deployment, build state.Build, provenance state.BuildProvenance) (map[string]string, error) {
	starter, ok := storagestarter.StarterForTemplate(template)
	if !ok {
		return nil, errors.New("unsupported storage acceptance starter")
	}
	return guestAppArtifacts(starter.AppArtifact, starter.SourceArtifact, storageRoot, sourceArchive, app, deployment, build, provenance)
}

// StorageGuestRuntimeManifest verifies the native bundle before and after the
// journey. The deployed base, source and application layer may be absent before deployment.
func StorageGuestRuntimeManifest(paths map[string]string, revision, template string, complete bool) (storagestarter.RuntimeManifest, error) {
	contract := storagestarter.SuiteContract()
	starter, ok := storagestarter.StarterForTemplate(template)
	result := storagestarter.RuntimeManifest{Version: contract.Version, SourceRevision: revision, Template: template, Runtime: starter.Runtime, Artifacts: make(map[string]string)}
	if !ok {
		return result, errors.New("unsupported storage acceptance starter")
	}
	artifacts := append(contract.Artifacts, starter.AppArtifact, starter.SourceArtifact)
	allowed := make(map[string]bool, len(artifacts))
	for _, name := range artifacts {
		allowed[name] = true
	}
	for name := range paths {
		if !allowed[name] {
			return result, errors.New("unexpected storage acceptance artifact")
		}
	}
	for _, name := range artifacts {
		path := paths[name]
		if !complete && path == "" && (name == "deploy_base" || name == starter.AppArtifact || name == starter.SourceArtifact) {
			continue
		}
		if path == "" {
			return result, errors.New("missing storage acceptance artifact")
		}
		switch name {
		case "kernel", "builder_base", "deploy_base", starter.AppArtifact, starter.SourceArtifact, "firecracker", "jailer":
		default:
			info, err := buildinfo.ReadFile(path)
			if err != nil || !postgresGuestBinaryRevision(info, revision) {
				return result, errors.New("storage acceptance binary revision mismatch")
			}
		}
		digest, err := PostgresGuestArtifactDigest(path)
		if err != nil {
			return result, err
		}
		result.Artifacts[name] = digest
	}
	if complete && !result.Valid() {
		return result, errors.New("invalid storage acceptance manifest")
	}
	return result, nil
}
