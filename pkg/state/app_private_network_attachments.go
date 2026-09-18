package state

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"sort"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppPrivateNetworkAttachment is the durable, provider-neutral attachment
// intent for one app. A connector may advance Status from pending to ready in
// a later runtime slice; callers must treat pending as fail-closed.
type AppPrivateNetworkAttachment struct {
	ID        string
	AccountID string
	AppID     string
	NetworkID string
	Region    string
	CIDRs     []netip.Prefix
	// AllowedCIDRs is an optional private-network policy. When non-empty,
	// only these destinations (and matching private ingress sources) are
	// admitted; an empty list preserves the historical network-wide allow.
	AllowedCIDRs []netip.Prefix
	Status       string
	StatusDetail string
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// AppPrivateNetworkAttachmentStore stays separate from Store so older state
// adapters and focused test doubles remain source-compatible while the
// provider connector is being developed.
type AppPrivateNetworkAttachmentStore interface {
	GetAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) (AppPrivateNetworkAttachment, error)
	UpsertAppPrivateNetworkAttachment(ctx context.Context, attachment AppPrivateNetworkAttachment) (AppPrivateNetworkAttachment, error)
	DeleteAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) error
}

// AppPrivateNetworkAttachmentReconcileStore is the optional extension used by
// the runtime connector. Keeping it separate means API-only state adapters do
// not need to implement a background-worker surface before they can serve the
// attachment intent endpoints.
type AppPrivateNetworkAttachmentReconcileStore interface {
	AppPrivateNetworkAttachmentStore
	ListAppPrivateNetworkAttachments(ctx context.Context, statuses []string, limit int) ([]AppPrivateNetworkAttachment, error)
	UpdateAppPrivateNetworkAttachmentStatus(ctx context.Context, accountID, appID, status, detail string) (AppPrivateNetworkAttachment, error)
}

var _ AppPrivateNetworkAttachmentStore = (*MemStore)(nil)
var _ AppPrivateNetworkAttachmentStore = (*PgStore)(nil)
var _ AppPrivateNetworkAttachmentReconcileStore = (*MemStore)(nil)
var _ AppPrivateNetworkAttachmentReconcileStore = (*PgStore)(nil)

func (m *MemStore) GetAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) (AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return AppPrivateNetworkAttachment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	attachment, ok := m.privateNetworkAttachments[appID]
	if !ok || attachment.AccountID != accountID {
		return AppPrivateNetworkAttachment{}, ErrNotFound
	}
	return clonePrivateNetworkAttachment(attachment), nil
}

func (m *MemStore) UpsertAppPrivateNetworkAttachment(ctx context.Context, attachment AppPrivateNetworkAttachment) (AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return AppPrivateNetworkAttachment{}, err
	}
	if attachment.AccountID == "" || attachment.AppID == "" || attachment.NetworkID == "" || attachment.Region == "" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if attachment.Status != "" && attachment.Status != "pending" && attachment.Status != "ready" && attachment.Status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if len(attachment.CIDRs) > 64 {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if len(attachment.AllowedCIDRs) > 64 {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.privateNetworkAttachments == nil {
		m.privateNetworkAttachments = make(map[string]AppPrivateNetworkAttachment)
	}
	if existing, ok := m.privateNetworkAttachments[attachment.AppID]; ok {
		if existing.AccountID != attachment.AccountID {
			return AppPrivateNetworkAttachment{}, fmt.Errorf("%w: app_private_network_attachments_account_key", ErrConflict)
		}
		attachment.ID = existing.ID
		attachment.CreatedAt = existing.CreatedAt
	}
	if attachment.ID == "" {
		attachment.ID = uuid.NewString()
	}
	if attachment.CreatedAt.IsZero() {
		attachment.CreatedAt = time.Now().UTC()
	}
	attachment.UpdatedAt = time.Now().UTC()
	attachment.CIDRs = append([]netip.Prefix(nil), attachment.CIDRs...)
	attachment.AllowedCIDRs = append([]netip.Prefix(nil), attachment.AllowedCIDRs...)
	m.privateNetworkAttachments[attachment.AppID] = attachment
	return clonePrivateNetworkAttachment(attachment), nil
}

func (m *MemStore) DeleteAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	attachment, ok := m.privateNetworkAttachments[appID]
	if !ok || attachment.AccountID != accountID {
		return ErrNotFound
	}
	delete(m.privateNetworkAttachments, appID)
	return nil
}

