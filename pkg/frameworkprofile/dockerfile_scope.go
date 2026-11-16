package frameworkprofile

import (
	"path"
	"sort"
	"strings"

	"github.com/onebox-faas/faas/pkg/dockerfilebase"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// DockerfileRegistryScope lists the image repositories the Dockerfiles in a
// source archive pull (ADR-20261110210000 slice 3j-4): every file named like
// a Dockerfile below sourceRoot, and each explicit path (a gregale.yaml or
// Compose selection), parsed with args. It is a superset of what any one
// build of the source uses, so the deployment's builds pull only
// repositories its own source names, whichever Dockerfile a build selects.
// found is false when the archive holds no Dockerfile.
func DockerfileRegistryScope(archivePath, sourceRoot string, explicit []string, args map[string]string) (result dockerfilebase.Result, found bool, err error) {
	logicalRoot, err := tarball.ResolveSourceRoot(archivePath, sourceRoot)
	if err != nil {
		return dockerfilebase.Result{}, false, err
	}
	files, _, err := readProfileFiles(archivePath, logicalRoot)
	if err != nil {
		return dockerfilebase.Result{}, false, err
	}
	wanted := map[string]bool{}
	for _, p := range explicit {
		if p = path.Clean(tarball.TrimLeadingDotSlash(p)); p != "." && !strings.HasPrefix(p, "../") {
			wanted[p] = true
		}
	}
	names := make([]string, 0, len(files))
	for name := range files {
		if wanted[name] || IsDockerfileName(path.Base(name)) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	results := make([]dockerfilebase.Result, 0, len(names))
	for _, name := range names {
		r, perr := dockerfilebase.Parse(files[name].Data, args)
		if perr != nil {
			return dockerfilebase.Result{}, true, perr
		}
		results = append(results, r)
	}
	if len(results) == 0 {
		return dockerfilebase.Result{}, false, nil
	}
	result, err = dockerfilebase.Merge(results...)
	return result, true, err
}

// IsDockerfileName reports whether a file's base name is a Dockerfile's:
// Dockerfile, Containerfile, Dockerfile.<x> or <x>.Dockerfile, in either
// case of the first letter.
func IsDockerfileName(base string) bool {
	lower := strings.ToLower(base)
	return lower == "dockerfile" || lower == "containerfile" ||
		strings.HasPrefix(lower, "dockerfile.") || strings.HasSuffix(lower, ".dockerfile") ||
		strings.HasPrefix(lower, "containerfile.") || strings.HasSuffix(lower, ".containerfile")
}
