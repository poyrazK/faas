// adr: 20261019071733 amendment 1 — the dashboard shows an app's release
// guards: freezes, gates, approvals and scheduled releases.
package apidserver

import (
	"context"
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/approval"
	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

// renderReleaseSafetyDashboard serves GET
// /dashboard/apps/{slug}/release-safety. It reads; every change stays on the
// CLI and API, which the page names.
func (s *server) renderReleaseSafetyDashboard(w http.ResponseWriter, r *http.Request) {
	acct, ok := AccountFrom(r.Context())
	if !ok {
		writeDashboardUnauthorized(w, r)
		return
	}
	app, err := s.store.AppBySlug(r.Context(), r.PathValue("slug"))
	if err != nil || app.AccountID != acct.ID {
		http.NotFound(w, r)
		return
	}
	page := dashboard.Page{Title: "Release safety", Body: "release_safety", Account: dashboardAccountView(acct, 0),
		Data: s.dashboardReleaseSafetyData(r.Context(), acct, app)}
	if err := dashboard.Render(w, s.log, httpsec.NonceFromContext(r.Context()), page); err != nil {
		renderProblem(w, s.log, err)
	}
}

func (s *server) dashboardReleaseSafetyData(ctx context.Context, acct state.Account, app state.App) dashboard.ReleaseSafetyData {
	data := dashboard.ReleaseSafetyData{AppSlug: app.Slug}
	unavailable := func(section string, err error) {
		s.log.Warn("dashboard: release safety section unavailable", "app", app.ID, "section", section, "err", err)
		data.Unavailable = append(data.Unavailable, section)
	}
	if store, ok := s.releaseFreezes(); ok && s.capabilityEnabledFor("deploy-freezes", acct, app) {
		if status, err := s.releaseFreezeStatus(ctx, store, acct.ID, app); err != nil {
			unavailable("Deploy freezes", err)
		} else {
			data.Freezes = &status
		}
	}
	gates, gatesOK := s.store.(state.ReleaseGateStore)
	slos, slosOK := s.store.(state.SLOStore)
	if data.GatesEnabled = gatesOK && slosOK && s.capabilityEnabledFor("release-gates", acct, app); data.GatesEnabled {
		if rows, err := gates.ListReleaseGates(ctx, app.ID); err != nil {
			unavailable("Release gates", err)
		} else {
			names := s.sloNames(ctx, slos, app.ID)
			for _, g := range rows {
				data.Gates = append(data.Gates, releaseGateView(g, names))
			}
		}
	}
	approvals, approvalsOK := s.store.(state.ReleaseApprovalStore)
	if data.ApprovalsEnabled = approvalsOK && s.capabilityEnabledFor("release-approvals", acct, app); data.ApprovalsEnabled {
		if err := s.dashboardReleaseApprovals(ctx, approvals, acct, app, &data); err != nil {
			unavailable("Release approvals", err)
		}
	}
	schedules, schedulesOK := s.store.(state.ScheduledReleaseStore)
	if data.SchedulesEnabled = schedulesOK && s.capabilityEnabledFor("scheduled-releases", acct, app); data.SchedulesEnabled {
		if rows, err := schedules.ListScheduledReleases(ctx, acct.ID, app.ID, api.ScheduledReleaseListMax); err != nil {
			unavailable("Scheduled releases", err)
		} else {
			data.Schedules = s.scheduleViews(ctx, acct.ID, rows)
		}
	}
	return data
}

// dashboardReleaseApprovals fills the approval policy and the releases that
// wait for approval.
func (s *server) dashboardReleaseApprovals(ctx context.Context, store state.ReleaseApprovalStore, acct state.Account, app state.App, data *dashboard.ReleaseSafetyData) error {
	policy, err := store.ReleaseApprovalPolicy(ctx, app.ID)
	switch {
	case errors.Is(err, state.ErrNotFound):
	case err != nil:
		return err
	default:
		view := approval.PolicyView(policy, app.Slug, s.releaseApprovalChannelNames(ctx, acct.ID, policy.NotifyChannelIDs))
		data.Policy = &view
	}
	waiting, err := s.waitingReleaseApprovals(ctx, app)
	if err != nil {
		return err
	}
	data.Waiting = waiting
	return nil
}
