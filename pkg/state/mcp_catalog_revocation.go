package state

import (
	"context"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"time"
)

type MCPCatalogOAuthRevocationStore interface {
	ListDueMCPCatalogOAuthRevocations(context.Context, int) ([]MCPCatalogObserverJob, error)
	ChangeMCPCatalogOAuthRevocation(context.Context, MCPCatalogObserverJob, string) (MCPCatalogObserverJob, error)
}

func revocationDue(view api.MCPCatalogMonitorSnapshot, at time.Time) bool {
	if view.OAuthLifecycle == nil || view.OAuthLifecycle.Revocation == nil {
		return false
	}
	j := view.OAuthLifecycle.Revocation
	return j.State == "pending" && !j.NextAttemptAt.After(at) && (j.LeaseUntil == nil || !j.LeaseUntil.After(at))
}
func changeRevocation(root *api.MCPCatalogMonitorSnapshot, job *MCPCatalogObserverJob, action string, at time.Time) error {
	view, ok := api.CatalogMonitorRoleSnapshot(*root, job.Role)
	if !ok || view.OAuthLifecycle == nil || view.OAuthLifecycle.Revocation == nil || job.Snapshot.OAuthLifecycle == nil || job.Snapshot.OAuthLifecycle.Revocation == nil {
		return ErrMCPCatalogMonitorChanged
	}
	current := view.OAuthLifecycle.Revocation
	if current.State != "pending" || current.ID != job.Snapshot.OAuthLifecycle.Revocation.ID {
		return ErrMCPCatalogMonitorChanged
	}
	lifecycle := *view.OAuthLifecycle
	next := *current
	view.OAuthLifecycle = &lifecycle
	view.OAuthLifecycle.Revocation = &next
	if action == "claim" {
		if !revocationDue(view, at) {
			return ErrMCPCatalogMonitorChanged
		}
		if next.Attempts >= api.MCPCatalogOAuthRevocationMaxAttempts {
			next.State = "manual_required"

			next.LeaseID = ""
			next.LeaseUntil = nil
			lifecycleEvent(&view, "revocation_manual_required", at)
			setObserverView(root, job.Role, view)
			job.Snapshot = view
			job.LeaseID = ""
			return nil
		}
		until := at.Add(time.Duration(api.MCPCatalogObserverLeaseSeconds) * time.Second)
		next.LeaseUntil = &until
		next.LeaseID = uuid.NewString()
		next.Attempts++
	} else {
		if job.LeaseID == "" || current.LeaseID != job.LeaseID || current.LeaseUntil == nil || !current.LeaseUntil.After(at) {
			return ErrMCPCatalogMonitorChanged
		}
		switch action {
		case "refresh_acknowledged":
			next.RefreshAcknowledged = true
		case "access_acknowledged":
			next.AccessAcknowledged = true
		case "credential_unavailable", "provider_unavailable":
			next.FailureReason = action
			next.LeaseID = ""
			next.LeaseUntil = nil
			delay := time.Duration(1<<min(next.Attempts, 6)) * time.Minute
			if delay > time.Duration(api.MCPCatalogOAuthRevocationMaxBackoffSeconds)*time.Second {
				delay = time.Duration(api.MCPCatalogOAuthRevocationMaxBackoffSeconds) * time.Second
			}
			next.NextAttemptAt = at.Add(delay)
			if next.Attempts >= api.MCPCatalogOAuthRevocationMaxAttempts {
				next.State = "manual_required"
				lifecycleEvent(&view, "revocation_manual_required", at)
			}
		default:
			return ErrInvalidArgument
		}
		if next.RefreshAcknowledged && next.AccessAcknowledged {
			next.State = "provider_acknowledged"
			next.AcknowledgedAt = &at
			next.FailureReason = ""
			lifecycleEvent(&view, "revocation_acknowledged", at)
		}
		if next.State == "provider_acknowledged" {
			next.Config = nil
			next.Ciphertext = nil
			next.ClientCiphertext = nil
			next.LeaseID = ""
			next.LeaseUntil = nil
		}
	}
	setObserverView(root, job.Role, view)
	job.Snapshot = view
	job.LeaseID = next.LeaseID
	return nil
}
