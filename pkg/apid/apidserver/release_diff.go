// adr: 20261021101117 — release diff.
package apidserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/releasediff"
	"github.com/onebox-faas/faas/pkg/state"
)

// releaseDiffDeploymentsMax bounds the deployments read to find a release's
// predecessor.
const releaseDiffDeploymentsMax = 500

// getReleaseDiff serves GET /v1/deployments/{id}/diff: what changed between
// a release and the one before it, or ?against= another release of the app.
func (s *server) getReleaseDiff(w http.ResponseWriter, r *http.Request, acct state.Account) {
	to, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	from, problem := s.releaseDiffBase(r.Context(), app, to, strings.TrimSpace(r.URL.Query().Get("against")))
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	writeJSON(w, http.StatusOK, s.computeReleaseDiff(r.Context(), app, from, to))
}

// releaseDiffBase is the release to compare with: against when given, else
// the predecessor; nil for an app's first release.
func (s *server) releaseDiffBase(ctx context.Context, app state.App, to state.Deployment, against string) (*state.Deployment, *api.Problem) {
	if against != "" {
		d, err := s.store.DeploymentByID(ctx, against)
		if err != nil || d.AppID != app.ID {
			return nil, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found", "no such deployment of this app")
		}
		if d.ID == to.ID {
			return nil, api.ErrValidation("compare a release with a different release")
		}
		return &d, nil
	}
	rows, err := s.store.ListDeploymentsForApp(ctx, app.ID, releaseDiffDeploymentsMax, 0)
	if err != nil {
		logCustomerFailure(s.log, "list deployments for release diff", err)
		return nil, api.ErrInternal("Gregale could not read this app's releases.").WithHint("Retry in a moment.")
	}
	if prev, ok := releasediff.Predecessor(rows, to); ok {
		return &prev, nil
	}
	return nil, nil
}

// computeReleaseDiff loads both releases' configuration and packages and
// compares them. Missing history makes a section unknown, never an error.
func (s *server) computeReleaseDiff(ctx context.Context, app state.App, from *state.Deployment, to state.Deployment) api.ReleaseDiff {
	in := releasediff.Inputs{App: app, From: from, To: to}
	if from != nil {
		in.FromConfig, in.ToConfig, in.ConfigNote = s.releaseDiffConfig(ctx, app, *from, to)
		fromPkgs, fromNote := s.releaseDiffPackages(ctx, *from)
		toPkgs, toNote := s.releaseDiffPackages(ctx, to)
		in.FromPkgs, in.ToPkgs = fromPkgs, toPkgs
		in.PkgsKnown = fromPkgs != nil && toPkgs != nil
		in.PkgsNote = firstNonEmpty(fromNote, toNote)
	}
	return releasediff.Compare(in)
}

// releaseDiffConfig finds the config revision each release started with.
func (s *server) releaseDiffConfig(ctx context.Context, app state.App, from, to state.Deployment) (*state.ConfigRevision, *state.ConfigRevision, string) {
	store, ok := s.store.(state.ConfigRevisionStore)
	if !ok {
		return nil, nil, "config history is unavailable"
	}
	scope := to.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	revisions, err := store.ListConfigRevisions(ctx, app.ID, scope, api.ConfigRevisionsRetained+1)
	if err != nil {
		logCustomerFailure(s.log, "list config revisions for release diff", err)
		return nil, nil, "config history could not be read"
	}
	fromRev, fromOK := releasediff.ConfigAt(revisions, from.CreatedAt)
	toRev, toOK := releasediff.ConfigAt(revisions, to.CreatedAt)
	if !fromOK || !toOK {
		return nil, nil, "the config history no longer reaches one of these releases"
	}
	return fromRev, toRev, ""
}

// releaseDiffPackages reads a release's build SBOM; nil with a note when the
// release has none.
func (s *server) releaseDiffPackages(ctx context.Context, d state.Deployment) ([]releasediff.Package, string) {
	if d.BuildID == "" {
		return nil, "image releases have no build SBOM"
	}
	prov, err := s.store.BuildProvenanceByBuildID(ctx, d.BuildID)
	if err != nil || prov.SBOMStorageKey == "" {
		return nil, "one of these builds has no SBOM"
	}
	key, ok := cleanSBOMKey(prov.SBOMStorageKey)
	if !ok {
		return nil, "one of these builds has no SBOM"
	}
	f, err := s.openBuildSBOM(ctx, key)
	if err != nil {
		return nil, "one of these builds' SBOM could not be read"
	}
	defer func() { _ = f.Close() }()
	pkgs, err := releasediff.ParseSBOM(f, api.ReleaseDiffSBOMMaxBytes)
	if err != nil {
		return nil, "one of these builds' SBOM could not be read"
	}
	if pkgs == nil {
		pkgs = []releasediff.Package{}
	}
	return pkgs, ""
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
