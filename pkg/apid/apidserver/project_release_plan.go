package apidserver

import (
	"io/fs"
	"os"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
)

// planWorkloadReleaseCommand is the release command a project deploy runs for
// workload and where it is declared, resolved from the scan directory with
// the rules projectWorkloadReleaseCommand applies to the staged source. The
// dry run used to show only the command the scan assigned (a Compose
// completion step or the repository root Procfile), so a service's own
// gregale.yaml release.command or Procfile release: appeared first when the
// apply ran it (agent eval 2026-11-24, voting app). note is non-empty when
// the apply will refuse the service's declaration.
func (s *server) planWorkloadReleaseCommand(scanDir string, workloads []reposcan.Workload, workload reposcan.Workload) (command, source, note string) {
	command, source, note = s.resolvePlanWorkloadReleaseCommand(scanDir, workloads, workload)
	// The apply refuses any release command while this installation cannot
	// run them; say so in the plan rather than after the build.
	if s.releasePhaseOff && command != "" && note == "" {
		note = workload.Name + ": " + releasePhaseOffProblem().Detail
	}
	return command, source, note
}

func (s *server) resolvePlanWorkloadReleaseCommand(scanDir string, workloads []reposcan.Workload, workload reposcan.Workload) (command, source, note string) {
	command, source = reposcan.PlanReleaseCommand(workload)
	if scanDir == "" {
		return command, source, ""
	}
	fsys := os.DirFS(scanDir)
	// The apply stages a Compose build context on its own; its manifest and
	// Procfile are then read from that directory only. A release-phase-off
	// installation ignores the context's Procfile, as the apply does.
	var buildContext composeReleaseContext
	narrowed := false
	roots := []string{cleanWorkloadDir(workload.RootDir), "."}
	app := state.App{RootDir: workload.RootDir}
	app.Manifest.BuildDockerfile = workload.Dockerfile
	app.Manifest.ProjectImage = workload.Image
	if composeDockerBuildContext(scanDir, workload, app) {
		roots = []string{cleanWorkloadDir(workload.RootDir)}
		narrowed = true
		if !s.releasePhaseOff {
			buildContext = composeReleaseContext{root: workload.RootDir}
		}
	}
	manifestCommand, manifestName := planReleaseManifestCommand(fsys, roots)
	if len(workloads) == 1 {
		if manifestCommand != "" {
			return manifestCommand, manifestName + " release.command", ""
		}
		if body, name, found := planReadFirst(fsys, roots, []string{"Procfile"}); found {
			if declared, ok, err := reposcan.ParseProcfileReleaseCommand(body); err == nil && ok {
				return declared, name + " release:", ""
			}
		}
		return command, source, ""
	}
	var manifest *gregalemanifest.Manifest
	if manifestCommand != "" {
		manifest = &gregalemanifest.Manifest{Release: &gregalemanifest.ReleaseConfig{Command: manifestCommand}}
	}
	readProcfile := dirProcfileReader(fsys)
	if narrowed && s.releasePhaseOff {
		// The apply stages the context alone and looks for dir/Procfile
		// inside it, which finds none.
		readProcfile = func(string, bool) ([]byte, bool, error) { return nil, false, nil }
	}
	declared, origin, dir, problem := serviceOwnReleaseDeclaration(readProcfile, workloads, workload, buildContext, manifest)
	if problem != nil {
		return command, source, workload.Name + ": " + problem.Detail
	}
	if declared == "" {
		return command, source, ""
	}
	if manifest != nil && declared == manifestCommand {
		// Name the file actually read (gregale.toml, or a manifest the
		// lookup found above the service's directory).
		origin = manifestName + " release.command"
	}
	if detail := sharedReleaseDirDetail(workloads, workload, dir, origin); detail != "" {
		return command, source, detail
	}
	return declared, origin, ""
}

// dirProcfileReader reads dir/Procfile from the scan directory, which always
// holds the whole repository; the repository root's Procfile is the scan's.
func dirProcfileReader(fsys fs.FS) releaseProcfileReader {
	return func(dir string, _ bool) ([]byte, bool, error) {
		if cleanWorkloadDir(dir) == "." {
			return nil, false, nil
		}
		body, _, found := planReadFirst(fsys, []string{dir}, []string{"Procfile"})
		return body, found, nil
	}
}

// planReleaseManifestCommand returns the release.command of the first
// gregale manifest in roots, in the apply's lookup order. A manifest that
// does not parse shows nothing here; the apply reports it.
func planReleaseManifestCommand(fsys fs.FS, roots []string) (string, string) {
	body, name, found := planReadFirst(fsys, roots, []string{"gregale.yaml", "gregale.yml", "gregale.toml"})
	if !found {
		return "", ""
	}
	parse := gregalemanifest.ParseBytes
	if strings.HasSuffix(name, ".toml") {
		parse = gregalemanifest.ParseTOMLBytes
	}
	m, err := parse(body)
	if err != nil || m == nil || m.Release == nil {
		return "", ""
	}
	return m.Release.Command, name
}

// planReadFirst reads the first of names found under roots, root by root.
func planReadFirst(fsys fs.FS, roots, names []string) ([]byte, string, bool) {
	seen := map[string]bool{}
	for _, root := range roots {
		root = cleanWorkloadDir(root)
		if seen[root] {
			continue
		}
		seen[root] = true
		for _, name := range names {
			file := path.Join(root, name)
			if !fs.ValidPath(file) {
				continue
			}
			info, err := fs.Stat(fsys, file)
			if err != nil || !info.Mode().IsRegular() || info.Size() > api.SourceManifestMaxBytes {
				continue
			}
			body, err := fs.ReadFile(fsys, file)
			if err == nil {
				return body, file, true
			}
		}
	}
	return nil, "", false
}
