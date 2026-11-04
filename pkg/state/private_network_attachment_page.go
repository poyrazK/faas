package state

import (
	"context"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PrivateNetworkAttachmentPage struct {
	AccountID, Region, NetworkID, AfterAppID string
	Statuses                                 []string
	Limit                                    int
}

type PrivateNetworkAttachmentPageStore interface {
	ListPrivateNetworkAttachmentPage(context.Context, PrivateNetworkAttachmentPage) ([]AppPrivateNetworkAttachment, error)
}

func validatePrivateAttachmentPage(page PrivateNetworkAttachmentPage) error {
	if page.AccountID == "" || page.Limit < 1 || page.Limit > 1000 {
		return ErrInvalidArgument
	}
	for _, status := range page.Statuses {
		if status != "pending" && status != "ready" && status != "error" {
			return ErrInvalidArgument
		}
	}
	return nil
}

func (m *MemStore) ListPrivateNetworkAttachmentPage(ctx context.Context, page PrivateNetworkAttachmentPage) ([]AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validatePrivateAttachmentPage(page); err != nil {
		return nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []AppPrivateNetworkAttachment
	for _, row := range m.privateNetworkAttachments {
		if row.AccountID != page.AccountID || (page.Region != "" && row.Region != page.Region) || (page.NetworkID != "" && row.NetworkID != page.NetworkID) || row.AppID <= page.AfterAppID {
			continue
		}
		matches := len(page.Statuses) == 0
		for _, status := range page.Statuses {
			if row.Status == status {
				matches = true
			}
		}
		if matches {
			out = append(out, clonePrivateNetworkAttachment(row))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AppID < out[j].AppID })
	if len(out) > page.Limit {
		out = out[:page.Limit]
	}
	return out, nil
}

func (s *PgStore) ListPrivateNetworkAttachmentPage(ctx context.Context, page PrivateNetworkAttachmentPage) ([]AppPrivateNetworkAttachment, error) {
	if err := validatePrivateAttachmentPage(page); err != nil {
		return nil, err
	}
	var after pgtype.UUID
	if page.AfterAppID != "" {
		if err := after.Scan(page.AfterAppID); err != nil {
			return nil, ErrInvalidArgument
		}
	}
	rows, err := sqlc.New().ListPrivateNetworkAttachmentPage(ctx, s.pool, sqlc.ListPrivateNetworkAttachmentPageParams{
		AccountID: mustPgUUID(page.AccountID), Region: page.Region, NetworkID: page.NetworkID,
		Statuses: nullableStrings(page.Statuses), AfterAppID: after, PageSize: int32(page.Limit),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	out := make([]AppPrivateNetworkAttachment, 0, len(rows))
	for _, row := range rows {
		out = append(out, AppPrivateNetworkAttachment{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), NetworkID: row.NetworkID, Region: row.Region,
			CIDRs: cidrTextToPrefixes(row.CidrsText), AllowedCIDRs: cidrTextToPrefixes(row.AllowedCidrsText), Status: row.Status, StatusDetail: row.StatusDetail, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time})
	}
	return out, nil
}

// ListScopedPrivateNetworkAttachments walks a stable app-ID cursor. Mutable
// reconciliation timestamps must not make rows move between pages.
func ListScopedPrivateNetworkAttachments(ctx context.Context, store any, page PrivateNetworkAttachmentPage) ([]AppPrivateNetworkAttachment, error) {
	pager, ok := store.(PrivateNetworkAttachmentPageStore)
	if !ok {
		return nil, fmt.Errorf("state: scoped private network attachment listing unsupported")
	}
	page.Limit = 1000
	var all []AppPrivateNetworkAttachment
	for {
		rows, err := pager.ListPrivateNetworkAttachmentPage(ctx, page)
		if err != nil {
			return nil, err
		}
		all = append(all, rows...)
		if len(rows) < page.Limit {
			return all, nil
		}
		next := rows[len(rows)-1].AppID
		if next <= page.AfterAppID {
			return nil, fmt.Errorf("state: private network attachment cursor did not advance")
		}
		page.AfterAppID = next
	}
}
