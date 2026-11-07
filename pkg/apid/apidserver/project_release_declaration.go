package apidserver

import (
	"fmt"
	"net/http"
	"path"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/reposcan"
	"github.com/onebox-faas/faas/pkg/state"
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
// app is the app as the staged archive sees it (a Compose build context is
// staged as the archive root); workload.RootDir is repository-relative.
func projectWorkloadReleaseCommand(staged string, workloads []reposcan.Workload, workload reposcan.Workload, app state.App, manifest *gregalemanifest.Manifest) (deploymentReleaseCommand, *api.Problem) {
	assigned := deploymentReleaseCommand{command: append([]string(nil), workload.ReleaseCommand...), shell: workload.ReleaseCommandShell}
	if len(workloads) == 1 {
		resolved, problem := resolveSourceReleaseDeclaration(staged, app, manifest)
		if problem != nil || len(resolved.command) == 0 {
			return assigned, problem
		}
		return resolved, nil
	}
	declared, origin, problem := serviceOwnReleaseDeclaration(staged, workload, app, manifest)
	if problem != nil || declared == "" {
		return assigned, problem
	}
	dir := cleanWorkloadDir(workload.RootDir)
	for _, other := range workloads {
		if other.Name != workload.Name && cleanWorkloadDir(other.RootDir) == dir {
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
// in its own directory and where it came from. The repository root Procfile
// is reposcan's (it assigns that release: process to exactly one workload).
func serviceOwnReleaseDeclaration(staged string, workload reposcan.Workload, app state.App, manifest *gregalemanifest.Manifest) (string, string, *api.Problem) {
	dir := cleanWorkloadDir(workload.RootDir)
	if manifest != nil && manifest.Release != nil && manifest.Release.Command != "" {
		return manifest.Release.Command, path.Join(dir, "gregale.yaml") + " release.command", nil
	}
	if dir == "." {
		return "", "", nil
	}
	// The service's own directory, as the origin below names it: an app
	// without a RootDir would otherwise read the root Procfile reposcan owns.
	body, found, err := readSourceRefProcfileBytes(staged, dir)
	if err != nil {
		return "", "", api.NewProblem(http.StatusBadRequest, api.CodeSourceInvalid, "Bad source", err.Error())
	}
	if !found {
		return "", "", nil
	}
	command, ok, err := reposcan.ParseProcfileReleaseCommand(body)
	if err != nil {
		return "", "", api.NewProblem(http.StatusUnprocessableEntity, CodeAppManifestInvalid, "Invalid Procfile", "release: "+err.Error())
	}
	if !ok {
		return "", "", nil
	}
	return command, path.Join(dir, "Procfile") + " release:", nil
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
