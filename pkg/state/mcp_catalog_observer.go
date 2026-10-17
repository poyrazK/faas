package state

import (
	"context"
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"sort"
	"time"
)

type MCPCatalogObserverJob struct {
	AccountID, AppID, Slug, Role string
	Snapshot                     api.MCPCatalogMonitorSnapshot
	Retirement                   *api.MCPOAuthRetirement
	LeaseID                      string
}
type MCPCatalogObserverStore interface {
	ListDueMCPCatalogObservers(context.Context, int) ([]MCPCatalogObserverJob, error)
	ClaimMCPCatalogObserver(context.Context, MCPCatalogObserverJob) (MCPCatalogObserverJob, error)
	SaveMCPCatalogObserverOAuth(context.Context, MCPCatalogObserverJob, api.MCPCatalogObserverOAuthStatus) error
	CompleteMCPCatalogObserver(context.Context, MCPCatalogObserverJob, api.MCPCatalogReceipt, string, time.Duration) (api.MCPCatalogReceipt, error)
}

func (s *PgStore) ListDueMCPCatalogObservers(ctx context.Context, limit int) ([]MCPCatalogObserverJob, error) {
	if limit < 1 || limit > api.MCPCatalogObserverConcurrency {
		return nil, ErrInvalidArgument
	}
	rows, err := sqlc.New().ListDueMCPCatalogObservers(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, err
	}
	out := make([]MCPCatalogObserverJob, 0, len(rows))
	for _, row := range rows {
		var snapshot api.MCPCatalogMonitorSnapshot
		if err := json.Unmarshal(row.View, &snapshot); err != nil {
			return nil, err
		}
		out = append(out, MCPCatalogObserverJob{AccountID: uuid.UUID(row.AccountID.Bytes).String(), AppID: uuid.UUID(row.AppID.Bytes).String(), Slug: row.Slug, Role: row.Role, Snapshot: snapshot})
	}
	return out, nil
}
func observerDue(view api.MCPCatalogMonitorSnapshot, at time.Time) bool {
	cfg := view.Config.Observer
	if !view.Config.Enabled || cfg == nil || !cfg.Enabled || cfg.Paused {
		return false
	}
	status := view.ObserverStatus
	if status != nil && status.OAuthFlow != nil && status.OAuthFlow.BrowserSessionSHA256 != "" && status.OAuthFlow.ExpiresAt.After(at) {
		return false
	}
	return status == nil || ((status.LeaseUntil == nil || !status.LeaseUntil.After(at)) && (status.NextCheckAt == nil || !status.NextCheckAt.After(at)))
}
func (m *MemStore) ListDueMCPCatalogObservers(_ context.Context, limit int) ([]MCPCatalogObserverJob, error) {
	if limit < 1 || limit > api.MCPCatalogObserverConcurrency {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []MCPCatalogObserverJob{}
	at := time.Now().UTC()
	for id, root := range m.mcpCatalogMonitors {
		app, ok := m.apps[id]
		if !ok || m.mcpCatalogMonitorOwners[id] != app.AccountID || !app.AcceptsRequestInvocations() || app.PublicAuthMode == AppPublicAuthModeInternalOnly || (app.Status != AppActive && app.Status != AppEvictedCold) {
			continue
		}
		account, ok := m.accounts[app.AccountID]
		if !ok || !account.Plan.CustomMetricsAllowed() {
			continue
		}
		views := api.CatalogMonitorSummary(root, app.Slug, at).Roles
		for role := range views {
			// Summary hides the lease token; eligibility uses the stored role instead.
			view, _ := api.CatalogMonitorRoleSnapshot(root, role)
			if observerDue(view, at) {
				out = append(out, MCPCatalogObserverJob{AccountID: app.AccountID, AppID: id, Slug: app.Slug, Role: role, Snapshot: cloneMCPCatalogMonitor(view)})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Snapshot.ObserverStatus, out[j].Snapshot.ObserverStatus
		if a == nil && b != nil {
			return true
		}
		if a != nil && b == nil {
			return false
		}
		if a != nil && b != nil && a.NextCheckAt != nil && b.NextCheckAt != nil && !a.NextCheckAt.Equal(*b.NextCheckAt) {
			return a.NextCheckAt.Before(*b.NextCheckAt)
		}
		return out[i].AppID+out[i].Role < out[j].AppID+out[j].Role
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func setObserverView(root *api.MCPCatalogMonitorSnapshot, role string, view api.MCPCatalogMonitorSnapshot) {
	if api.CatalogMonitorRole(role) == "default" {
		roles := root.Roles
		*root = view
		root.Roles = roles
	} else {
		root.Roles[role] = view
	}
}
func claimObserver(root *api.MCPCatalogMonitorSnapshot, job *MCPCatalogObserverJob, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok {
		return ErrNotFound
	}
	if view.Config.Revision != job.Snapshot.Config.Revision || !observerDue(view, at) {
		return ErrMCPCatalogMonitorChanged
	}
	if view.ObserverStatus == nil {
		view.ObserverStatus = &api.MCPCatalogObserverStatus{}
	}
	until := at.Add(time.Duration(api.MCPCatalogObserverLeaseSeconds) * time.Second)
	status := *view.ObserverStatus
	status.State = "checking"
	status.LeaseID = uuid.NewString()
	status.LeaseUntil = &until
	status.LastAttemptAt = &at
	view.ObserverStatus = &status
	setObserverView(root, job.Role, view)
	job.LeaseID = status.LeaseID
	job.Snapshot = view
	return nil
}
func (s *PgStore) ClaimMCPCatalogObserver(ctx context.Context, job MCPCatalogObserverJob) (MCPCatalogObserverJob, error) {
	_, err := s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error { return claimObserver(root, &job, at) })
	return job, err
}
func (m *MemStore) ClaimMCPCatalogObserver(_ context.Context, job MCPCatalogObserverJob) (MCPCatalogObserverJob, error) {
	_, err := m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error { return claimObserver(root, &job, at) })
	return job, err
}
func completeObserver(root *api.MCPCatalogMonitorSnapshot, job MCPCatalogObserverJob, receipt *api.MCPCatalogReceipt, reason string, delay time.Duration, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok {
		return ErrNotFound
	}
	cfg := view.Config.Observer
	status := view.ObserverStatus
	if cfg == nil || !cfg.Enabled || cfg.Paused || !view.Config.Enabled || view.Config.Revision != job.Snapshot.Config.Revision || status == nil || status.LeaseID != job.LeaseID || job.LeaseID == "" || status.LeaseUntil == nil || !status.LeaseUntil.After(at) {
		return ErrMCPCatalogMonitorChanged
	}
	if api.CatalogMonitorRole(receipt.Role) != api.CatalogMonitorRole(job.Role) || receipt.Revision != view.Config.Revision || delay <= 0 || delay > time.Duration(api.MCPCatalogObserverMaxIntervalSeconds+api.MCPCatalogObserverMaxBackoffSeconds)*time.Second {
		return ErrInvalidArgument
	}
	if reason != "" && reason != "credential_unavailable" && reason != "source_unavailable" && reason != "comparison_unavailable" {
		return ErrInvalidArgument
	}
	if (reason == "") != (receipt.State != "unavailable") {
		return ErrInvalidArgument
	}
	if err := mutateMCPCatalogMonitorRole(&view, nil, receipt, at); err != nil {
		return err
	}
	next := at.Add(delay)
	updated := *status
	updated.State = "scheduled"
	updated.LeaseID = ""
	updated.LeaseUntil = nil
	updated.NextCheckAt = &next
	updated.FailureReason = reason
	if reason == "" {
		updated.LastSuccessAt = &at
		updated.ConsecutiveFailures = 0
	} else {
		updated.State = "degraded"
		if updated.ConsecutiveFailures < api.MCPCatalogObserverMaxFailures {
			updated.ConsecutiveFailures++
		}
	}
	view.ObserverStatus = &updated
	setObserverView(root, job.Role, view)
	*receipt = *view.Latest
	return nil
}
func (s *PgStore) CompleteMCPCatalogObserver(ctx context.Context, job MCPCatalogObserverJob, receipt api.MCPCatalogReceipt, reason string, delay time.Duration) (api.MCPCatalogReceipt, error) {
	_, err := s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return completeObserver(root, job, &receipt, reason, delay, at)
	})
	return receipt, err
}
func (m *MemStore) CompleteMCPCatalogObserver(_ context.Context, job MCPCatalogObserverJob, receipt api.MCPCatalogReceipt, reason string, delay time.Duration) (api.MCPCatalogReceipt, error) {
	_, err := m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return completeObserver(root, job, &receipt, reason, delay, at)
	})
	return receipt, err
}

