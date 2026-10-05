package environmentgitops

import (
	"archive/tar"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/environmentsync"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// ReadGitDefinition reads one regular manifest from a GitHub codeload archive.
// No archive content is extracted onto the host. Both compressed and expanded
// streams are bounded, and the full gzip stream is verified before approval.
func ReadGitDefinition(reader io.Reader, manifestPath string, maxArchiveBytes int64) (environmentsync.DesiredState, error) {
	return readGitBuildDefinition(reader, manifestPath, maxArchiveBytes, nil)
}

// ReadGitBuildDefinition also verifies the exact build roots and Dockerfiles.
// A repository-root Dockerfile cannot substitute for a missing member file.
func ReadGitBuildDefinition(reader io.Reader, manifestPath string, maxArchiveBytes int64, sources []api.EnvironmentWorkloadSource) (environmentsync.DesiredState, error) {
	return readGitBuildDefinition(reader, manifestPath, maxArchiveBytes, sources)
}

func readGitBuildDefinition(reader io.Reader, manifestPath string, maxArchiveBytes int64, sources []api.EnvironmentWorkloadSource) (environmentsync.DesiredState, error) {
	if maxArchiveBytes <= 0 || manifestPath == "." || path.Clean(manifestPath) != manifestPath || tarball.EscapesRoot(manifestPath) || strings.ContainsAny(manifestPath, "\\\x00") {
		return environmentsync.DesiredState{}, fmt.Errorf("invalid Git definition path or archive limit")
	}
	compressed := &io.LimitedReader{R: reader, N: maxArchiveBytes + 1}
	gz, err := gzip.NewReader(compressed)
	if err != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("git source is not a gzip archive")
	}
	defer func() { _ = gz.Close() }()
	expanded := &io.LimitedReader{R: gz, N: api.EnvironmentGitOpsMaxExpandedArchiveBytes + 1}
	archive := tar.NewReader(expanded)
	root := ""
	var definition []byte
	seen := map[string]bool{}
	buildRoots, dockerfiles := make([]bool, len(sources)), make([]bool, len(sources))
	for count := 0; ; count++ {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return environmentsync.DesiredState{}, fmt.Errorf("invalid Git source archive")
		}
		if count >= api.SourceArchiveMaxEntries {
			return environmentsync.DesiredState{}, fmt.Errorf("git source has too many archive entries")
		}
		if tarball.EscapesRoot(header.Name) || strings.ContainsAny(header.Name, "\\\x00") {
			return environmentsync.DesiredState{}, fmt.Errorf("git archive path escapes its root")
		}
		name := path.Clean(header.Name)
		parts := strings.SplitN(name, "/", 2)
		if root == "" {
			root = parts[0]
		}
		if parts[0] != root || name == "." {
			return environmentsync.DesiredState{}, fmt.Errorf("git source has ambiguous archive roots")
		}
		if seen[name] {
			return environmentsync.DesiredState{}, fmt.Errorf("git source has duplicate archive paths")
		}
		seen[name] = true
		if len(parts) == 2 && header.Typeflag == tar.TypeReg {
			for i, source := range sources {
				buildRoots[i] = buildRoots[i] || source.Directory == "." || strings.HasPrefix(parts[1], source.Directory+"/")
				dockerfiles[i] = dockerfiles[i] || parts[1] == path.Join(source.Directory, source.Dockerfile)
			}
		}
		if len(parts) != 2 || parts[1] != manifestPath {
			continue
		}
		if header.Typeflag != tar.TypeReg || header.Size < 1 || header.Size > int64(api.EnvironmentGitOpsMaxDefinitionBytes) {
			return environmentsync.DesiredState{}, fmt.Errorf("environment manifest must be a bounded regular file")
		}
		definition, err = io.ReadAll(io.LimitReader(archive, int64(api.EnvironmentGitOpsMaxDefinitionBytes)+1))
		if err != nil || len(definition) != int(header.Size) {
			return environmentsync.DesiredState{}, fmt.Errorf("environment manifest is incomplete")
		}
	}
	// tar EOF can precede the gzip checksum or a transport failure. Drain the
	// bounded remainder so neither can turn a truncated response into authority.
	if _, err := io.Copy(gitArchivePadding{}, expanded); err != nil || expanded.N <= 0 || compressed.N <= 0 {
		return environmentsync.DesiredState{}, fmt.Errorf("git source is truncated or exceeds archive limits")
	}
	if definition == nil {
		return environmentsync.DesiredState{}, fmt.Errorf("environment manifest was not found")
	}
	for i, source := range sources {
		if !buildRoots[i] || (source.Dockerfile != "" && !dockerfiles[i]) {
			return environmentsync.DesiredState{}, fmt.Errorf("reviewed Git build root or Dockerfile was not found")
		}
	}
	parsed, err := gregalemanifest.ParseEnvironment(definition)
	if err != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("invalid environment manifest")
	}
	desired, err := environmentsync.Compile(parsed)
	if err != nil {
		return environmentsync.DesiredState{}, fmt.Errorf("invalid environment definition")
	}
	return desired, nil
}

// Only zero padding is valid after the tar end marker. A second tar stream or
// nonzero trailing payload would otherwise escape duplicate-path checks.
type gitArchivePadding struct{}

func (gitArchivePadding) Write(data []byte) (int, error) {
	for _, value := range data {
		if value != 0 {
			return 0, fmt.Errorf("unexpected trailing Git archive content")
		}
	}
	return len(data), nil
}
