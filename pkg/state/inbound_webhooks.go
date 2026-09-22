package state

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
)

type InboundWebhookProvider string

const InboundWebhookProviderStripe InboundWebhookProvider = "stripe"

type InboundWebhookEndpoint struct {
	ID                  string
	AppID               string
	AccountID           string
	Name                string
	Provider            InboundWebhookProvider
	TokenHash           []byte
	SigningSecretSealed []byte
	DeliveryPath        string
	Enabled             bool
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type UpdateInboundWebhookEndpointParams struct {
	SigningSecretSealed *[]byte
	DeliveryPath        *string
	Enabled             *bool
}

type InboundWebhookQuotaScope string

const (
	InboundWebhookQuotaScopeApp     InboundWebhookQuotaScope = "app"
	InboundWebhookQuotaScopeAccount InboundWebhookQuotaScope = "account"
)

type InboundWebhookQuotaError struct {
	Scope    InboundWebhookQuotaScope
	Limit    int
	Observed int
}

func (e *InboundWebhookQuotaError) Error() string {
	return fmt.Sprintf("state: inbound webhook quota exceeded (scope=%s, limit=%d, observed=%d)", e.Scope, e.Limit, e.Observed)
}

// InboundWebhookStore is optional so older Store fakes remain source
// compatible. MemStore and PgStore both implement the complete contract.
type InboundWebhookStore interface {
	CreateInboundWebhookEndpointIfUnderQuota(context.Context, InboundWebhookEndpoint, api.Limits) (InboundWebhookEndpoint, error)
	ListInboundWebhookEndpointsForApp(context.Context, string) ([]InboundWebhookEndpoint, error)
	InboundWebhookEndpointByID(context.Context, string) (InboundWebhookEndpoint, error)
	InboundWebhookEndpointByTokenHash(context.Context, []byte) (InboundWebhookEndpoint, error)
	UpdateInboundWebhookEndpoint(context.Context, string, UpdateInboundWebhookEndpointParams) (InboundWebhookEndpoint, error)
	DeleteInboundWebhookEndpoint(context.Context, string) error
}

func (m *MemStore) CreateInboundWebhookEndpointIfUnderQuota(_ context.Context, in InboundWebhookEndpoint, limits api.Limits) (InboundWebhookEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[in.AppID]
	if !ok || app.Status == AppDeleted || app.AccountID != in.AccountID {
		return InboundWebhookEndpoint{}, ErrNotFound
	}
	if m.inboundWebhookEndpoints == nil {
		m.inboundWebhookEndpoints = make(map[string]InboundWebhookEndpoint)
	}
	appCount, accountCount := 0, 0
	for _, endpoint := range m.inboundWebhookEndpoints {
		if endpoint.AppID == in.AppID {
			appCount++
			if endpoint.Name == in.Name {
				return InboundWebhookEndpoint{}, ErrConflict
			}
		}
		if endpoint.AccountID == in.AccountID {
			accountCount++
		}
		if bytes.Equal(endpoint.TokenHash, in.TokenHash) {
			return InboundWebhookEndpoint{}, ErrConflict
		}
	}
	if limits.InboundWebhookPerApp <= 0 || appCount >= limits.InboundWebhookPerApp {
		return InboundWebhookEndpoint{}, &InboundWebhookQuotaError{Scope: InboundWebhookQuotaScopeApp, Limit: limits.InboundWebhookPerApp, Observed: appCount}
	}
	if limits.InboundWebhookPerAccount <= 0 || accountCount >= limits.InboundWebhookPerAccount {
		return InboundWebhookEndpoint{}, &InboundWebhookQuotaError{Scope: InboundWebhookQuotaScopeAccount, Limit: limits.InboundWebhookPerAccount, Observed: accountCount}
	}
	if in.ID == "" {
		in.ID = newID()
	}
	if in.DeliveryPath == "" {
		in.DeliveryPath = "/"
	}
	if in.CreatedAt.IsZero() {
		in.CreatedAt = time.Now().UTC()
	}
	in.UpdatedAt = in.CreatedAt
	in.TokenHash = bytes.Clone(in.TokenHash)
	in.SigningSecretSealed = bytes.Clone(in.SigningSecretSealed)
	m.inboundWebhookEndpoints[in.ID] = in
	return in, nil
}

func (m *MemStore) ListInboundWebhookEndpointsForApp(_ context.Context, appID string) ([]InboundWebhookEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]InboundWebhookEndpoint, 0)
	for _, endpoint := range m.inboundWebhookEndpoints {
		if endpoint.AppID == appID {
			out = append(out, cloneInboundWebhookEndpoint(endpoint))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].CreatedAt.Equal(out[j].CreatedAt) {
			return out[i].ID < out[j].ID
		}
		return out[i].CreatedAt.Before(out[j].CreatedAt)
	})
	return out, nil
}

