package apidserver

import (
	"fmt"
	"net/http"
	"path"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tarball"
)

// projectWorkloadReleaseCommand resolves the release command of one workload
// in a project deploy (ADR-20261019120007 amendment 1).
//
// reposcan already assigned a Compose completion dependency or the repository
// root Procfile's release: process (workload.ReleaseCommand). A single-workload
// project resolves its source declaration exactly as a single-app deploy.
//
// In a multi-workload project a service may also declare its own command in
// its own directory: gregale.yaml release.command, or a Procfile release:
// process outside the repository root. It used to be dropped without a word,
// so a project deployed only with `deploy --project` could never run its
// migration. It applies when no other workload builds from that directory;
// a shared directory would run the command once per service, so it is
// refused. It wins over a Compose completion step, as on every other deploy
// path of the service.
//
// app is the app as the staged archive sees it; workload.RootDir is
// repository-relative. narrowedRoot is the repository directory a Compose
// build context was staged from as the archive root, or "" when the archive
// holds the whole repository.
//
// A Procfile at the root of a narrowed archive is the service's own (agent
// eval 2026-11-24: a Compose worker's worker/Procfile release: never ran,
// because it was skipped as the repository root's). It applies only where
// nothing else already decided: when another service builds from the same
// context, or the service has a Compose completion step, the command stays
// what it was before that fix, so redeploying an existing project neither
// fails nor swaps its migration.
func projectWorkloadReleaseCommand(staged string, workloads []reposcan.Workload, workload reposcan.Workload, app state.App, narrowedRoot string, manifest *gregalemanifest.Manifest) (deploymentReleaseCommand, *api.Problem) {
	assigned := deploymentReleaseCommand{command: append([]string(nil), workload.ReleaseCommand...), shell: workload.ReleaseCommandShell}
	if len(workloads) == 1 {
		resolved, problem := resolveSourceReleaseDeclaration(staged, app, manifest)
		if problem != nil || len(resolved.command) == 0 {
			return assigned, problem
		}
		return resolved, nil
	}
	declared, origin, fromContextProcfile, problem := serviceOwnReleaseDeclaration(staged, workload, narrowedRoot, manifest)
	if problem != nil || declared == "" {
		return assigned, problem
	}
	dir := cleanWorkloadDir(workload.RootDir)
	if fromContextProcfile {
		dir = cleanWorkloadDir(narrowedRoot)
		if workload.ReleaseService != "" {
			return assigned, nil
		}
	}
	for _, other := range workloads {
		if other.Name != workload.Name && cleanWorkloadDir(other.RootDir) == dir {
			if fromContextProcfile {
				return assigned, nil
			}
			return deploymentReleaseCommand{}, projectReleaseDeclarationProblem(fmt.Sprintf(
				"%s declares a release command, but services %q and %q both build from that directory, so it would run once per service. "+
					"Move it into one service's own directory, or declare it once as a Compose service the others wait for with condition: service_completed_successfully.",
				origin, workload.Name, other.Name))
		}
	}
	// The service's own declaration wins over a Compose completion step, as
	// on every other deploy path (ADR-20261102192943).
	return validateDeploymentReleaseCommand(declared)
}

// serviceOwnReleaseDeclaration returns the release command a service declares
// in its own directory, where it came from, and whether it is the Procfile at
// the root of a narrowed archive. The repository root Procfile is reposcan's
// (it assigns that release: process to exactly one workload).
func serviceOwnReleaseDeclaration(staged string, workload reposcan.Workload, narrowedRoot string, manifest *gregalemanifest.Manifest) (string, string, bool, *api.Problem) {
	dir := cleanWorkloadDir(workload.RootDir)
	if manifest != nil && manifest.Release != nil && manifest.Release.Command != "" {
		return manifest.Release.Command, path.Join(dir, "gregale.yaml") + " release.command", false, nil
	}
	narrowed := cleanWorkloadDir(narrowedRoot) != "."
	if narrowed {
		// The archive root is the build context, so its Procfile is the
		// service's own. An empty app.RootDir alone does not say the
		// archive was narrowed (nightly 11-07), so the caller does.
		dir = cleanWorkloadDir(narrowedRoot)
	} else if dir == "." {
		return "", "", false, nil
	}
	// Otherwise only the service's own directory counts: the reader falls
	// back to the archive root, whose Procfile reposcan already assigned.
	body, found, err := readServiceOwnProcfile(staged, dir, narrowed)
	if err != nil {
		return "", "", false, api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	if !found {
		return "", "", false, nil
	}
	command, ok, err := reposcan.ParseProcfileReleaseCommand(body)
	if err != nil {
		return "", "", false, api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid Procfile", "release: "+err.Error())
	}
	if !ok {
		return "", "", false, nil
	}
	return command, path.Join(dir, "Procfile") + " release:", narrowed, nil
}

func cleanWorkloadDir(dir string) string {
	if dir == "" {
		return "."
	}
	return path.Clean(dir)
}

func projectReleaseDeclarationProblem(detail string) *api.Problem {
	return api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Ambiguous release command", detail)
}

// readServiceOwnProcfile reads dir/Procfile and ignores the archive-root
// Procfile the source-root lookup falls back to. In an archive narrowed to
// the service's own directory, the archive-root Procfile is dir/Procfile.
func readServiceOwnProcfile(staged, dir string, narrowed bool) ([]byte, bool, error) {
	if narrowed {
		body, _, found, err := readSourceRefArchiveFileBytes(staged, ".", []string{"Procfile"}, sourceRefManifestMaxBytes, "Procfile")
		return body, found && err == nil, err
	}
	body, name, found, err := readSourceRefArchiveFileBytes(staged, dir, []string{"Procfile"}, sourceRefManifestMaxBytes, "Procfile")
	if err != nil || !found {
		return nil, false, err
	}
	prefix, err := tarball.RootPrefix(staged)
	if err != nil {
		return nil, false, err
	}
	if path.Dir(name) == path.Clean(strings.Trim(prefix, "/")) || path.Dir(name) == "." {
		return nil, false, nil
	}
	return body, true, nil
}
