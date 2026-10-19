package e2etest

import (
	"debug/buildinfo"
	"errors"
	"strings"

	"github.com/onebox-faas/faas/pkg/e2etest/servicestarter"
)

// ServiceGuestRuntimeManifest hashes the actual executed binaries and private
// image artifacts. TLS private keys and fixture credentials never enter it.
func ServiceGuestRuntimeManifest(paths map[string]string, revision string, complete bool) (servicestarter.RuntimeManifest, error) {
	c := servicestarter.SuiteContract()
	m := servicestarter.RuntimeManifest{Version: c.Version, SourceRevision: revision, Template: c.Template, Runtime: c.Runtime, Artifacts: map[string]string{}}
	allowed := map[string]bool{}
	for _, name := range c.Artifacts {
		allowed[name] = true
	}
	for name := range paths {
		if !allowed[name] {
			return m, errors.New("unexpected managed HTTPS artifact")
		}
	}
	for _, name := range c.Artifacts {
		deployed := strings.HasSuffix(name, "_app_layer") || strings.HasSuffix(name, "_definition") || strings.HasSuffix(name, "_request") || strings.HasSuffix(name, "_source_archive") || strings.HasSuffix(name, "_build_provenance")
		path := paths[name]
		if !complete && deployed && path == "" {
			continue
		}
		if path == "" {
			return m, errors.New("missing managed HTTPS artifact")
		}
		binary := !deployed && name != "kernel" && name != "builder_base" && name != "minimal_base" && name != "deploy_base" && name != "service_ca" && name != "firecracker" && name != "jailer"
		if binary {
			info, err := buildinfo.ReadFile(path)
			if err != nil || !postgresGuestBinaryRevision(info, revision) {
				return m, errors.New("managed HTTPS binary revision mismatch")
			}
		}
		digest, err := PostgresGuestArtifactDigest(path)
		if err != nil {
			return m, err
		}
		m.Artifacts[name] = digest
	}
	if complete && !m.Valid() {
		return m, errors.New("invalid managed HTTPS manifest")
	}
	return m, nil
}