func (m *MemStore) InboundWebhookEndpointByID(_ context.Context, id string) (InboundWebhookEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, ok := m.inboundWebhookEndpoints[id]
	if !ok {
		return InboundWebhookEndpoint{}, ErrNotFound
	}
	return cloneInboundWebhookEndpoint(endpoint), nil
}

func (m *MemStore) InboundWebhookEndpointByTokenHash(_ context.Context, tokenHash []byte) (InboundWebhookEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, endpoint := range m.inboundWebhookEndpoints {
		if bytes.Equal(endpoint.TokenHash, tokenHash) {
			app, exists := m.apps[endpoint.AppID]
			if !exists || app.Status == AppDeleted {
				return InboundWebhookEndpoint{}, ErrNotFound
			}
			return cloneInboundWebhookEndpoint(endpoint), nil
		}
	}
	return InboundWebhookEndpoint{}, ErrNotFound
}

func (m *MemStore) UpdateInboundWebhookEndpoint(_ context.Context, id string, params UpdateInboundWebhookEndpointParams) (InboundWebhookEndpoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	endpoint, ok := m.inboundWebhookEndpoints[id]
	if !ok {
		return InboundWebhookEndpoint{}, ErrNotFound
	}
	if params.SigningSecretSealed != nil {
		endpoint.SigningSecretSealed = bytes.Clone(*params.SigningSecretSealed)
	}
	if params.DeliveryPath != nil {
		endpoint.DeliveryPath = *params.DeliveryPath
	}
	if params.Enabled != nil {
		endpoint.Enabled = *params.Enabled
	}
	endpoint.UpdatedAt = time.Now().UTC()
	m.inboundWebhookEndpoints[id] = endpoint
	return cloneInboundWebhookEndpoint(endpoint), nil
}

func (m *MemStore) DeleteInboundWebhookEndpoint(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.inboundWebhookEndpoints[id]; !ok {
		return ErrNotFound
	}
	delete(m.inboundWebhookEndpoints, id)
	return nil
}

func cloneInboundWebhookEndpoint(endpoint InboundWebhookEndpoint) InboundWebhookEndpoint {
	endpoint.TokenHash = bytes.Clone(endpoint.TokenHash)
	endpoint.SigningSecretSealed = bytes.Clone(endpoint.SigningSecretSealed)
	return endpoint
}

const inboundWebhookEndpointColumns = `id, app_id, account_id, name, provider,
       token_hash, signing_secret_sealed, delivery_path, enabled,
       created_at, updated_at`

type inboundWebhookEndpointScanner interface{ Scan(dest ...any) error }

func scanInboundWebhookEndpoint(row inboundWebhookEndpointScanner) (InboundWebhookEndpoint, error) {
	var endpoint InboundWebhookEndpoint
	var provider string
	err := row.Scan(
		&endpoint.ID, &endpoint.AppID, &endpoint.AccountID, &endpoint.Name, &provider,
		&endpoint.TokenHash, &endpoint.SigningSecretSealed, &endpoint.DeliveryPath,
		&endpoint.Enabled, &endpoint.CreatedAt, &endpoint.UpdatedAt,
	)
	endpoint.Provider = InboundWebhookProvider(provider)
	return endpoint, err
}

func (s *PgStore) CreateInboundWebhookEndpointIfUnderQuota(ctx context.Context, in InboundWebhookEndpoint, limits api.Limits) (InboundWebhookEndpoint, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: begin inbound webhook create: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var locked int
	if err := tx.QueryRow(ctx, `select 1 from apps where id=$1 and account_id=$2 and status <> 'deleted' for update`, in.AppID, in.AccountID).Scan(&locked); err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	// Serialize the account-wide count across concurrent creates on different
	// apps; the app row lock above alone protects only the per-app cap.
	if err := tx.QueryRow(ctx, `select 1 from accounts where id=$1 for update`, in.AccountID).Scan(&locked); err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	var appCount, accountCount int
	if err := tx.QueryRow(ctx, `select count(*) from inbound_webhook_endpoints where app_id=$1`, in.AppID).Scan(&appCount); err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: count inbound webhooks for app: %w", err)
	}
	if limits.InboundWebhookPerApp <= 0 || appCount >= limits.InboundWebhookPerApp {
		return InboundWebhookEndpoint{}, &InboundWebhookQuotaError{Scope: InboundWebhookQuotaScopeApp, Limit: limits.InboundWebhookPerApp, Observed: appCount}
	}
	if err := tx.QueryRow(ctx, `select count(*) from inbound_webhook_endpoints where account_id=$1`, in.AccountID).Scan(&accountCount); err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: count inbound webhooks for account: %w", err)
	}
	if limits.InboundWebhookPerAccount <= 0 || accountCount >= limits.InboundWebhookPerAccount {
		return InboundWebhookEndpoint{}, &InboundWebhookQuotaError{Scope: InboundWebhookQuotaScopeAccount, Limit: limits.InboundWebhookPerAccount, Observed: accountCount}
	}
	row := tx.QueryRow(ctx, `insert into inbound_webhook_endpoints
		(app_id,account_id,name,provider,token_hash,signing_secret_sealed,delivery_path,enabled)
		values ($1,$2,$3,$4,$5,$6,$7,$8) returning `+inboundWebhookEndpointColumns,
		in.AppID, in.AccountID, in.Name, string(in.Provider), in.TokenHash,
		in.SigningSecretSealed, in.DeliveryPath, in.Enabled)
	endpoint, err := scanInboundWebhookEndpoint(row)
	if err != nil {
		if isUniqueViolation(err) {
			return InboundWebhookEndpoint{}, ErrConflict
		}
		return InboundWebhookEndpoint{}, fmt.Errorf("state: insert inbound webhook endpoint: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: commit inbound webhook endpoint: %w", err)
	}
	return endpoint, nil
}

