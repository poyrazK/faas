package apidserver

import (
	"context"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/issuetrackers"
	"github.com/onebox-faas/faas/pkg/issuetracker"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) startIssueTrackerAutoSync(ctx context.Context) {
	if _, ok := s.store.(state.IssueTrackerAutoSyncStore); !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(api.IssueMaintenanceInterval)
		defer ticker.Stop()
		for {
			s.runIssueTrackerAutoSyncBatch(ctx)
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *server) runIssueTrackerAutoSyncBatch(ctx context.Context) {
	if store, ok := s.store.(state.LinearOAuthStore); ok {
		if err := store.CleanupLinearOAuthFlows(ctx); err != nil {
			s.log.Warn("Linear OAuth flow cleanup failed")
		}
	}
	st, ok := s.store.(state.IssueTrackerAutoSyncStore)
	if !ok {
		return
	}
	if callbacks, ok := s.store.(state.IssueTrackerCallbackStore); ok {
		for i := 0; i < api.IssueTrackerCallbackBatch; i++ {
			processed, err := callbacks.ProcessIssueTrackerCallback(ctx)
			if err != nil {
				s.log.Warn("tracker callback scheduling failed")
				break
			}
			if !processed {
				break
			}
		}
		if err := callbacks.CleanupIssueTrackerCallbacks(ctx); err != nil {
			s.log.Warn("tracker callback receipt cleanup failed")
		}
	}
	for i := 0; i < api.IssueTrackerAutoSyncBatch && ctx.Err() == nil; i++ {
		row, err := st.ClaimIssueTrackerAutoSync(ctx)
		if errors.Is(err, state.ErrNoIssueTrackerAutoSyncWork) {
			return
		}
		if err != nil {
			s.log.Warn("automatic issue tracker sync claim failed")
			return
		}
		out := s.runIssueTrackerAutoSync(ctx, st, row)
		if out.Completed {
			continue
		}
		// Retain observations after a provider timeout; fencing refuses stale completions.
		finishCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
		_, err = st.FinishIssueTrackerAutoSync(finishCtx, out)
		cancel()
		if err != nil && !errors.Is(err, state.ErrIssueTrackerAutoSyncClaimLost) {
			s.log.Warn("automatic issue tracker sync receipt failed", "link_id", row.Policy.LinkID)
		}
	}
}
func (s *server) runIssueTrackerAutoSync(ctx context.Context, st state.IssueTrackerAutoSyncStore, row state.IssueTrackerAutoSyncRecord) state.IssueTrackerAutoSyncRecord {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.IssueTrackerRequestTimeoutSeconds)*time.Second)
	defer cancel()
	app, err := s.store.AppByID(ctx, row.AppID)
	if err != nil {
		row.Policy.Outcome = "error"
		row.Policy.Reason = "app_unavailable"
		return row
	}
	account, err := s.store.AccountByID(ctx, row.AccountID)
	if err != nil {
		row.Policy.Outcome = "error"
		row.Policy.Reason = "account_unavailable"
		return row
	}
	if app.AccountID != account.ID || !account.Plan.IssueLimits().Enabled || !account.MayDeploy() || app.DeletedAt != nil || app.Status == state.AppDeleted {
		row.Policy.Outcome = "blocked"
		row.Policy.Reason = "owner_or_entitlement_unavailable"
		return row
	}
	provider := s.nativeTrackerProvider
	if provider == nil {
		provider = issuetracker.NewClient(nil)
	}
	if row.Baseline.Provider == "linear" {
		service := issuetrackers.LinearService{Host: nativeLinearCreationHost{server: s, app: app, account: account, issueID: row.Policy.SourceIssueID, linkID: row.Policy.LinkID}, Provider: provider}
		return issuetrackers.LinearAutoSyncStep(ctx, service, st, row)
	}
	service := issuetrackers.Service{Host: nativeAutoSyncHost{nativeIssueTrackerHost{server: s, app: app, account: account, issueID: row.Policy.SourceIssueID, linkID: row.Policy.LinkID}}, Provider: provider}
	return issuetrackers.AutoSyncStep(ctx, service, st, row)
}

type nativeAutoSyncHost struct{ nativeIssueTrackerHost }

func (h nativeAutoSyncHost) Prepare(ctx context.Context) (issuetrackers.Prepared, error) {
	app, err := h.server.store.AppByID(ctx, h.app.ID)
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	account, err := h.server.store.AccountByID(ctx, h.account.ID)
	if err != nil {
		return issuetrackers.Prepared{}, err
	}
	if app.AccountID != account.ID || app.DeletedAt != nil || app.Status == state.AppDeleted || !account.MayDeploy() || !account.Plan.IssueLimits().Enabled {
		return issuetrackers.Prepared{}, api.NewProblem(409, "issue_tracker_auto_sync_ineligible", "Automatic sync paused", "owner or Issues entitlement is unavailable")
	}
	h.app = app
	h.account = account
	return h.nativeIssueTrackerHost.Prepare(ctx)
}
