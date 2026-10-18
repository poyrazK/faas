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

func retirementJob(account string, r api.MCPOAuthRetirement) MCPCatalogObserverJob {
	root := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Scope: r.Scope, Revision: r.ID, Role: r.Role}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []api.MCPCatalogOAuthEvent{}, Revocation: r.Revocation}}
	if r.Role != "default" {
		root.Config.Role = r.Role
	}
	return MCPCatalogObserverJob{AccountID: account, AppID: r.AppID, Slug: r.AppSlug, Role: r.Role, Snapshot: root, Retirement: &r, LeaseID: r.Revocation.LeaseID}
}
func (s *PgStore) ListDueMCPCatalogOAuthRevocations(ctx context.Context, limit int) ([]MCPCatalogObserverJob, error) {
	if limit < 1 || limit > api.MCPCatalogObserverConcurrency {
		return nil, ErrInvalidArgument
	}
	if err := sqlc.New().PruneMCPOAuthRetirementMetadata(ctx, s.pool); err != nil {
		return nil, err
	}
	rows, err := sqlc.New().ListDueMCPOAuthRetirements(ctx, s.pool, int32(limit))
	if err != nil {
		return nil, err
	}
	out := []MCPCatalogObserverJob{}
	for _, row := range rows {
		var r api.MCPOAuthRetirement
		if err = json.Unmarshal(row.Payload, &r); err != nil {
			return nil, err
		}
		out = append(out, retirementJob(uuid.UUID(row.AccountID.Bytes).String(), r))
	}
	return out, nil
}
func retirementDue(r api.MCPOAuthRetirement, at time.Time) bool {
	switch r.State {
	case "pending", "held", "awaiting_issuance", "manual_required":
		if !r.RetainUntil.After(at) {
			return true
		}
	}
	switch r.State {
	case "pending", "held", "awaiting_issuance":
		return !r.Revocation.NextAttemptAt.After(at) && (r.Revocation.LeaseUntil == nil || !r.Revocation.LeaseUntil.After(at))
	}
	return false
}
func (m *MemStore) ListDueMCPCatalogOAuthRevocations(_ context.Context, limit int) ([]MCPCatalogObserverJob, error) {
	if limit < 1 || limit > api.MCPCatalogObserverConcurrency {
		return nil, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	at := time.Now().UTC()
	out := []MCPCatalogObserverJob{}
	for id, entry := range m.mcpOAuthRetirements {
		switch entry.Record.State {
		case "installed", "provider_acknowledged", "manually_acknowledged", "expired":
			if entry.Record.CreatedAt.Before(at.Add(-time.Duration(api.MCPOAuthRetirementMetadataSeconds) * time.Second)) {
				delete(m.mcpOAuthRetirements, id)
			}
		}
		if retirementDue(entry.Record, at) {
			out = append(out, retirementJob(entry.AccountID, cloneRetirement(entry.Record)))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i].Retirement, out[j].Retirement
		if !a.Revocation.NextAttemptAt.Equal(b.Revocation.NextAttemptAt) {
			return a.Revocation.NextAttemptAt.Before(b.Revocation.NextAttemptAt)
		}
		return a.ID < b.ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
func advanceRetirement(r *api.MCPOAuthRetirement, job *MCPCatalogObserverJob, action string, active bool, at time.Time) error {
	if job.Retirement == nil || job.Retirement.ID != r.ID {
		return ErrMCPCatalogMonitorChanged
	}
	if action == "claim" {
		if !retirementDue(*r, at) {
			return ErrMCPCatalogMonitorChanged
		}
		if !r.RetainUntil.After(at) {
			r.State = "expired"
			r.Revocation = api.MCPOAuthRetirementPublic(*r).Revocation
			r.Revocation.State = "manual_required"
			retirementEvent(r, "expired", at)
		} else if r.State == "awaiting_issuance" {
			r.State = "manual_required"
			r.Reason = "issuance_unknown"
			r.Revocation.State = "manual_required"
			retirementEvent(r, "issuance_unknown", at)
		} else if active && !r.AllowActiveGrantRevocation {
			if r.State != "held" {
				retirementEvent(r, "held_for_active_connection", at)
			}
			r.State = "held"
			r.Revocation.NextAttemptAt = at.Add(time.Duration(api.MCPOAuthRetirementHeldPollSeconds) * time.Second)
		} else {
			r.State = "pending"
			r.Revocation.State = "pending"
			if !revocationDue(api.MCPCatalogMonitorSnapshot{OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{Revocation: r.Revocation}}, at) {
				return ErrMCPCatalogMonitorChanged
			}
			if err := advanceRetirementPhase(r, job, action, at); err != nil {
				return err
			}
		}
	} else {
		if r.State != "pending" {
			return ErrMCPCatalogMonitorChanged
		}
		if err := advanceRetirementPhase(r, job, action, at); err != nil {
			return err
		}
	}
	job.Retirement = r
	job.Snapshot.OAuthLifecycle = &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []api.MCPCatalogOAuthEvent{}, Revocation: r.Revocation}
	job.LeaseID = r.Revocation.LeaseID
	return nil
}
func advanceRetirementPhase(r *api.MCPOAuthRetirement, job *MCPCatalogObserverJob, action string, at time.Time) error {
	view := api.MCPCatalogMonitorSnapshot{Config: api.MCPCatalogMonitorConfig{Revision: r.ID}, OAuthLifecycle: &api.MCPCatalogOAuthLifecycle{LocalState: "disconnected", History: []api.MCPCatalogOAuthEvent{}, Revocation: r.Revocation}}
	phaseJob := *job
	phaseJob.Role = "default"
	phaseJob.Snapshot = view
	if err := changeRevocation(&view, &phaseJob, action, at); err != nil {
		return err
	}
	r.Revocation = view.OAuthLifecycle.Revocation
	if r.Revocation.State != "pending" {
		r.State = r.Revocation.State
		retirementEvent(r, r.State, at)
	}
	job.LeaseID = r.Revocation.LeaseID
	return nil
}
func (s *PgStore) ChangeMCPCatalogOAuthRevocation(ctx context.Context, job MCPCatalogObserverJob, action string) (MCPCatalogObserverJob, error) {
	if job.Retirement == nil {
		return job, ErrInvalidArgument
	}
	out, err := s.editRetirement(ctx, job.AccountID, job.Retirement.ID, func(r *api.MCPOAuthRetirement, active bool, at time.Time) error {
		return advanceRetirement(r, &job, action, active, at)
	})
	if err == nil {
		job = retirementJob(job.AccountID, out)
	}
	return job, err
}
func (m *MemStore) ChangeMCPCatalogOAuthRevocation(_ context.Context, job MCPCatalogObserverJob, action string) (MCPCatalogObserverJob, error) {
	if job.Retirement == nil {
		return job, ErrInvalidArgument
	}
	out, err := m.editRetirement(job.AccountID, job.Retirement.ID, func(r *api.MCPOAuthRetirement, active bool, at time.Time) error {
		return advanceRetirement(r, &job, action, active, at)
	})
	if err == nil {
		job = retirementJob(job.AccountID, out)
	}
	return job, err
}
