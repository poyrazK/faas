// handlers_url.go — per-deployment preview URL read seam
// (issue #976 / ADR-122 / SAFE-RELEASES-C.2).
//
// GET /v1/deployments/{id}/url returns the resolved preview URL the
// edge will mint under for a single deployment. The endpoint is the
// read companion to the CREATE path's stamping and the cert allowlist's
// deployment-preview branch (pkg/gateway/allowlist.go).
//
// Wire shape: api.DeploymentPreviewURL (pkg/api/dto.go).
//   - Host / URL: derived from {deploy-{N}.{slug}.gregale.dev} via
//     pkg/gateway.BuildDeploymentPreviewURL. N is the per-app ordinal
//     from state.PgStore.DeploymentOrdinal — the (created_at, id)
//     ordering is stable even after later deploys land.
//   - Alive: hoisted from state.Deployment.DeploymentPreviewActive so
//     dashboards/CLI flip a single boolean instead of round-tripping
//     the allowlist.
//
// IDOR posture (mirrors getDeployment at handlers_ext.go:1136):
// a deployment that doesn't exist OR belongs to a different account
// returns 404. Cross-account probes never learn whether the row exists.
//
// 404 semantics:
//   - Missing deployment row → 404 "no such deployment"
//   - Cross-account row → 404 "no such deployment" (same envelope;
//     we never reveal cross-account existence)
//   - DeploymentPreviewActive() == false → 200 with Alive=false and
//     Host="". NOT a 404 — the dashboard renders a "preview has ended"
//     chip rather than treating the row as gone.
//
// Refusal semantics when DeployWildcardSuffix is "":
// The deployment-preview zone is disabled on this platform (e.g.
// staging paths that don't mint deployment-preview certs). The
// handler returns 200 with Alive=false and Host="" so the wire
// shape stays stable across environments.

package main

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/wire"
)

func (s *server) getDeploymentURL(w http.ResponseWriter, r *http.Request, acct state.Account) {
	id := r.PathValue("id")
	d, err := s.store.DeploymentByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, state.ErrNotFound) {
			s.notFound(w, "no such deployment")
			return
		}
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("load deployment: %v", err)))
		return
	}
	app, err := s.store.AppByID(r.Context(), d.AppID)
	if err != nil || app.AccountID != acct.ID {
		// Same posture as getDeployment: cross-account probes get
		// 404, never 403 — we never reveal whether the row exists
		// in another account.
		s.notFound(w, "no such deployment")
		return
	}
	alive := d.DeploymentPreviewActive()
	resp := api.DeploymentPreviewURL{
		DeploymentID: d.ID,
		AppID:        d.AppID,
		Alive:        alive,
	}
	if !alive {
		// The deployment row exists and belongs to the caller, but
		// its status is failed/superseded/zero — the preview URL
		// has effectively ended. The 200 envelope carries Alive=false
		// so dashboards render the closed-state copy without
		// round-tripping again. Host/URL stay empty.
		writeJSON(w, http.StatusOK, resp)
		return
	}
	suffix := wire.DeployWildcardSuffix
	if suffix == "" {
		// Deployment-preview zone disabled on this platform. The
		// 200 envelope carries Alive=false so the dashboard shows
		// the "preview zone not enabled on this build" chip.
		resp.Alive = false
		writeJSON(w, http.StatusOK, resp)
		return
	}
	ord, err := s.store.DeploymentOrdinal(r.Context(), app.ID, d.ID)
	if err != nil {
		api.WriteProblem(w, api.ErrInternal(
			fmt.Sprintf("resolve deployment ordinal: %v", err)))
		return
	}
	host := gateway.BuildDeploymentPreviewURL(suffix, ord, app.Slug)
	if host == "" {
		// URL build refused the (ordinal, slug) pair — indicates
		// malformed app.Slug, not a runtime error. 422 since the
		// bug is on our side (row+app passed the parser, but the
		// URL stamper refused the inputs). Surface the bug to the
		// dashboard as a 5xx-equivalent so the customer can
		// escalate via the live incident channel.
		api.WriteProblem(w, api.NewProblem(http.StatusInternalServerError,
			api.CodeInternal,
			"deployment preview URL build failed",
			fmt.Sprintf("app.Slug=%q ordinal=%d; BuildDeploymentPreviewURL refused the pair", app.Slug, ord)))
		return
	}
	resp.Host = host
	resp.URL = wire.DeployPreviewURIScheme + "://" + host
	writeJSON(w, http.StatusOK, resp)
}
