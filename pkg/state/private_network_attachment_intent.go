package state

import (
	"context"
	"errors"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type PrivateNetworkAttachmentIntentStatusStore interface {
	UpdatePrivateNetworkAttachmentStatusForIntent(context.Context, AppPrivateNetworkAttachment, string, string) (AppPrivateNetworkAttachment, error)
}

type PrivateNetworkAttachmentIntentReader interface {
	PrivateNetworkAttachmentByAppID(context.Context, string) (AppPrivateNetworkAttachment, error)
}

func (m *MemStore) PrivateNetworkAttachmentByAppID(ctx context.Context, appID string) (AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return AppPrivateNetworkAttachment{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.privateNetworkAttachments[appID]
	if !ok {
		return AppPrivateNetworkAttachment{}, ErrNotFound
	}
	return clonePrivateNetworkAttachment(row), nil
}

func (s *PgStore) PrivateNetworkAttachmentByAppID(ctx context.Context, appID string) (AppPrivateNetworkAttachment, error) {
	row, err := sqlc.New().PrivateNetworkAttachmentByAppID(ctx, s.pool, mustPgUUID(appID))
	if err != nil {
		return AppPrivateNetworkAttachment{}, mapErr(err)
	}
	return AppPrivateNetworkAttachment{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), NetworkID: row.NetworkID, Region: row.Region,
		CIDRs: cidrTextToPrefixes(row.CidrsText), AllowedCIDRs: cidrTextToPrefixes(row.AllowedCidrsText), Status: row.Status, StatusDetail: row.StatusDetail, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}

func (m *MemStore) UpdatePrivateNetworkAttachmentStatusForIntent(ctx context.Context, expected AppPrivateNetworkAttachment, status, detail string) (AppPrivateNetworkAttachment, error) {
	if err := ctx.Err(); err != nil {
		return AppPrivateNetworkAttachment{}, err
	}
	if status != "pending" && status != "ready" && status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row, ok := m.privateNetworkAttachments[expected.AppID]
	if !ok || row.ID != expected.ID || row.AccountID != expected.AccountID || row.NetworkID != expected.NetworkID || row.Region != expected.Region || !slices.Equal(row.CIDRs, expected.CIDRs) || !slices.Equal(row.AllowedCIDRs, expected.AllowedCIDRs) {
		return AppPrivateNetworkAttachment{}, ErrConflict
	}
	row.Status, row.StatusDetail, row.UpdatedAt = status, detail, time.Now().UTC()
	m.privateNetworkAttachments[row.AppID] = row
	return clonePrivateNetworkAttachment(row), nil
}

func (s *PgStore) UpdatePrivateNetworkAttachmentStatusForIntent(ctx context.Context, expected AppPrivateNetworkAttachment, status, detail string) (AppPrivateNetworkAttachment, error) {
	if status != "pending" && status != "ready" && status != "error" {
		return AppPrivateNetworkAttachment{}, ErrInvalidArgument
	}
	row, err := sqlc.New().UpdatePrivateNetworkAttachmentStatusForIntent(ctx, s.pool, sqlc.UpdatePrivateNetworkAttachmentStatusForIntentParams{
		AccountID: mustPgUUID(expected.AccountID), AppID: mustPgUUID(expected.AppID), ID: mustPgUUID(expected.ID), NetworkID: expected.NetworkID, Region: expected.Region,
		Cidrs: expected.CIDRs, AllowedCidrs: expected.AllowedCIDRs, Status: status, StatusDetail: detail,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return AppPrivateNetworkAttachment{}, ErrConflict
	}
	if err != nil {
		return AppPrivateNetworkAttachment{}, mapErr(err)
	}
	return AppPrivateNetworkAttachment{ID: pgUUIDString(row.ID), AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), NetworkID: row.NetworkID, Region: row.Region,
		CIDRs: cidrTextToPrefixes(row.CidrsText), AllowedCIDRs: cidrTextToPrefixes(row.AllowedCidrsText), Status: row.Status, StatusDetail: row.StatusDetail, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}, nil
}
