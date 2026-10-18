package managedpostgres

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/connectioncapacity"
)

// QualifiedConnectionAllocation is trusted operator evidence, supplied outside
// the customer API. SourceSHA256 identifies the independently retained provider
// or native qualification report. A number inferred from a compute class or
// max_connections is not an allocation. This is an attestation boundary, not
// automatic provider discovery or cryptographic verification of that report.
type QualifiedConnectionAllocation struct {
	Version            int       `json:"version"`
	AccountID          string    `json:"account_id"`
	DatabaseID         string    `json:"database_id"`
	BackendID          string    `json:"backend_id"`
	BackendFingerprint string    `json:"backend_fingerprint"`
	ProviderResourceID string    `json:"provider_resource_id"`
	DataResourceID     string    `json:"data_resource_id"`
	Generation         int64     `json:"generation"`
	UsableConnections  int64     `json:"usable_connections"`
	Source             string    `json:"source"`
	SourceSHA256       string    `json:"source_sha256"`
	CheckedAt          time.Time `json:"checked_at"`
	ExpiresAt          time.Time `json:"expires_at"`
}

// DecodeConnectionAllocation bounds operator input and refuses unknown fields
// or trailing documents. Its contents still need exact target/freshness checks.
func DecodeConnectionAllocation(reader io.Reader) (*QualifiedConnectionAllocation, error) {
	data, err := io.ReadAll(io.LimitReader(reader, api.DataAPIConnectionAllocationMaxBytes+1))
	if err != nil || len(data) > api.DataAPIConnectionAllocationMaxBytes {
		return nil, ErrInvalid
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var allocation QualifiedConnectionAllocation
	if err := decoder.Decode(&allocation); err != nil {
		return nil, ErrInvalid
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return nil, ErrInvalid
	}
	return &allocation, nil
}

func (a *QualifiedConnectionAllocation) matches(d Database, now time.Time) bool {
	return a != nil && a.Version == 1 && a.AccountID == d.AccountID && a.DatabaseID == d.ID &&
		a.BackendID == d.BackendID && a.BackendFingerprint == d.BackendFingerprint &&
		a.ProviderResourceID == d.ProviderResourceID && a.DataResourceID == d.DataResourceID && a.Generation == d.DesiredGeneration &&
		a.UsableConnections > 0 && a.UsableConnections <= api.DataAPIConnectionBudgetMaxCount &&
		(a.Source == "provider_api" || a.Source == "native_qualification") && validSHA256(a.SourceSHA256) &&
		!a.CheckedAt.IsZero() && !now.Before(a.CheckedAt) && now.Before(a.ExpiresAt) && a.ExpiresAt.After(a.CheckedAt) &&
		a.ExpiresAt.Sub(a.CheckedAt) <= api.DataAPIConnectionAllocationMaxAge
}

// ConnectionCapacityProvider must verify the exact dataset and direct endpoint
// before and after a bounded read-only SQL observation. It must not issue roles,
// alter grants or query through a pooled/default-branch alias.
type ConnectionCapacityProvider interface {
	ObserveConnectionCapacity(context.Context, Database) (connectioncapacity.Observation, error)
}

type ConnectionCapacityResult struct {
	State     string
	Reason    string
	CheckedAt time.Time
	ExpiresAt time.Time
	Evidence  *ConnectionBudgetCapacityEvidence
}

type ConnectionCapacityStore interface {
	Get(context.Context, string, string) (Database, error)
	ClaimConnectionCapacity(context.Context, Database, string, time.Time, time.Time) error
	FinishConnectionCapacity(context.Context, Database, string, ConnectionCapacityResult, time.Time) error
}

// ObserveConnectionCapacity is an operator worker boundary. Management GETs
// only read its durable cache. Claiming immediately invalidates old evidence;
// failures therefore cannot silently continue serving a previous success.
func ObserveConnectionCapacity(ctx context.Context, registry *Registry, store ConnectionCapacityStore, accountID, databaseID string, allocation *QualifiedConnectionAllocation) (ConnectionCapacityResult, error) {
	if registry == nil || store == nil {
		return ConnectionCapacityResult{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, api.DataAPIConnectionCapacityTimeout)
	defer cancel()
	d, err := store.Get(ctx, accountID, databaseID)
	if err != nil {
		return ConnectionCapacityResult{}, err
	}
	if d.State != StateReady || d.DesiredGeneration != d.ObservedGeneration || d.DataResourceID == "" {
		return ConnectionCapacityResult{}, ErrConflict
	}
	now := time.Now().UTC()
	token := uuid.NewString()
	if err := store.ClaimConnectionCapacity(ctx, d, token, now, now.Add(api.DataAPIConnectionCapacityLease)); err != nil {
		return ConnectionCapacityResult{}, err
	}
	result := observeConnectionCapacity(ctx, registry, d, allocation, now)
	// A canceled SQL read still invalidates the old cache. The claim already
	// did so durably; leave its lease to expire if shutdown prevents finishing.
	if err := store.FinishConnectionCapacity(ctx, d, token, result, time.Now().UTC()); err != nil {
		return ConnectionCapacityResult{}, err
	}
	return result, nil
}

func observeConnectionCapacity(ctx context.Context, registry *Registry, d Database, allocation *QualifiedConnectionAllocation, now time.Time) ConnectionCapacityResult {
	out := ConnectionCapacityResult{State: "unknown", Reason: "allocation_unverified", CheckedAt: now, ExpiresAt: now}
	if !allocation.matches(d, now) {
		return out
	}
	backend, err := registry.Resolve(d.BackendID, d.BackendFingerprint)
	if err != nil {
		out.Reason = "backend_unavailable"
		return out
	}
	provider, ok := backend.Provider.(ConnectionCapacityProvider)
	if !ok {
		out.Reason = "observer_unsupported"
		return out
	}
	observed, err := provider.ObserveConnectionCapacity(ctx, d)
	if err != nil || !observed.Valid() || observed.MaxConnections > api.DataAPIConnectionBudgetMaxCount {
		out.Reason = "sql_capacity_unavailable"
		return out
	}
	expires := minTime(now.Add(api.DataAPIConnectionBudgetMaxAge), allocation.ExpiresAt)
	if !time.Now().Before(expires) {
		out.Reason = "observation_expired"
		return out
	}
	out.State, out.Reason, out.ExpiresAt = "verified", "", expires
	out.Evidence = &ConnectionBudgetCapacityEvidence{DatabaseID: d.ID, BackendFingerprint: d.BackendFingerprint,
		Generation: d.DesiredGeneration, UsableConnections: observed.Ceiling(allocation.UsableConnections),
		CheckedAt: now, ExpiresAt: expires, Source: allocation.Source, SourceSHA256: allocation.SourceSHA256,
		AllocationCheckedAt: allocation.CheckedAt, AllocationExpiresAt: allocation.ExpiresAt, AllocationConnections: allocation.UsableConnections, SQL: &observed}
	return out
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