func (m *MemStore) ListAppPrivateNetworkAttachments(ctx context.Context, statuses []string, limit int) ([]AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	allowed := make(map[string]struct{}, len(statuses))
	for _, status := range statuses {
		if status != "pending" && status != "ready" && status != "error" {
			return nil, ErrInvalidArgument
		}
		allowed[status] = struct{}{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]AppPrivateNetworkAttachment, 0, len(m.privateNetworkAttachments))
	for _, attachment := range m.privateNetworkAttachments {
		if len(allowed) > 0 {
			if _, ok := allowed[attachment.Status]; !ok {
				continue
			}
		}
		out = append(out, clonePrivateNetworkAttachment(attachment))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].AppID < out[j].AppID
		}
		return out[i].UpdatedAt.Before(out[j].UpdatedAt)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) UpdateAppPrivateNetworkAttachmentStatus(ctx context.Context, accountID, appID, status, detail string) (AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return AppPrivateNetworkAttachment{}, err
	}
	if status != "pending" && status != "ready" && status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	attachment, ok := m.privateNetworkAttachments[appID]
	if !ok || attachment.AccountID != accountID {
		return AppPrivateNetworkAttachment{}, ErrNotFound
	}
	attachment.Status = status
	attachment.StatusDetail = detail
	attachment.UpdatedAt = time.Now().UTC()
	m.privateNetworkAttachments[appID] = attachment
	return clonePrivateNetworkAttachment(attachment), nil
}

func (s *PgStore) GetAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) (AppPrivateNetworkAttachment, error) {
	row := s.pool.QueryRow(ctx, `
		select id, account_id, app_id, network_id, region,
		       coalesce(cidrs::text, '{}'), coalesce(allowed_cidrs::text, '{}'), status, status_detail,
		       created_at, updated_at
		  from app_private_network_attachments
		 where account_id = $1 and app_id = $2`, accountID, appID)
	return scanAppPrivateNetworkAttachment(row)
}

