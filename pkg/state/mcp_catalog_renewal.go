package state

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
	"time"
)

type MCPCatalogOAuthRenewalStore interface {
	BeginMCPCatalogOAuthRenewal(context.Context, MCPCatalogObserverJob, string) (api.MCPCatalogOAuthRenewal, bool, error)
	RecordMCPCatalogOAuthRenewal(context.Context, MCPCatalogObserverJob, string, string, *api.MCPCatalogObserverOAuthStatus) error
}

func beginOAuthRenewal(root *api.MCPCatalogMonitorSnapshot, job MCPCatalogObserverJob, source string, at time.Time) (api.MCPCatalogOAuthRenewal, bool, error) {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok {
		return api.MCPCatalogOAuthRenewal{}, false, ErrNotFound
	}
	st := view.ObserverStatus
	if view.Config.Revision != job.Snapshot.Config.Revision || st == nil || job.LeaseID == "" || st.LeaseID != job.LeaseID || st.LeaseUntil == nil || !st.LeaseUntil.After(at) || view.Config.Observer == nil || view.Config.Observer.OAuth == nil || !view.Config.Enabled || !view.Config.Observer.Enabled || view.Config.Observer.Paused {
		return api.MCPCatalogOAuthRenewal{}, false, ErrMCPCatalogMonitorChanged
	}
	if len(source) != 64 {
		return api.MCPCatalogOAuthRenewal{}, false, ErrInvalidArgument
	}
	if st.OAuth != nil && st.OAuth.SourceSHA256 != source && !view.Config.Observer.OAuth.Guided {
		phase := retirementPhase(view.Config.Observer.OAuth, st.OAuth, at)
		lifecycleEvent(&view, "credentials_discarded", at)
		view.OAuthLifecycle.Revocation = phase
		view.OAuthLifecycle.CredentialState = "credential_changed"
		view.OAuthLifecycle.LocalState = "disconnected"
		st.OAuth, st.Renewal = nil, nil
		setObserverView(root, job.Role, view)
	}
	if old := st.Renewal; old != nil && old.SourceSHA256 == source && old.State != "installed" && old.State != "failed" {
		copy := *old
		if copy.State == "pending" && copy.LeaseID != job.LeaseID {
			copy.State = "reauthorization_required"
			copy.Outcome = "issuance_unknown"
			st.Renewal = &copy
			if st.OAuth != nil {
				oauth := *st.OAuth
				oauth.State = "reauthorization_required"
				st.OAuth = &oauth
			}
			lifecycleEvent(&view, "reauthorization_required", at)
			view.OAuthLifecycle.LocalState = "reauthorization_required"
			setObserverView(root, job.Role, view)
		}
		return copy, false, nil
	}
	journal := api.MCPCatalogOAuthRenewal{ID: uuid.NewString(), State: "pending", StartedAt: at, SourceSHA256: source, LeaseID: job.LeaseID}
	st.Renewal = &journal
	setObserverView(root, job.Role, view)
	return journal, true, nil
}

