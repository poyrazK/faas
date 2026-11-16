package apidserver

import (
	"context"
	"fmt"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/whycopy"
)

// appOperationalOOMWindow is how far back the operational summary looks for
// out-of-memory kills.
const appOperationalOOMWindow = 24 * time.Hour

// appOperationalOOMRecommendation reports the app's recent out-of-memory
// kills. Only the plan-gated debug status showed them, so a Free app's
// callers saw bare timeouts and closed sockets while inspect stayed quiet
// (2026-11-12 agent feedback). A failed read adds nothing: the summary never
// fails for it.
func (s *server) appOperationalOOMRecommendation(ctx context.Context, app state.App) (api.AppOperationalRecommendation, bool) {
	store, ok := s.store.(state.WorkloadOOMKillLister)
	if !ok {
		return api.AppOperationalRecommendation{}, false
	}
	now := time.Now().UTC()
	kills, total, err := workloadOOMKills(ctx, store, app.ID, now.Add(-appOperationalOOMWindow), now)
	if err != nil || len(kills) == 0 {
		return api.AppOperationalRecommendation{}, false
	}
	liveID := ""
	if live, err := s.store.LiveDeployment(ctx, app.ID); err == nil {
		liveID = live.ID
	}
	return appOOMRecommendation(app.Slug, kills[0], total, liveID), true
}

// appOOMRecommendation explains the newest kill. A kill on the release that
// serves now is an error; one on an earlier release is a warning, since a
// later deploy may have fixed it.
func appOOMRecommendation(slug string, newest api.DebugStatusOOMKill, total int, liveDeploymentID string) api.AppOperationalRecommendation {
	count := "1 out-of-memory kill"
	if total != 1 {
		count = fmt.Sprintf("%d out-of-memory kills", total)
	}
	message := count + " in the last 24 hours"
	switch {
	case newest.PeakMB > 0 && newest.PlanMB > 0:
		message += fmt.Sprintf("; the newest reached %d MB on a %d MB limit", newest.PeakMB, newest.PlanMB)
	case newest.PlanMB > 0:
		message += fmt.Sprintf("; the newest hit the %d MB limit", newest.PlanMB)
	}
	if newest.DeploymentFailed {
		message += ", and its deployment failed."
	} else {
		message += ". The killed instance was replaced; requests it was serving failed or timed out."
	}
	severity := "error"
	if liveDeploymentID != "" && newest.DeploymentID != "" && newest.DeploymentID != liveDeploymentID {
		severity = "warning"
		message += " It was on an earlier deployment than the one serving now."
	}
	ram := "<MB>"
	if suggested := whycopy.OOMSuggestedRAMMB(newest.PeakMB, newest.PlanMB); suggested > 0 {
		ram = fmt.Sprint(suggested)
	}
	next := "Raise its memory with `gregale app " + slug + " --ram " + ram + "` (your account plan sets the per-app maximum), or reduce what it holds in memory."
	return api.AppOperationalRecommendation{Code: api.CodeAppRuntimeOOM, Severity: severity, Message: message, Next: next}
}
