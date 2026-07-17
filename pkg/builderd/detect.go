package builderd

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
)

// Framework is the autodetected build pipeline (spec §4.5, §9). The builder VM
// runs Railpack internally; this enum names which language profile Railpack
// should target. M6 v1 ships Node and Python — these are the two cases the §14
// acceptance gate exercises (bare Node and bare Python repos, no config).
type Framework string

const (
	FrameworkNode    Framework = "node"
	FrameworkPython  Framework = "python"
	FrameworkDocker  Framework = "docker" // tarball contains a Dockerfile at the root
	FrameworkUnknown Framework = "unknown"
)

// Detector sniffs a source tarball to pick a Framework. The detection rule
// is deliberately dumb and stable: it inspects the top-level entries of the
// tarball and picks one framework. A Dockerfile at the root wins over the
// language markers (matches `faas deploy --dockerfile`).
type Detector struct{}

// NewDetector returns a Detector.
func NewDetector() *Detector { return &Detector{} }

// Detect reads the tarball at path and returns its framework. Errors are
// best-effort: an unreadable tarball returns FrameworkUnknown + error so the
// caller can record a user_error failure_class.
func (d *Detector) Detect(path string) (Framework, error) {
	f, err := os.Open(path)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("detect: open: %w", err)
	}
	defer func() { _ = f.Close() }()

	gz, err := gzip.NewReader(f)
	if err != nil {
		return FrameworkUnknown, fmt.Errorf("detect: gzip: %w", err)
	}
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	hasDocker := false
	hasNode := false
	hasPython := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return FrameworkUnknown, fmt.Errorf("detect: read tar: %w", err)
		}
		// Only sniff the top-level entries — `faas deploy` always sends a
		// tarball whose root is the project (verified by apid's
		// validateTarballShape).
		if strings.Contains(hdr.Name, "/") {
			continue
		}
		switch strings.ToLower(hdr.Name) {
		case "dockerfile":
			hasDocker = true
		case "package.json":
			hasNode = true
		case "requirements.txt", "pyproject.toml", "pipfile", "setup.py":
			hasPython = true
		}
	}
	switch {
	case hasDocker:
		return FrameworkDocker, nil
	case hasNode:
		return FrameworkNode, nil
	case hasPython:
		return FrameworkPython, nil
	}
	return FrameworkUnknown, errors.New("detect: no package.json, requirements.txt, or Dockerfile found at tarball root")
}
