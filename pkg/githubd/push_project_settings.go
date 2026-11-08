package githubd

import (
	"context"
	"fmt"
	"io/fs"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/githubdgrpc"
	"github.com/onebox-faas/faas/pkg/gregalemanifest"
	"github.com/onebox-faas/faas/pkg/state"
)

// reportReviewedOnlyProjectSettings writes a failed GitHub check when a push
// changes the project's root gregale.yaml and it declares settings only a
// reviewed deploy applies: path routes, async routes and database bindings
// (ADR-20261108090000). A push applied none of them and said nothing, so a
// service it added came up without its route or database. Like a removal,
// they wait for `gregale deploy`, which shows them in its plan.
func (s *Service) reportReviewedOnlyProjectSettings(ctx context.Context, fsys fs.FS, changedFiles []string, filterMode string,
	installationID int64, repo, sha string, project state.Project, scope string) {
	if filterMode != filterModePaths || !slices.ContainsFunc(changedFiles, isRootProjectManifest) {
		return
	}
	raw, err := fs.ReadFile(fsys, "gregale.yaml")
	if err != nil {
		raw, err = fs.ReadFile(fsys, "gregale.yml")
	}
	if err != nil {
		return
	}
	m, err := gregalemanifest.ParseBytes(raw)
	if err != nil || m == nil {
		return
	}
	var settings []string
	if m.Routes != nil {
		settings = append(settings, "routes")
	}
	if m.AsyncRoutes != nil {
		settings = append(settings, "async_routes")
	}
	if len(m.Databases) > 0 {
		settings = append(settings, "databases")
	}
	if len(settings) == 0 {
		return
	}
	summary := fmt.Sprintf("This push changed gregale.yaml, which declares %s. A GitHub push does not apply these project settings; "+
		"run `gregale deploy` to review and apply them. The services in this push deploy without the change.", strings.Join(settings, ", "))
	s.Log.Info("githubd: push leaves project settings for a reviewed deploy", "project_id", project.ID, "settings", settings)
	if err := s.writeAppCheck(ctx, installationID, repo, sha, project.Slug+" (project settings)", scope, githubdgrpc.CheckPhaseFailed, summary); err != nil {
		s.Log.Warn("githubd: write project settings check", "project_id", project.ID, "err", err)
	}
}

func isRootProjectManifest(path string) bool {
	return path == "gregale.yaml" || path == "gregale.yml"
}
