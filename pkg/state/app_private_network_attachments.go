package state

import (
	"context"
	"fmt"
	"net/netip"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AppPrivateNetworkAttachment is the durable, provider-neutral attachment
// intent for one app. A connector may advance Status from pending to ready in
// a later runtime slice; callers must treat pending as fail-closed.
type AppPrivateNetworkAttachment struct {
	ID           string
	AccountID    string
	AppID        string
	NetworkID    string
	Region       string
	CIDRs        []netip.Prefix
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

var _ AppPrivateNetworkAttachmentStore = (*MemStore)(nil)
var _ AppPrivateNetworkAttachmentStore = (*PgStore)(nil)

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

func (s *PgStore) GetAppPrivateNetworkAttachment(ctx context.Context, accountID, appID string) (AppPrivateNetworkAttachment, error) {
	row := s.pool.QueryRow(ctx, `
		select id, account_id, app_id, network_id, region,
		       coalesce(cidrs::text, '{}'), status, status_detail,
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
	row := s.pool.QueryRow(ctx, `
		insert into app_private_network_attachments
		       (account_id, app_id, network_id, region, cidrs, status, status_detail)
		values ($1, $2, $3, $4, $5::cidr[], $6, $7)
		on conflict (app_id) do update set
		       network_id = excluded.network_id,
		       region = excluded.region,
		       cidrs = excluded.cidrs,
		       status = excluded.status,
		       status_detail = excluded.status_detail,
		       updated_at = now()
		 where app_private_network_attachments.account_id = excluded.account_id
		returning id, account_id, app_id, network_id, region,
		          coalesce(cidrs::text, '{}'), status, status_detail,
		          created_at, updated_at`,
		attachment.AccountID, attachment.AppID, attachment.NetworkID, attachment.Region,
		cidrs, attachment.Status, attachment.StatusDetail)
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

type privateNetworkAttachmentScanner interface {
	Scan(dest ...any) error
}

func scanAppPrivateNetworkAttachment(row privateNetworkAttachmentScanner) (AppPrivateNetworkAttachment, error) {
	var out AppPrivateNetworkAttachment
	var cidrText string
	if err := row.Scan(&out.ID, &out.AccountID, &out.AppID, &out.NetworkID, &out.Region,
		&cidrText, &out.Status, &out.StatusDetail, &out.CreatedAt, &out.UpdatedAt); err != nil {
		if err == pgx.ErrNoRows {
			return AppPrivateNetworkAttachment{}, ErrNotFound
		}
		return AppPrivateNetworkAttachment{}, mapErr(err)
	}
	out.CIDRs = cidrTextToPrefixes(cidrText)
	return out, nil
}

func clonePrivateNetworkAttachment(in AppPrivateNetworkAttachment) AppPrivateNetworkAttachment {
	in.CIDRs = append([]netip.Prefix(nil), in.CIDRs...)
	return in
}
