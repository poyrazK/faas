package apidserver

import (
	"errors"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/state"
)

// resumeEnvironmentAutomation enables, in one stage, the crons, event
// subscriptions and bucket notifications a full clone copied paused that the
// source ran (ADR-20261104120000 amendments 2026-11-12, 2026-11-14 and
// 2026-11-16). Ones the source
// had disabled, and any a customer enabled or disabled since, stay as they
// are. Production has nothing a clone paused. A retry after a partial
// failure resumes the rest.
func (s *server) resumeEnvironmentAutomation(w http.ResponseWriter, r *http.Request, acct state.Account) {
	if p, ok := principalFrom(r); ok && p.Key != nil && p.Key.AppID != "" {
		api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Account key required",
			"resuming an environment's automation requires an account-scoped credential"))
		return
	}
	project, ok := s.loadProject(w, r, acct)
	if !ok {
		return
	}
	environment := r.PathValue("environment")
	if state.IsProductionScope(environment) {
		api.WriteProblem(w, api.ErrValidation("production has no automation a clone paused; name a stage"))
		return
	}
	resp, problem := s.resumeClonePausedAutomation(r, acct, project, environment)
	if problem != nil {
		api.WriteProblem(w, problem)
		return
	}
	for _, id := range resp.CronIDs {
		_ = s.notif.Notify(r.Context(), db.NotifyCronChanged, `{"kind":"updated","cron":"`+id+`"}`)
	}
	s.audit.Emit(r.Context(), "project.environment.automation_resumed", &acct.ID, map[string]any{
		"project_id": project.ID, "project_slug": project.Slug, "environment": environment, "cron_ids": resp.CronIDs,
		"event_subscription_ids": resp.EventSubscriptionIDs, "bucket_ids": resp.BucketIDs,
	})
	writeJSON(w, http.StatusOK, resp)
}

// resumeClonePausedAutomation resumes, in order, the event subscriptions,
// bucket notifications and crons a full clone paused in one stage.
// Subscriptions go first: a refusal over the plan's event subscription caps
// then leaves everything as the clone left it. A retry after a later failure
// resumes the rest.
func (s *server) resumeClonePausedAutomation(r *http.Request, acct state.Account, project state.Project, environment string) (api.ResumeEnvironmentAutomationResponse, *api.Problem) {
	crons, cronsOK := s.store.(state.CloneResumeStore)
	subscriptions, subscriptionsOK := s.store.(state.CloneEventSubscriptionResumeStore)
	buckets, bucketsOK := s.store.(state.CloneBucketNotificationResumeStore)
	limits, limitsOK := api.LimitsFor(acct.Plan)
	if !cronsOK || !subscriptionsOK || !bucketsOK || !limitsOK {
		return api.ResumeEnvironmentAutomationResponse{}, api.ErrInternal("environment automation resume unavailable")
	}
	ctx := r.Context()
	resumedSubscriptions, err := subscriptions.ResumeEnvironmentClonePausedEventSubscriptions(ctx, project.AccountID, project.ID, environment, limits)
	var quota *state.EventSubscriptionQuotaError
	if errors.As(err, &quota) {
		return api.ResumeEnvironmentAutomationResponse{}, api.ErrPlanEventSubscriptionQuota(acct.Plan, quota.Scope, quota.Limit, quota.Observed)
	}
	var bucketIDs []string
	if err == nil {
		bucketIDs, err = buckets.ResumeEnvironmentClonePausedBucketNotifications(ctx, project.AccountID, project.ID, environment)
	}
	var resumedCrons []state.Cron
	if err == nil {
		resumedCrons, err = crons.ResumeEnvironmentClonePausedCrons(ctx, project.AccountID, project.ID, environment)
	}
	switch {
	case errors.Is(err, state.ErrNotFound):
		return api.ResumeEnvironmentAutomationResponse{}, api.NewProblem(http.StatusNotFound, api.CodeNotFound, "Not found",
			"no environment "+environment+" in project "+project.Slug)
	case err != nil:
		return api.ResumeEnvironmentAutomationResponse{}, api.ErrCapacity("could not resume the environment's automation")
	}
	resp := api.ResumeEnvironmentAutomationResponse{Environment: environment, CronsResumed: len(resumedCrons), CronIDs: make([]string, 0, len(resumedCrons)),
		EventSubscriptionsResumed: len(resumedSubscriptions), EventSubscriptionIDs: make([]string, 0, len(resumedSubscriptions)),
		BucketNotificationsResumed: len(bucketIDs), BucketIDs: append(make([]string, 0, len(bucketIDs)), bucketIDs...)}
	for _, c := range resumedCrons {
		resp.CronIDs = append(resp.CronIDs, c.ID)
	}
	for _, sub := range resumedSubscriptions {
		resp.EventSubscriptionIDs = append(resp.EventSubscriptionIDs, sub.ID)
	}
	return resp, nil
}