// Result recording is fenced by operation and revision, independently of the
// observer lease: a completed response can be recovered by the next lease owner.
func recordOAuthRenewal(root *api.MCPCatalogMonitorSnapshot, job MCPCatalogObserverJob, id, phase string, result *api.MCPCatalogObserverOAuthStatus) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok {
		return ErrNotFound
	}
	st := view.ObserverStatus
	if view.Config.Revision != job.Snapshot.Config.Revision || st == nil || st.Renewal == nil || st.Renewal.ID != id || (st.Renewal.State != "pending" && st.Renewal.State != "reauthorization_required") {
		return ErrMCPCatalogMonitorChanged
	}
	if phase != "saved" && phase != "failed" && phase != "reauthorization_required" {
		return ErrInvalidArgument
	}
	if phase == "saved" && (result == nil || len(result.Ciphertext) == 0 || len(result.Ciphertext) > api.MCPCatalogOAuthMaxResponseBytes || result.SourceSHA256 != st.Renewal.SourceSHA256 || (result.State != "connected" && result.State != "reauthorization_required") || result.State == "connected" && (result.ExpiresAt == nil || result.LastRenewalAt == nil)) {
		return ErrInvalidArgument
	}
	copy := *st.Renewal
	copy.State = phase
	copy.Result = result
	if phase == "saved" {
		copy.Outcome = "response_recorded"
	}
	if phase == "reauthorization_required" {
		copy.Outcome = "issuance_unknown"
	}
	if phase == "reauthorization_required" {
		if st.OAuth != nil {
			oauth := *st.OAuth
			oauth.State = "reauthorization_required"
			st.OAuth = &oauth
		}
		lifecycleEvent(&view, "reauthorization_required", time.Now().UTC())
		view.OAuthLifecycle.LocalState = "reauthorization_required"
	}
	st.Renewal = &copy
	setObserverView(root, job.Role, view)
	return nil
}
func (s *PgStore) BeginMCPCatalogOAuthRenewal(ctx context.Context, job MCPCatalogObserverJob, source string) (out api.MCPCatalogOAuthRenewal, fresh bool, err error) {
	if job.Snapshot.Config.Observer == nil || job.Snapshot.Config.Observer.OAuth == nil {
		return out, false, ErrInvalidArgument
	}
	busy, err := sqlc.New().MCPOAuthRetirementBindingBusy(ctx, s.pool, sqlc.MCPOAuthRetirementBindingBusyParams{AccountID: mustPgUUID(job.AccountID), BindingSha256: retirementBinding(job.Snapshot.Config.Observer.OAuth)})
	if err != nil {
		return out, false, err
	}
	if busy {
		return out, false, ErrConflict
	}
	_, err = s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		var e error
		out, fresh, e = beginOAuthRenewal(root, job, source, at)
		return e
	})
	return
}
func (m *MemStore) BeginMCPCatalogOAuthRenewal(_ context.Context, job MCPCatalogObserverJob, source string) (out api.MCPCatalogOAuthRenewal, fresh bool, err error) {
	_, err = m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, at time.Time) error {
		for _, entry := range m.mcpOAuthRetirements {
			if entry.AccountID == job.AccountID && entry.Binding == retirementBinding(job.Snapshot.Config.Observer.OAuth) && entry.Record.State == "pending" {
				return ErrConflict
			}
		}
		var e error
		out, fresh, e = beginOAuthRenewal(root, job, source, at)
		return e
	})
	return
}
func (s *PgStore) RecordMCPCatalogOAuthRenewal(ctx context.Context, job MCPCatalogObserverJob, id, phase string, result *api.MCPCatalogObserverOAuthStatus) error {
	_, err := s.editMCPCatalogMonitor(ctx, job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, _ time.Time) error {
		return recordOAuthRenewal(root, job, id, phase, result)
	})
	if errors.Is(err, ErrMCPCatalogMonitorChanged) && phase == "saved" && result != nil {
		_ = s.RecordMCPOAuthIssuedGrant(ctx, job.AccountID, id, result.Ciphertext, "credential_changed")
	}
	return err
}
func (m *MemStore) RecordMCPCatalogOAuthRenewal(ctx context.Context, job MCPCatalogObserverJob, id, phase string, result *api.MCPCatalogObserverOAuthStatus) error {
	_, err := m.editMCPCatalogMonitor(job.AccountID, job.AppID, false, func(root *api.MCPCatalogMonitorSnapshot, _ time.Time) error {
		return recordOAuthRenewal(root, job, id, phase, result)
	})
	if errors.Is(err, ErrMCPCatalogMonitorChanged) && phase == "saved" && result != nil {
		_ = m.RecordMCPOAuthIssuedGrant(ctx, job.AccountID, id, result.Ciphertext, "credential_changed")
	}
	return err
}
