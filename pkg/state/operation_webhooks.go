// adr: 1193
package state

import (
	"context"
	"crypto/subtle"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var (
	// ErrOperationWebhookVersionConflict reports a stale expected binding version.
	ErrOperationWebhookVersionConflict = errors.New("state: operation webhook binding version changed")
	// ErrOperationWebhookRoutingConflict reports another delivery mode on the endpoint.
	ErrOperationWebhookRoutingConflict = errors.New("state: endpoint already has another delivery mode")
	// ErrOperationWebhookBindingChanged means the binding changed after the
	// caller resolved its definition; resolve again before admitting.
	ErrOperationWebhookBindingChanged = errors.New("state: operation webhook binding changed during admission")
)

var operationWebhookName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,63}$`)

// OperationWebhookBinding routes one inbound webhook endpoint to a customer
// operation. The owner is fixed here, never derived from provider payload.
type OperationWebhookBinding struct {
	EndpointID       string
	AccountID        string
	AppID            string
	Scope            string
	OperationName    string
	PlatformTenantID string
	EventTypes       []string
	Version          int64
	CreatedAt        time.Time
	UpdatedAt        time.Time
}

// Admits reports whether an event type is in the binding's allowlist; an
// empty list admits every verified event.
func (b OperationWebhookBinding) Admits(eventType string) bool {
	return len(b.EventTypes) == 0 || slices.Contains(b.EventTypes, eventType)
}

type OperationWebhookBindingOptions struct {
	EndpointID       string
	AccountID        string
	AppID            string
	ExpectedVersion  int64
	Scope            string
	OperationName    string
	PlatformTenantID string
	EventTypes       []string
}

// WebhookOperationAdmission is one verified provider event for a bound
// endpoint. BindingVersion is the version the caller resolved against.
type WebhookOperationAdmission struct {
	EndpointID      string
	ProviderEventID string
	BodySHA256      [32]byte
	BindingVersion  int64
	Admission       OperationAdmission
}

type WebhookOperationReceipt struct {
	OperationID string
	AcceptedAt  time.Time
	Duplicate   bool
}

// OperationWebhookStore persists ADR-1193 bindings and admits exactly one
// operation per provider event, independently of later rebinding.
type OperationWebhookStore interface {
	SaveOperationWebhookBinding(context.Context, OperationWebhookBindingOptions) (OperationWebhookBinding, error)
	OperationWebhookBinding(context.Context, string) (OperationWebhookBinding, error)
	DeleteOperationWebhookBinding(ctx context.Context, endpointID, accountID string, version int64) error
	OperationWebhookReceipt(ctx context.Context, endpointID, providerEventID string) (WebhookOperationReceipt, [32]byte, error)
	AdmitWebhookOperation(context.Context, WebhookOperationAdmission) (WebhookOperationReceipt, error)
}

func validateOperationWebhookBinding(opts OperationWebhookBindingOptions) error {
	for _, id := range []string{opts.EndpointID, opts.AccountID, opts.AppID, opts.PlatformTenantID} {
		if _, err := operationUUID(id); err != nil {
			return ErrInvalidArgument
		}
	}
	if opts.ExpectedVersion < 0 || api.ValidateScope(opts.Scope) != nil || !operationWebhookName.MatchString(opts.OperationName) || len(opts.EventTypes) > api.OperationWebhookEventTypesMax {
		return ErrInvalidArgument
	}
	seen := map[string]bool{}
	for _, eventType := range opts.EventTypes {
		if !api.ValidInboundWebhookEventType(eventType) || seen[eventType] {
			return ErrInvalidArgument
		}
		seen[eventType] = true
	}
	return nil
}

func validateWebhookOperationAdmission(in WebhookOperationAdmission) error {
	if _, err := operationUUID(in.EndpointID); err != nil || !api.ValidInboundWebhookEventID(in.ProviderEventID) || in.BindingVersion <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

// PostgreSQL text rejects NUL. The fixed-width endpoint UUID keeps the key
// unambiguous although provider event IDs may contain the separator.
func operationWebhookEventLock(endpointID, providerEventID string) string {
	return "customer-operation-webhook/" + endpointID + "/" + providerEventID
}

func operationWebhookBindingFromSQL(row sqlc.CustomerOperationWebhookBinding) OperationWebhookBinding {
	return OperationWebhookBinding{EndpointID: pgUUIDString(row.EndpointID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID),
		Scope: row.Scope, OperationName: row.OperationName, PlatformTenantID: pgUUIDString(row.PlatformTenantID), EventTypes: slices.Clone(row.EventTypes),
		Version: row.Version, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
}

func (s *PgStore) SaveOperationWebhookBinding(ctx context.Context, opts OperationWebhookBindingOptions) (OperationWebhookBinding, error) {
	if err := validateOperationWebhookBinding(opts); err != nil {
		return OperationWebhookBinding{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return OperationWebhookBinding{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	endpoint, account, app, tenant := mustPgUUID(opts.EndpointID), mustPgUUID(opts.AccountID), mustPgUUID(opts.AppID), mustPgUUID(opts.PlatformTenantID)
	// The endpoint lock serializes versioned writers, as the routing guard does.
	if _, err := q.LockWebhookAutomationEndpoint(ctx, tx, sqlc.LockWebhookAutomationEndpointParams{ID: endpoint, AppID: app, AccountID: account}); err != nil {
		return OperationWebhookBinding{}, mapErr(err)
	}
	owner, err := q.OperationWebhookTenantStatus(ctx, tx, sqlc.OperationWebhookTenantStatusParams{AppID: app, AccountID: account, TenantID: tenant})
	if err != nil {
		return OperationWebhookBinding{}, mapErr(err)
	}
	if owner.Status != string(PlatformTenantActive) {
		return OperationWebhookBinding{}, ErrPlatformTenantSuspended
	}
	if !owner.Linked {
		return OperationWebhookBinding{}, ErrNotFound
	}
	prior, err := q.GetOperationWebhookBinding(ctx, tx, endpoint)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return OperationWebhookBinding{}, err
	}
	if prior.Version != opts.ExpectedVersion {
		return OperationWebhookBinding{}, ErrOperationWebhookVersionConflict
	}
	row, err := q.SaveOperationWebhookBinding(ctx, tx, sqlc.SaveOperationWebhookBindingParams{EndpointID: endpoint, AccountID: account, AppID: app,
		Scope: opts.Scope, OperationName: opts.OperationName, PlatformTenantID: tenant, EventTypes: append([]string{}, opts.EventTypes...)})
	if isUniqueViolation(err) {
		return OperationWebhookBinding{}, ErrOperationWebhookRoutingConflict
	}
	if err != nil {
		return OperationWebhookBinding{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return OperationWebhookBinding{}, err
	}
	return operationWebhookBindingFromSQL(row), nil
}

func (s *PgStore) OperationWebhookBinding(ctx context.Context, endpointID string) (OperationWebhookBinding, error) {
	endpoint, err := operationUUID(endpointID)
	if err != nil {
		return OperationWebhookBinding{}, ErrNotFound
	}
	row, err := sqlc.New().GetOperationWebhookBinding(ctx, s.pool, endpoint)
	if err != nil {
		return OperationWebhookBinding{}, mapErr(err)
	}
	return operationWebhookBindingFromSQL(row), nil
}

func (s *PgStore) DeleteOperationWebhookBinding(ctx context.Context, endpointID, accountID string, version int64) error {
	endpoint, err := operationUUID(endpointID)
	if err != nil {
		return ErrNotFound
	}
	account, err := operationUUID(accountID)
	if err != nil {
		return ErrNotFound
	}
	q := sqlc.New()
	rows, err := q.DeleteOperationWebhookBinding(ctx, s.pool, sqlc.DeleteOperationWebhookBindingParams{EndpointID: endpoint, AccountID: account, Version: version})
	if err != nil || rows == 1 {
		return err
	}
	current, err := q.GetOperationWebhookBinding(ctx, s.pool, endpoint)
	if err != nil {
		return mapErr(err)
	}
	if pgUUIDString(current.AccountID) != accountID {
		return ErrNotFound
	}
	return ErrOperationWebhookVersionConflict
}

// OperationWebhookReceipt is a read-only duplicate check. Callers use it to
// acknowledge a provider retry before resolving definitions or admission.
func (s *PgStore) OperationWebhookReceipt(ctx context.Context, endpointID, providerEventID string) (WebhookOperationReceipt, [32]byte, error) {
	endpoint, err := operationUUID(endpointID)
	if err != nil {
		return WebhookOperationReceipt{}, [32]byte{}, ErrNotFound
	}
	row, err := sqlc.New().GetOperationWebhookReceipt(ctx, s.pool, sqlc.GetOperationWebhookReceiptParams{EndpointID: endpoint, ProviderEventID: providerEventID})
	if err != nil {
		return WebhookOperationReceipt{}, [32]byte{}, mapErr(err)
	}
	return operationWebhookReceiptFromSQL(row)
}

func operationWebhookReceiptFromSQL(row sqlc.CustomerOperationWebhookReceipt) (WebhookOperationReceipt, [32]byte, error) {
	var digest [32]byte
	if len(row.BodySha256) != len(digest) {
		return WebhookOperationReceipt{}, digest, fmt.Errorf("state: operation webhook receipt digest is %d bytes", len(row.BodySha256))
	}
	copy(digest[:], row.BodySha256)
	return WebhookOperationReceipt{OperationID: pgUUIDString(row.OperationID), AcceptedAt: row.AcceptedAt.Time, Duplicate: true}, digest, nil
}

// AdmitWebhookOperation commits the webhook receipt with the operation's own
// admission, idempotency receipt and initial event (ADR-521, ADR-1193).
func (s *PgStore) AdmitWebhookOperation(ctx context.Context, in WebhookOperationAdmission) (WebhookOperationReceipt, error) {
	if err := validateWebhookOperationAdmission(in); err != nil {
		return WebhookOperationReceipt{}, err
	}
	admission := in.Admission
	account, err := operationUUID(admission.AccountID)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	definition, err := operationUUID(admission.DefinitionID)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	tenant, err := operationUUID(admission.PlatformTenantID)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	hint, err := s.OperationDefinitionByID(ctx, admission.AccountID, admission.DefinitionID)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	endpoint := mustPgUUID(in.EndpointID)
	if err := q.LockOperationWebhookEvent(ctx, tx, operationWebhookEventLock(in.EndpointID, in.ProviderEventID)); err != nil {
		return WebhookOperationReceipt{}, err
	}
	// The receipt answers a retry even after its binding was removed.
	prior, err := q.GetOperationWebhookReceipt(ctx, tx, sqlc.GetOperationWebhookReceiptParams{EndpointID: endpoint, ProviderEventID: in.ProviderEventID})
	if err == nil {
		receipt, digest, err := operationWebhookReceiptFromSQL(prior)
		if err != nil {
			return WebhookOperationReceipt{}, err
		}
		if subtle.ConstantTimeCompare(digest[:], in.BodySHA256[:]) != 1 {
			return WebhookOperationReceipt{}, ErrOperationInputConflict
		}
		return receipt, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return WebhookOperationReceipt{}, err
	}
	binding, err := q.LockOperationWebhookBinding(ctx, tx, endpoint)
	if err != nil {
		return WebhookOperationReceipt{}, mapErr(err)
	}
	if binding.Version != in.BindingVersion {
		return WebhookOperationReceipt{}, ErrOperationWebhookBindingChanged
	}
	if pgUUIDString(binding.AccountID) != admission.AccountID || pgUUIDString(binding.PlatformTenantID) != admission.PlatformTenantID || pgUUIDString(binding.AppID) != hint.AppID {
		return WebhookOperationReceipt{}, ErrOperationWebhookBindingChanged
	}
	if _, err := q.DeleteExpiredOperationWebhookReceipts(ctx, tx, sqlc.DeleteExpiredOperationWebhookReceiptsParams{EndpointID: endpoint, PageLimit: api.OperationWebhookReceiptCleanupBatch}); err != nil {
		return WebhookOperationReceipt{}, err
	}
	op, created, err := s.admitOperationTx(ctx, tx, account, definition, tenant, admission, hint)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	expires := time.Now().UTC().Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds) * time.Second)
	rows, err := q.InsertOperationWebhookReceipt(ctx, tx, sqlc.InsertOperationWebhookReceiptParams{EndpointID: endpoint, ProviderEventID: in.ProviderEventID, BodySha256: in.BodySHA256[:],
		AccountID: account, OperationID: mustPgUUID(op.ID), BindingVersion: binding.Version, ExpiresAt: pgtype.Timestamptz{Time: expires, Valid: true}})
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	if rows != 1 {
		return WebhookOperationReceipt{}, ErrConflict
	}
	if err := tx.Commit(ctx); err != nil {
		return WebhookOperationReceipt{}, fmt.Errorf("state: commit webhook operation admission: %w", err)
	}
	return WebhookOperationReceipt{OperationID: op.ID, AcceptedAt: op.CreatedAt, Duplicate: !created}, nil
}

type operationWebhookReceipt struct {
	WebhookOperationReceipt
	AccountID      string
	BodySHA256     [32]byte
	BindingVersion int64
	ExpiresAt      time.Time
}

func operationWebhookReceiptKey(endpointID, providerEventID string) string {
	return endpointID + "\x00" + providerEventID
}

func (m *MemStore) SaveOperationWebhookBinding(_ context.Context, opts OperationWebhookBindingOptions) (OperationWebhookBinding, error) {
	if err := validateOperationWebhookBinding(opts); err != nil {
		return OperationWebhookBinding{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, ok := m.inboundWebhookEndpoints[opts.EndpointID]
	if !ok || endpoint.AppID != opts.AppID || endpoint.AccountID != opts.AccountID {
		return OperationWebhookBinding{}, ErrNotFound
	}
	tenant, ok := m.platformTenants[opts.PlatformTenantID]
	if !ok || tenant.AccountID != opts.AccountID {
		return OperationWebhookBinding{}, ErrNotFound
	}
	if tenant.Status != PlatformTenantActive {
		return OperationWebhookBinding{}, ErrPlatformTenantSuspended
	}
	linked := false
	for _, surface := range m.tenantSurfaces {
		if surface.AccountID == opts.AccountID && m.platformTenantBySurface[surface.ID] == opts.PlatformTenantID && surface.AppID == opts.AppID && surface.Status == SurfaceStatusActive {
			linked = true
			break
		}
	}
	if !linked {
		return OperationWebhookBinding{}, ErrNotFound
	}
	data := m.operationWebhookMemoryLocked()
	prior := data.webhookBindings[opts.EndpointID]
	if prior.Version != opts.ExpectedVersion {
		return OperationWebhookBinding{}, ErrOperationWebhookVersionConflict
	}
	if _, exists := m.webhookAutomationBindings[opts.EndpointID]; exists {
		return OperationWebhookBinding{}, ErrOperationWebhookRoutingConflict
	}
	if _, exists := m.exclusiveTriggerBindings["inbound_webhook\x00"+opts.EndpointID]; exists {
		return OperationWebhookBinding{}, ErrOperationWebhookRoutingConflict
	}
	now := time.Now().UTC()
	data.webhookBindingRevision++
	binding := OperationWebhookBinding{EndpointID: opts.EndpointID, AccountID: opts.AccountID, AppID: opts.AppID, Scope: opts.Scope, OperationName: opts.OperationName,
		PlatformTenantID: opts.PlatformTenantID, EventTypes: slices.Clone(opts.EventTypes), Version: data.webhookBindingRevision, CreatedAt: now, UpdatedAt: now}
	if prior.Version != 0 {
		binding.CreatedAt = prior.CreatedAt
	}
	data.webhookBindings[opts.EndpointID] = binding
	binding.EventTypes = slices.Clone(binding.EventTypes)
	return binding, nil
}

func (m *MemStore) OperationWebhookBinding(_ context.Context, endpointID string) (OperationWebhookBinding, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	binding, ok := m.operationWebhookMemoryLocked().webhookBindings[endpointID]
	if !ok {
		return OperationWebhookBinding{}, ErrNotFound
	}
	binding.EventTypes = slices.Clone(binding.EventTypes)
	return binding, nil
}

func (m *MemStore) DeleteOperationWebhookBinding(_ context.Context, endpointID, accountID string, version int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationWebhookMemoryLocked()
	binding, ok := data.webhookBindings[endpointID]
	if !ok || binding.AccountID != accountID {
		return ErrNotFound
	}
	if binding.Version != version {
		return ErrOperationWebhookVersionConflict
	}
	delete(data.webhookBindings, endpointID)
	return nil
}

func (m *MemStore) OperationWebhookReceipt(_ context.Context, endpointID, providerEventID string) (WebhookOperationReceipt, [32]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	receipt, ok := m.operationWebhookMemoryLocked().webhookReceipts[operationWebhookReceiptKey(endpointID, providerEventID)]
	if !ok || !receipt.ExpiresAt.After(time.Now().UTC()) {
		return WebhookOperationReceipt{}, [32]byte{}, ErrNotFound
	}
	return receipt.WebhookOperationReceipt, receipt.BodySHA256, nil
}

func (m *MemStore) AdmitWebhookOperation(ctx context.Context, in WebhookOperationAdmission) (WebhookOperationReceipt, error) {
	if err := validateWebhookOperationAdmission(in); err != nil {
		return WebhookOperationReceipt{}, err
	}
	releaseErr, err := m.operationReleaseAdmission(ctx, in.Admission)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	data := m.operationWebhookMemoryLocked()
	now := time.Now().UTC()
	key := operationWebhookReceiptKey(in.EndpointID, in.ProviderEventID)
	if prior, exists := data.webhookReceipts[key]; exists && prior.ExpiresAt.After(now) {
		if subtle.ConstantTimeCompare(prior.BodySHA256[:], in.BodySHA256[:]) != 1 {
			return WebhookOperationReceipt{}, ErrOperationInputConflict
		}
		return prior.WebhookOperationReceipt, nil
	}
	binding, ok := data.webhookBindings[in.EndpointID]
	if !ok {
		return WebhookOperationReceipt{}, ErrNotFound
	}
	if binding.Version != in.BindingVersion || binding.AccountID != in.Admission.AccountID || binding.PlatformTenantID != in.Admission.PlatformTenantID {
		return WebhookOperationReceipt{}, ErrOperationWebhookBindingChanged
	}
	op, created, err := m.admitOperationLocked(in.Admission, releaseErr)
	if err != nil {
		return WebhookOperationReceipt{}, err
	}
	accepted := WebhookOperationReceipt{OperationID: op.ID, AcceptedAt: op.CreatedAt, Duplicate: true}
	data.webhookReceipts[key] = operationWebhookReceipt{WebhookOperationReceipt: accepted, AccountID: op.AccountID, BodySHA256: in.BodySHA256, BindingVersion: binding.Version,
		ExpiresAt: now.Add(time.Duration(op.PlanLimits.IdempotencyRetentionSeconds) * time.Second)}
	accepted.Duplicate = !created
	return accepted, nil
}

type operationWebhookMemory struct {
	webhookBindings        map[string]OperationWebhookBinding
	webhookReceipts        map[string]operationWebhookReceipt
	webhookBindingRevision int64
}

func (m *MemStore) operationWebhookMemoryLocked() *operationWebhookMemory {
	data := m.operationMemoryLocked()
	if data.webhooks == nil {
		data.webhooks = &operationWebhookMemory{webhookBindings: map[string]OperationWebhookBinding{}, webhookReceipts: map[string]operationWebhookReceipt{}}
	}
	return data.webhooks
}

// deleteOperationWebhookEndpointLocked mirrors the endpoint foreign-key
// cascade; operations admitted through the endpoint are retained.
func (m *MemStore) deleteOperationWebhookEndpointLocked(endpointID string) {
	data := m.operationWebhookMemoryLocked()
	delete(data.webhookBindings, endpointID)
	for key := range data.webhookReceipts {
		if len(key) > len(endpointID) && key[:len(endpointID)+1] == endpointID+"\x00" {
			delete(data.webhookReceipts, key)
		}
	}
}

// operationWebhookBoundLocked lets the other routing modes honor the guard.
func (m *MemStore) operationWebhookBoundLocked(endpointID string) bool {
	_, ok := m.operationWebhookMemoryLocked().webhookBindings[endpointID]
	return ok
}