func (s *PgStore) ListInboundWebhookEndpointsForApp(ctx context.Context, appID string) ([]InboundWebhookEndpoint, error) {
	rows, err := s.pool.Query(ctx, `select `+inboundWebhookEndpointColumns+` from inbound_webhook_endpoints where app_id=$1 order by created_at,id`, appID)
	if err != nil {
		return nil, fmt.Errorf("state: list inbound webhook endpoints: %w", err)
	}
	defer rows.Close()
	out := make([]InboundWebhookEndpoint, 0)
	for rows.Next() {
		endpoint, scanErr := scanInboundWebhookEndpoint(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("state: scan inbound webhook endpoint: %w", scanErr)
		}
		out = append(out, endpoint)
	}
	return out, rows.Err()
}

func (s *PgStore) InboundWebhookEndpointByID(ctx context.Context, id string) (InboundWebhookEndpoint, error) {
	endpoint, err := scanInboundWebhookEndpoint(s.pool.QueryRow(ctx, `select `+inboundWebhookEndpointColumns+` from inbound_webhook_endpoints where id=$1`, id))
	if err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	return endpoint, nil
}

func (s *PgStore) InboundWebhookEndpointByTokenHash(ctx context.Context, tokenHash []byte) (InboundWebhookEndpoint, error) {
	endpoint, err := scanInboundWebhookEndpoint(s.pool.QueryRow(ctx, `select `+prefixedInboundWebhookEndpointColumns("w")+`
		from inbound_webhook_endpoints w
		join apps a on a.id=w.app_id and a.status <> 'deleted'
		where w.token_hash=$1`, tokenHash))
	if err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	return endpoint, nil
}

func prefixedInboundWebhookEndpointColumns(alias string) string {
	parts := strings.Split(inboundWebhookEndpointColumns, ",")
	for i := range parts {
		parts[i] = alias + "." + strings.TrimSpace(parts[i])
	}
	return strings.Join(parts, ", ")
}

func (s *PgStore) UpdateInboundWebhookEndpoint(ctx context.Context, id string, params UpdateInboundWebhookEndpointParams) (InboundWebhookEndpoint, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: begin inbound webhook update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	endpoint, err := scanInboundWebhookEndpoint(tx.QueryRow(ctx, `select `+inboundWebhookEndpointColumns+` from inbound_webhook_endpoints where id=$1 for update`, id))
	if err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	if params.SigningSecretSealed != nil {
		endpoint.SigningSecretSealed = *params.SigningSecretSealed
	}
	if params.DeliveryPath != nil {
		endpoint.DeliveryPath = *params.DeliveryPath
	}
	if params.Enabled != nil {
		endpoint.Enabled = *params.Enabled
	}
	endpoint, err = scanInboundWebhookEndpoint(tx.QueryRow(ctx, `update inbound_webhook_endpoints
		set signing_secret_sealed=$2,delivery_path=$3,enabled=$4,updated_at=now()
		where id=$1 returning `+inboundWebhookEndpointColumns,
		id, endpoint.SigningSecretSealed, endpoint.DeliveryPath, endpoint.Enabled))
	if err != nil {
		return InboundWebhookEndpoint{}, mapErr(err)
	}
	if err := tx.Commit(ctx); err != nil {
		return InboundWebhookEndpoint{}, fmt.Errorf("state: commit inbound webhook update: %w", err)
	}
	return endpoint, nil
}

func (s *PgStore) DeleteInboundWebhookEndpoint(ctx context.Context, id string) error {
	tag, err := s.pool.Exec(ctx, `delete from inbound_webhook_endpoints where id=$1`, id)
	if err != nil {
		return fmt.Errorf("state: delete inbound webhook endpoint: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

var _ InboundWebhookStore = (*MemStore)(nil)
var _ InboundWebhookStore = (*PgStore)(nil)
var _ error = (*InboundWebhookQuotaError)(nil)

func (e *InboundWebhookQuotaError) Is(target error) bool {
	_, ok := target.(*InboundWebhookQuotaError)
	return ok
}
