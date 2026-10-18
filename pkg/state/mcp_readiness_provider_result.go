package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// Completion atomically records provider acceptance/failure with the fenced queue outcome.
type MCPReadinessProviderResult struct {
	Accepted   bool
	StatusCode int
	Failure    string
	RetryAfter *time.Time
}
type MCPReadinessProviderOutboxStore interface {
	CompleteMCPReadinessProviderDelivery(context.Context, string, string, time.Time, MCPReadinessProviderResult) error
}

func validateReadinessProviderResult(r MCPReadinessProviderResult, at time.Time) error {
	if r.StatusCode < 0 || r.StatusCode > 599 || r.Accepted && (r.StatusCode != 202 || r.Failure != "" || r.RetryAfter != nil) {
		return ErrInvalidArgument
	}
	switch r.Failure {
	case "", "secret_unavailable", "invalid_event", "transport_error", "provider_rejected", "invalid_acknowledgement":
	default:
		return ErrInvalidArgument
	}
	if !r.Accepted && r.Failure == "" {
		return ErrInvalidArgument
	}
	if r.RetryAfter != nil && (r.RetryAfter.Sub(at) > time.Duration(api.MCPReadinessPagerDutyMaxRetryAfterSeconds)*time.Second) {
		return ErrInvalidArgument
	}
	return nil
}
func applyReadinessProviderResult(d AlertDelivery, r MCPReadinessProviderResult, at time.Time) AlertDelivery {
	d.Status = AlertDeliveryFailed
	d.AttemptCount = 1
	d.LastStatusCode = r.StatusCode
	d.LastError = r.Failure
	if r.Accepted {
		d.Status = AlertDeliveryDelivered
		d.DeliveredAt = at
	}
	return d
}
func readinessProviderRetry(e *readinessIncidentEventRecord, name string, r *MCPReadinessProviderResult) {
	if r == nil || r.RetryAfter == nil {
		return
	}
	d := e.Destinations[name]
	if d.AvailableAt.Before(*r.RetryAfter) {
		d.AvailableAt = *r.RetryAfter
		e.Destinations[name] = d
		projectReadinessDestinations(e)
	}
}
func (m *MemStore) CompleteMCPReadinessProviderDelivery(ctx context.Context, id, token string, at time.Time, r MCPReadinessProviderResult) error {
	if validateReadinessProviderResult(r, at) != nil {
		return ErrInvalidArgument
	}
	return m.completeReadinessDelivery(ctx, id, token, at, &r)
}
func (s *PgStore) CompleteMCPReadinessProviderDelivery(ctx context.Context, id, token string, at time.Time, r MCPReadinessProviderResult) error {
	if validateReadinessProviderResult(r, at) != nil {
		return ErrInvalidArgument
	}
	return s.completeReadinessDelivery(ctx, id, token, at, &r)
}

// A timeout, malformed acknowledgement or server failure may follow acceptance.
// Recovery must conservatively clear the same dedup key at that receiver too.
func readinessProviderUncertain(route *readinessIncidentRouting, e readinessIncidentEventRecord, name string, r *MCPReadinessProviderResult) {
	if r != nil && name == "primary" && e.Kind == "alert.fired" && (r.Accepted || r.Failure == "transport_error" || r.Failure == "invalid_acknowledgement" || r.StatusCode >= 500) {
		route.PrimaryInformed = true
	}
}
