// adr: 20261022120744 — the release page and its timeline.
package apidserver

import (
	"context"
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/releasediff"
	"github.com/onebox-faas/faas/pkg/apid/releasetimeline"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// getReleaseTimeline serves GET /v1/deployments/{id}/timeline.
func (s *server) getReleaseTimeline(w http.ResponseWriter, r *http.Request, acct state.Account) {
	d, app, ok := s.loadCanaryDeployment(w, r, acct)
	if !ok {
		return
	}
	timeline, err := s.releaseTimeline(r.Context(), app, d)
	if err != nil {
		writeCustomerInternalProblem(w, r, s.log, "read release timeline",
			"Gregale could not read this release's history.", "Retry in a moment.", err)
		return
	}
	writeJSON(w, http.StatusOK, timeline)
}

// releaseTimeline reads what the timeline is built from. The audit history
// is required; approvals and the app's other releases only add detail.
func (s *server) releaseTimeline(ctx context.Context, app state.App, d state.Deployment) (api.ReleaseTimeline, error) {
	rows, err := s.store.ListDeploymentAudit(ctx, d.ID, api.ReleaseTimelineAuditRowsMax+1)
	if err != nil {
		return api.ReleaseTimeline{}, err
	}
	in := releasetimeline.Inputs{Deployment: s.withRolloutHold(ctx, s.deploymentResponse(ctx, d, app)), App: app.Slug,
		AutoRollbackReason: d.LastAutoRollbackReason, Watched: d.RollbackOn5xx, WatchWindow: api.PostReleaseWatchWindow,
		AuditTruncated: len(rows) > api.ReleaseTimelineAuditRowsMax, Actor: s.releaseActorNamer(ctx), Now: timeNow()}
	if in.AuditTruncated {
		rows = rows[:api.ReleaseTimelineAuditRowsMax]
	}
	for _, row := range rows {
		in.Audit = append(in.Audit, releasetimeline.AuditRow{ID: row.ID, At: row.At, Kind: string(row.Kind), Actor: row.Actor, Data: row.Data})
	}
	if releases, err := s.store.ListDeploymentsForApp(ctx, app.ID, releaseDiffDeploymentsMax, 0); err != nil {
		logCustomerFailure(s.log, "list deployments for release timeline", err)
	} else {
		in.Stable, in.ReplacedBy = stableRelease(releases, d), replacingRelease(releases, d)
	}
	if _, status, err := s.releaseApprovalState(ctx, app, d); err != nil {
		logCustomerFailure(s.log, "read release approval for release timeline", err)
	} else {
		in.Approval = &status
	}
	return releasetimeline.Build(in), nil
}

// stableRelease is the release a canary in flight returns traffic to on
// abort: the newest earlier live release of its scope that still serves.
// Otherwise it is the release this one replaced.
func stableRelease(releases []state.Deployment, d state.Deployment) *releasetimeline.Release {
	var best *state.Deployment
	for i, other := range releases {
		if other.ID == d.ID || deploymentScope(other.Scope) != deploymentScope(d.Scope) || !other.CreatedAt.Before(d.CreatedAt) ||
			other.Status != state.DeployLive || other.TrafficPercent <= 0 {
			continue
		}
		if best == nil || other.CreatedAt.After(best.CreatedAt) {
			best = &releases[i]
		}
	}
	if best == nil {
		prev, ok := releasediff.Predecessor(releases, d)
		if !ok {
			return nil
		}
		best = &prev
	}
	return &releasetimeline.Release{ID: best.ID, Revision: best.Revision}
}

// replacingRelease is the oldest later release of the same scope that took
// over, for a release that no longer serves.
func replacingRelease(releases []state.Deployment, d state.Deployment) *releasetimeline.Release {
	if d.Status != state.DeploySuperseded {
		return nil
	}
	var best *state.Deployment
	for i, other := range releases {
		if other.ID == d.ID || deploymentScope(other.Scope) != deploymentScope(d.Scope) || !other.CreatedAt.After(d.CreatedAt) ||
			other.RolloutAbortedAt != nil || (other.Status != state.DeployLive && other.Status != state.DeploySuperseded) {
			continue
		}
		if best == nil || other.CreatedAt.Before(best.CreatedAt) {
			best = &releases[i]
		}
	}
	if best == nil {
		return nil
	}
	at := best.CreatedAt
	if best.RolloutCompletedAt != nil {
		at = *best.RolloutCompletedAt
	}
	return &releasetimeline.Release{ID: best.ID, Revision: best.Revision, At: at}
}

func deploymentScope(scope string) string {
	if scope == "" {
		return api.DefaultEnvScope
	}
	return scope
}

// releaseActorNamer names audit actors for people: an account's email, or
// "Gregale" for apid's and meterd's automatic steps.
func (s *server) releaseActorNamer(ctx context.Context) func(string) string {
	emails := map[string]string{}
	return func(actor string) string {
		component, id, found := strings.Cut(actor, ":")
		switch {
		case found && (component == "account" || component == "user"):
			if email, ok := emails[id]; ok {
				return email
			}
			email := "a teammate"
			if acct, err := s.store.AccountByID(ctx, id); err == nil && acct.Email != "" {
				email = acct.Email
			}
			emails[id] = email
			return email
		case found && (component == "apid" || component == "meterd" || component == "schedd" || component == "imaged"):
			return "Gregale"
		case actor == "" || actor == "system":
			return "Gregale"
		}
		return actor
	}
}

// renderReleasePage serves GET /dashboard/releases/{id}. Like the approval
// page it renders no release data: its script reads the timeline and the
// diff with the signed-in session, so the API's access checks decide what
// the person may see and do.
func (s *server) renderReleasePage(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	id, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Referrer-Policy", "no-referrer")
	page := dashboard.Page{Title: "Release", Body: "release", Account: dashboardAccountView(acct, 0),
		Data: dashboard.ReleasePageData{DeploymentID: id.String()}}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}