func (s *PgStore) UpsertAppPrivateNetworkAttachment(ctx context.Context, attachment AppPrivateNetworkAttachment) (AppPrivateNetworkAttachment, error) {
	if attachment.AccountID == "" || attachment.AppID == "" || attachment.NetworkID == "" || attachment.Region == "" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if attachment.Status != "" && attachment.Status != "pending" && attachment.Status != "ready" && attachment.Status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if len(attachment.CIDRs) > 64 {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if len(attachment.AllowedCIDRs) > 64 {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	if attachment.Status == "" {
		attachment.Status = "pending"
	}
	if attachment.StatusDetail == "" {
		attachment.StatusDetail = "connector not provisioned; traffic remains blocked until the attachment is ready"
	}
	cidrs := make([]string, 0, len(attachment.CIDRs))
	for _, prefix := range attachment.CIDRs {
		cidrs = append(cidrs, prefix.String())
	}
	allowedCIDRs := make([]string, 0, len(attachment.AllowedCIDRs))
	for _, prefix := range attachment.AllowedCIDRs {
		allowedCIDRs = append(allowedCIDRs, prefix.String())
	}
	row := s.pool.QueryRow(ctx, `
		insert into app_private_network_attachments
		       (account_id, app_id, network_id, region, cidrs, allowed_cidrs, status, status_detail)
		values ($1, $2, $3, $4, $5::cidr[], $6::cidr[], $7, $8)
		on conflict (app_id) do update set
		       network_id = excluded.network_id,
		       region = excluded.region,
		       cidrs = excluded.cidrs,
		       allowed_cidrs = excluded.allowed_cidrs,
		       status = excluded.status,
		       status_detail = excluded.status_detail,
		       updated_at = now()
		 where app_private_network_attachments.account_id = excluded.account_id
		returning id, account_id, app_id, network_id, region,
		          coalesce(cidrs::text, '{}'), coalesce(allowed_cidrs::text, '{}'), status, status_detail,
		          created_at, updated_at`,
		attachment.AccountID, attachment.AppID, attachment.NetworkID, attachment.Region,
		cidrs, allowedCIDRs, attachment.Status, attachment.StatusDetail)
	return scanAppPrivateNetworkAttachment(row)
}

func (s *PgStore) DeleteAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) error {
	tag, err := s.pool.Exec(ctx,
		`delete from app_private_network_attachments where account_id = $1 and app_id = $2`, accountID, appID)
	if err != nil {
		return mapErr(err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) ListAppPrivateNetworkAttachments(ctx context.Context, statuses []string, limit int) ([]AppPrivateNetworkAttachment, error) {
	if limit <= 0 || limit > 1000 {
		return nil, ErrInvalidArgument
	}
	for _, status := range statuses {
		if status != "pending" && status != "ready" && status != "error" {
			return nil, ErrInvalidArgument
		}
	}
	rows, err := s.pool.Query(ctx, `
		select id, account_id, app_id, network_id, region,
		       coalesce(cidrs::text, '{}'), coalesce(allowed_cidrs::text, '{}'), status, status_detail,
		       created_at, updated_at
		  from app_private_network_attachments
		 where ($1::text[] is null or status = any($1::text[]))
		 order by updated_at asc
		 limit $2`, nullableStrings(statuses), limit)
	if err != nil {
		return nil, mapErr(err)
	}
	defer rows.Close()
	out := make([]AppPrivateNetworkAttachment, 0)
	for rows.Next() {
		attachment, scanErr := scanAppPrivateNetworkAttachment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		out = append(out, attachment)
	}
	if err := rows.Err(); err != nil {
		return nil, mapErr(err)
	}
	return out, nil
}

func (s *PgStore) UpdateAppPrivateNetworkAttachmentStatus(ctx context.Context, accountID, appID, status, detail string) (AppPrivateNetworkAttachment, error) {
	if status != "pending" && status != "ready" && status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	row := s.pool.QueryRow(ctx, `
		update app_private_network_attachments
		   set status = $3, status_detail = $4, updated_at = now()
		 where account_id = $1 and app_id = $2
		returning id, account_id, app_id, network_id, region,
		          coalesce(cidrs::text, '{}'), coalesce(allowed_cidrs::text, '{}'), status, status_detail,
		          created_at, updated_at`, accountID, appID, status, detail)
	return scanAppPrivateNetworkAttachment(row)
}

type privateNetworkAttachmentScanner interface {
	Scan(dest ...any) error
}

func scanAppPrivateNetworkAttachment(row privateNetworkAttachmentScanner) (AppPrivateNetworkAttachment, error) {
	var out AppPrivateNetworkAttachment
	var cidrText string
	var allowedCIDRText string
	if err := row.Scan(&out.ID, &out.AccountID, &out.AppID, &out.NetworkID, &out.Region,
		&cidrText, &allowedCIDRText, &out.Status, &out.StatusDetail, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return AppPrivateNetworkAttachment{}, ErrNotFound
		}
		return AppPrivateNetworkAttachment{}, mapErr(err)
	}
	out.CIDRs = cidrTextToPrefixes(cidrText)
	out.AllowedCIDRs = cidrTextToPrefixes(allowedCIDRText)
	return out, nil
}

func clonePrivateNetworkAttachment(in AppPrivateNetworkAttachment) AppPrivateNetworkAttachment {
	in.CIDRs = append([]netip.Prefix(nil), in.CIDRs...)
	in.AllowedCIDRs = append([]netip.Prefix(nil), in.AllowedCIDRs...)
	return in
}

func nullableStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	return values
}