// saveObserverOAuth commits token rotation before catalog requests, under the
// same live revision and lease fence as observation completion.
func saveObserverOAuth(root *api.MCPCatalogMonitorSnapshot, job MCPCatalogObserverJob, oauth api.MCPCatalogObserverOAuthStatus, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok {
		return ErrNotFound
	}
	cfg, status := view.Config.Observer, view.ObserverStatus
	if cfg == nil || cfg.OAuth == nil || !cfg.Enabled || cfg.Paused || !view.Config.Enabled || view.Config.Revision != job.Snapshot.Config.Revision || status == nil || status.LeaseID != job.LeaseID || job.LeaseID == "" || status.LeaseUntil == nil || !status.LeaseUntil.After(at) {
		return ErrMCPCatalogMonitorChanged
	}
	if (oauth.State != "connected" && oauth.State != "reauthorization_required") || len(oauth.Ciphertext) == 0 || len(oauth.Ciphertext) > api.MCPCatalogOAuthMaxResponseBytes || len(oauth.SourceSHA256) != 64 || (oauth.State == "connected" && (oauth.LastRenewalAt == nil || oauth.ExpiresAt == nil || !oauth.ExpiresAt.After(at) && (status.Renewal == nil || status.Renewal.State != "saved"))) {
		return ErrInvalidArgument
	}
	updated := *status
	if status.Renewal != nil && status.Renewal.State == "saved" {
		saved, savedErr := json.Marshal(status.Renewal.Result)
		incoming, incomingErr := json.Marshal(oauth)
		if savedErr != nil || incomingErr != nil || string(saved) != string(incoming) {
			return ErrMCPCatalogMonitorChanged
		}
		journal := *status.Renewal
		journal.State, journal.Result = "installed", nil
		if oauth.State == "reauthorization_required" {
			journal.State = "reauthorization_required"
		}
		updated.Renewal = &journal
	}
	if status.OAuth != nil && status.OAuth.SourceSHA256 == oauth.SourceSHA256 {
		oauth.RetirementID = status.OAuth.RetirementID
	}
	updated.OAuth = &oauth
	kind := "renewed"
	if oauth.State == "reauthorization_required" {
		kind = "reauthorization_required"
	} else if status.OAuth == nil || status.OAuth.SourceSHA256 != oauth.SourceSHA256 {
		kind = "connected"
	}
	lifecycleEvent(&view, kind, at)
	view.OAuthLifecycle.LocalState = oauth.State
	if oauth.State == "connected" {
		view.OAuthLifecycle.CredentialState = ""
	}
	view.ObserverStatus = &updated
	setObserverView(root, job.Role, view)
	return nil
}
func (s *PgStore) SaveMCPCatalogObserverOAuth(ctx context.Context, job MCPCatalogObserverJob, oauth api.MCPCatalogObserverOAuthStatus) error {
	_, err := s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return saveObserverOAuth(root, job, oauth, at)
	})
	return err
}
func (m *MemStore) SaveMCPCatalogObserverOAuth(_ context.Context, job MCPCatalogObserverJob, oauth api.MCPCatalogObserverOAuthStatus) error {
	_, err := m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		return saveObserverOAuth(root, job, oauth, at)
	})
	return err
}
