package main

import (
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"

	"github.com/onebox-faas/faas/pkg/dashboard"
	"github.com/onebox-faas/faas/pkg/httpsec"
	"github.com/onebox-faas/faas/pkg/state"
)

const dashboardAsyncInvocationPath = "/dashboard/invocations/"

func (s *server) renderAsyncInvocationDetail(w http.ResponseWriter, r *http.Request, log *slog.Logger, acct state.Account, id string) {
	ctx := r.Context()
	inv, err := s.store.InvocationByID(ctx, id)
	if errors.Is(err, state.ErrNotFound) || (err == nil && (inv.AccountID != acct.ID || inv.Source != state.InvocationAsyncInvoke)) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		log.Warn("dashboard async invocation: read invocation", "account_id", acct.ID, "invocation_id", id, "err", err)
		http.Error(w, "Invocation details are temporarily unavailable. Please try again shortly.", http.StatusServiceUnavailable)
		return
	}

	app, err := s.store.AppByID(ctx, inv.AppID)
	appSlug := "deleted app"
	if err == nil {
		if app.AccountID != acct.ID {
			http.NotFound(w, r)
			return
		}
		appSlug = app.Slug
	} else if !errors.Is(err, state.ErrNotFound) {
		log.Warn("dashboard async invocation: read app", "account_id", acct.ID, "app_id", inv.AppID, "invocation_id", id, "err", err)
		http.Error(w, "Invocation details are temporarily unavailable. Please try again shortly.", http.StatusServiceUnavailable)
		return
	}

	data := dashboard.AsyncInvocationDetailData{Invocation: projectDashboardAsyncInvocationDetail(inv, appSlug)}
	appCount, countErr := s.store.CountDeployedApps(ctx, acct.ID)
	if countErr != nil {
		log.Warn("dashboard async invocation: count apps", "account_id", acct.ID, "err", countErr)
		appCount = 0
	}
	view, _ := AccountFrom(ctx)
	page := dashboard.Page{
		Title:   "Async invocation — " + inv.ID,
		Body:    "async_invocation_detail",
		Account: dashboardAccountView(view, appCount),
		Data:    data,
	}
	if err := dashboard.Render(w, log, httpsec.NonceFromContext(ctx), page); err != nil {
		renderProblem(w, log, err)
	}
}

func projectDashboardAsyncInvocationDetail(inv state.Invocation, appSlug string) dashboard.AsyncInvocationDetailItem {
	item := dashboard.AsyncInvocationDetailItem{
		ID: inv.ID, AppSlug: appSlug, Method: inv.Method, Path: inv.Path,
		State: string(inv.State), StateClass: dashboardInvocationStateClass(inv.State),
		Attempts: inv.Attempts, CreatedAt: dashboardJobsTime(inv.CreatedAt), DueAt: dashboardJobsTime(inv.DueAt),
		RetryPolicy: dashboardAsyncInvocationRetryPolicy(inv), LastError: dashboard.FormatAlertError(inv.LastError),
		OnSuccessDestinationID: inv.OnSuccessDestinationID,
		OnFailureDestinationID: inv.OnFailureDestinationID,
	}
	if inv.Outcome != nil {
		item.Outcome = string(*inv.Outcome)
	}
	if inv.ReceivedAt != nil {
		item.ReceivedAt = dashboardJobsTime(*inv.ReceivedAt)
	}
	if inv.CompletedAt != nil {
		item.CompletedAt = dashboardJobsTime(*inv.CompletedAt)
	}
	if inv.DeadlineAt != nil {
		item.DeadlineAt = dashboardJobsTime(*inv.DeadlineAt)
	}
	if inv.ResultRetentionUntil != nil {
		item.ResultRetentionUntil = dashboardJobsTime(*inv.ResultRetentionUntil)
	}
	if appSlug != "deleted app" {
		item.OnSuccessDestinationURL = dashboardAsyncDestinationURL(appSlug, inv.OnSuccessDestinationID)
		item.OnFailureDestinationURL = dashboardAsyncDestinationURL(appSlug, inv.OnFailureDestinationID)
	}
	return item
}

func dashboardAsyncDestinationURL(appSlug, destinationID string) string {
	if appSlug == "" || destinationID == "" {
		return ""
	}
	return "/dashboard/apps/" + url.PathEscape(appSlug) + "/webhooks#webhook-" + url.PathEscape(destinationID)
}

func dashboardAsyncInvocationRetryPolicy(inv state.Invocation) string {
	policy := inv.RetryPolicy()
	if policy.Zero() {
		return "Platform default"
	}
	parts := make([]string, 0, 4)
	if policy.MaxAttempts > 0 {
		parts = append(parts, fmt.Sprintf("up to %d attempts", policy.MaxAttempts))
	}
	if policy.BaseSeconds > 0 {
		parts = append(parts, fmt.Sprintf("base delay %gs", policy.BaseSeconds))
	}
	if policy.MaxSeconds > 0 {
		parts = append(parts, fmt.Sprintf("delay cap %gs", policy.MaxSeconds))
	}
	if policy.JitterSeconds > 0 {
		parts = append(parts, fmt.Sprintf("±%g%% jitter", policy.JitterSeconds*100))
	}
	if len(parts) == 0 {
		return "Platform default"
	}
	return strings.Join(parts, " · ")
}
