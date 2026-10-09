package managedpostgres

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
)

// AccountingReconciliationRequest attests a retained provider identity and
// actual shutdown, never absence inferred from a failed provider lookup.
type AccountingReconciliationRequest struct {
	ReconciliationID   string    `json:"reconciliation_id"`
	DatabaseID         string    `json:"database_id"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	ProviderResourceID string    `json:"provider_resource_id"`
	ShutdownAt         time.Time `json:"shutdown_at"`
	ObservedAt         time.Time `json:"observed_at"`
	EvidenceReference  string    `json:"evidence_reference"`
	EvidenceSHA256     string    `json:"evidence_sha256"`
	Reason             string    `json:"reason"`
	ExpectedRevision   string    `json:"expected_revision,omitempty"`
}

type AccountingReconciliationResult struct {
	ReconciliationID      string     `json:"reconciliation_id"`
	DatabaseID            string     `json:"database_id"`
	Revision              string     `json:"revision"`
	Applied               bool       `json:"applied"`
	PreviousDeletedAt     *time.Time `json:"previous_deleted_at,omitempty"`
	ShutdownAt            time.Time  `json:"shutdown_at"`
	ObservedAt            time.Time  `json:"observed_at"`
	SharedAccounting      bool       `json:"shared_accounting"`
	RequiresUsageRecovery bool       `json:"requires_usage_recovery"`
}

type AccountingReconciliationCommand struct {
	Request          AccountingReconciliationRequest
	ActorID          string
	Expected         Database
	Policy           UsagePolicy
	Meters           []Meter
	SharedAccounting bool
	Now              time.Time
}

type AccountingReconciliationStore interface {
	ReconcileAccounting(context.Context, AccountingReconciliationCommand, bool) (AccountingReconciliationResult, error)
	ReplayAccountingReconciliation(context.Context, string, string, AccountingReconciliationRequest) (AccountingReconciliationResult, error)
}

func (s *Service) ReconcileAccounting(ctx context.Context, accountID, actorID string, request AccountingReconciliationRequest, apply bool) (AccountingReconciliationResult, error) {
	store, ok := s.store.(AccountingReconciliationStore)
	if !ok {
		return AccountingReconciliationResult{}, ErrUnsupported
	}
	now := s.now().UTC()
	if err := validateAccountingReconciliation(request, actorID, now, apply); err != nil {
		return AccountingReconciliationResult{}, err
	}
	id, _ := uuid.Parse(request.ReconciliationID)
	databaseID, _ := uuid.Parse(request.DatabaseID)
	request.ReconciliationID, request.DatabaseID = id.String(), databaseID.String()
	if apply {
		result, err := store.ReplayAccountingReconciliation(ctx, accountID, actorID, request)
		if err == nil {
			return result, nil
		}
		if !errors.Is(err, ErrNotFound) {
			return AccountingReconciliationResult{}, err
		}
	}
	policy := s.registry.UsagePolicy()
	if !policy.Enabled {
		return AccountingReconciliationResult{}, ErrUnsupported
	}
	database, err := s.store.Get(ctx, accountID, request.DatabaseID)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	backend, err := s.registry.Resolve(database.BackendID, database.BackendFingerprint)
	if err != nil {
		return AccountingReconciliationResult{}, err
	}
	if len(backend.Capabilities.UsageMeters) == 0 {
		return AccountingReconciliationResult{}, ErrUnsupported
	}
	command := AccountingReconciliationCommand{Request: request, ActorID: actorID, Expected: database, Policy: policy,
		Meters: backend.Capabilities.UsageMeters, SharedAccounting: database.RestoreSourceDatabaseID != "" && backend.Capabilities.RestoreUsageIncludedInSource, Now: now}
	return store.ReconcileAccounting(ctx, command, apply)
}

func validateAccountingReconciliation(r AccountingReconciliationRequest, actor string, now time.Time, apply bool) error {
	if _, err := uuid.Parse(r.ReconciliationID); err != nil {
		return ErrInvalid
	}
	if _, err := uuid.Parse(r.DatabaseID); err != nil {
		return ErrInvalid
	}
	if !validImportText(actor, 256) || !validImportText(r.BackendID, 128) || !validSHA256(r.BackendFingerprint) ||
		!validImportText(r.ProviderResourceID, 1024) || !validImportText(r.EvidenceReference, 256) ||
		!validSHA256(r.EvidenceSHA256) || !validImportText(r.Reason, 512) || now.IsZero() || r.ShutdownAt.IsZero() ||
		r.ObservedAt.Before(r.ShutdownAt) || r.ObservedAt.After(now) || r.ShutdownAt.Nanosecond()%1000 != 0 ||
		r.ObservedAt.Nanosecond()%1000 != 0 || (apply && !validSHA256(r.ExpectedRevision)) || (!apply && r.ExpectedRevision != "") {
		return ErrInvalid
	}
	return nil
}

func accountingReconciliationHash(actor string, request AccountingReconciliationRequest) string {
	return importHash(struct {
		Actor   string
		Request AccountingReconciliationRequest
	}{actor, request})
}
