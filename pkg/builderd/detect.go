package builderd

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/onebox-faas/faas/pkg/markers"
)

// Framework is the autodetected build pipeline (spec §4.5, §9).
// Aliased from pkg/markers (issue #736 / DEPLOY-PROV-2 / ADR-088)
// so existing callers (pkg/builderd/builderd.go, build_base.go)
// compile unchanged. The CLI also imports pkg/markers directly
// with a type alias to avoid pulling in pkg/builderd's
// transitive deps (DB, scheduler, firecracker).
type Framework = markers.Framework

const (
	FrameworkNode    = markers.FrameworkNode
	FrameworkPython  = markers.FrameworkPython
	FrameworkGo      = markers.FrameworkGo
	FrameworkDocker  = markers.FrameworkDocker
	FrameworkUnknown = markers.FrameworkUnknown
)

// Detector sniffs a source tarball to pick a Framework. The
// detection rule itself lives in pkg/markers (the single source
// of truth); this type is a thin shim so existing callers that
// inject a *Detector for testability continue to work. See
// ADR-088 for the design.
type Detector struct{}

// NewDetector returns a Detector.
func NewDetector() *Detector { return &Detector{} }

// Detect reads the tarball at path and returns its framework.
// Delegates to markers.DetectFromTarball. The parity contract
// pkg/markers.DetectFromTarball exposes is (FrameworkUnknown,
// nil) for missing markers; the builderd.Detect shim wraps
// that into an error so the existing build pipeline can record
// a user_error failure_class (see TestProcessOne_FrameworkDetectFailsFlipsDeployment
// and TestProcessOne_UnknownFrameworkFails below). See ADR-088.
func (d *Detector) Detect(path string) (Framework, error) {
	return d.DetectAtRoot(path, "")
}

// DetectAtRoot reads framework markers below sourceRoot while preserving the
// legacy archive-root behavior for an empty root. The selected root is
// validated by pkg/markers so malformed deployment metadata fails before a VM
// is spawned.
func (d *Detector) DetectAtRoot(path, sourceRoot string) (Framework, error) {
	fw, err := markers.DetectFromTarballAtRoot(path, sourceRoot)
	if err != nil {
		return fw, err
	}
	if fw == markers.FrameworkUnknown {
		if sourceRoot == "" || sourceRoot == "." {
			return fw, errors.New("detect: no package.json, requirements.txt, pyproject.toml, Pipfile, setup.py, go.mod, or Dockerfile found at tarball root")
		}
		return fw, fmt.Errorf("detect: no package.json, requirements.txt, pyproject.toml, Pipfile, setup.py, go.mod, or Dockerfile found below source root %q", sourceRoot)
	}
	return fw, nil
}

// DetectFromFS is the FS variant — used by tests that don't
// want to round-trip through a tarball. Mirrors Detect on the
// CLI side via cmd/gregale/pack.go.
func (d *Detector) DetectFromFS(fsys fs.FS) (Framework, error) {
	return markers.DetectFromFS(fsys)
}

// DetectWithVersion is Detect plus a best-effort version read.
// The version is "" when no version file is found or the parser
// fails; it is never an error condition. See ADR-087 for the
// per-parser priority order and the operator-only rationale
// (the build pipeline never reads the returned version).
//
// The unknown-framework case is reported as an error to preserve
// the original pkg/builderd.detch.Detect contract — the build
// pipeline at builderd.go:339 uses this to record a user_error
// failure_class (see TestProcessOne_FrameworkDetectFailsFlipsDeployment
// and TestProcessOne_UnknownFrameworkFails). ADR-088.
func (d *Detector) DetectWithVersion(path string) (Framework, string, error) {
	return d.DetectWithVersionAtRoot(path, "")
}

// DetectWithVersionAtRoot is DetectAtRoot plus the source-declared version
// lookup scoped to the same source root.
func (d *Detector) DetectWithVersionAtRoot(path, sourceRoot string) (Framework, string, error) {
	fw, err := d.DetectAtRoot(path, sourceRoot)
	if err != nil {
		return fw, "", err
	}
	return fw, markers.VersionFromTarballAtRoot(path, fw, sourceRoot), nil
}
