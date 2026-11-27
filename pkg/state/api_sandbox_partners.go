package state

import (
	"context"
	"crypto/subtle"
	"fmt"
	"slices"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxPartner struct {
	Label          string
	InviteHash     []byte
	InvitedAt      *time.Time
	RedeemedAt     *time.Time
	RevokedAt      *time.Time
	ConsumerKeyID  string
	FirstSuccessAt *time.Time
}

func (p APISandboxPartner) Response() api.APISandboxPartnerResponse {
	return api.APISandboxPartnerResponse{
		Label: p.Label, InvitedAt: p.InvitedAt, RedeemedAt: p.RedeemedAt, RevokedAt: p.RevokedAt, FirstSuccessAt: p.FirstSuccessAt,
	}
}

func (p APISandboxPartner) MatchesInvite(hash []byte) bool {
	return p.RedeemedAt == nil && p.RevokedAt == nil && len(hash) == 32 && subtle.ConstantTimeCompare(p.InviteHash, hash) == 1
}

func (p APISandboxPartner) ActiveKey(keyID string) bool {
	return keyID != "" && p.RedeemedAt != nil && p.RevokedAt == nil && p.ConsumerKeyID == keyID
}

type APISandboxPartnerRedemption struct {
	App                         App
	ConsumerID, KeyName, Prefix string
	InviteHash, KeyHash         []byte
	At                          time.Time
	Quota                       ConsumerKeyQuota
}

type APISandboxPartnerStore interface {
	GetAPISandboxPartner(ctx context.Context, appID string) (APISandboxPartner, error)
	SetAPISandboxPartnerInvite(ctx context.Context, appID string, hash []byte, at time.Time) (APISandboxPartner, error)
	RedeemAPISandboxPartnerInvite(ctx context.Context, redemption APISandboxPartnerRedemption) (ConsumerKey, error)
	RevokeAPISandboxPartner(ctx context.Context, appID string, at time.Time) error
}

func apiSandboxPartnerFromRow(row sqlc.ApiSandboxPartner, err error) (APISandboxPartner, error) {
	if err != nil {
		return APISandboxPartner{}, mapErr(err)
	}
	return APISandboxPartner{
		Label: row.Label, InviteHash: row.InviteHash, InvitedAt: optionalPgTime(row.InvitedAt),
		RedeemedAt: optionalPgTime(row.RedeemedAt), RevokedAt: optionalPgTime(row.RevokedAt),
		ConsumerKeyID: pgUUIDString(row.ConsumerKeyID), FirstSuccessAt: optionalPgTime(row.FirstSuccessAt),
	}, nil
}

func (s *PgStore) GetAPISandboxPartner(ctx context.Context, appID string) (APISandboxPartner, error) {
	return apiSandboxPartnerFromRow(sqlc.New().GetAPISandboxPartner(ctx, s.pool, mustPgUUID(appID)))
}

func (s *PgStore) SetAPISandboxPartnerInvite(ctx context.Context, appID string, hash []byte, at time.Time) (APISandboxPartner, error) {
	return apiSandboxPartnerFromRow(sqlc.New().SetAPISandboxPartnerInvite(ctx, s.pool, sqlc.SetAPISandboxPartnerInviteParams{
		AppID: mustPgUUID(appID), InviteHash: hash, AtTime: pgTimestamptzUTC(at),
	}))
}

func (s *PgStore) RedeemAPISandboxPartnerInvite(ctx context.Context, in APISandboxPartnerRedemption) (ConsumerKey, error) {
	scopes := []string{"read", "write"}
	if err := validatePgConsumerKeyForConsumer(in.App.AccountID, in.ConsumerID, in.KeyName, in.Prefix, in.KeyHash, scopes, in.App.PreviewExpiresAt); err != nil {
		return ConsumerKey{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return ConsumerKey{}, err
	}
	defer rollbackOrgAccess(ctx, tx)
	// The key quota lock also locks the app. Take it before locking the
	// invitation so concurrent redemption and app teardown use one order.
	subject, _, err := lockQuotaSubjectTx(ctx, tx, in.App.ID)
	if err != nil {
		return ConsumerKey{}, err
	}
	q := sqlc.New()
	appID, at := mustPgUUID(in.App.ID), pgTimestamptzUTC(in.At)
	partner, err := apiSandboxPartnerFromRow(q.LockAPISandboxPartner(ctx, tx, sqlc.LockAPISandboxPartnerParams{AppID: appID, AtTime: at}))
	if err != nil {
		return ConsumerKey{}, err
	}
	if !partner.MatchesInvite(in.InviteHash) {
		return ConsumerKey{}, ErrNotFound
	}
	if err := checkConsumerKeyCounts(ctx, tx, in.App.ID, subject, in.Quota); err != nil {
		return ConsumerKey{}, err
	}
	key, err := insertConsumerKeyForConsumer(ctx, tx, in.App.AccountID, in.ConsumerID, in.KeyName, in.Prefix, in.KeyHash, scopes, in.App.PreviewExpiresAt)
	if err != nil {
		return ConsumerKey{}, err
	}
	if key.AppID != in.App.ID {
		return ConsumerKey{}, ErrNotFound
	}
	if err := q.RedeemAPISandboxPartnerInvite(ctx, tx, sqlc.RedeemAPISandboxPartnerInviteParams{
		AppID: appID, AtTime: at, ConsumerKeyID: mustPgUUID(key.ID),
	}); err != nil {
		return ConsumerKey{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return ConsumerKey{}, fmt.Errorf("state: commit sandbox partner redemption: %w", err)
	}
	return key, nil
}

func (s *PgStore) RevokeAPISandboxPartner(ctx context.Context, appID string, at time.Time) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer rollbackOrgAccess(ctx, tx)
	q := sqlc.New()
	id, revokedAt := mustPgUUID(appID), pgTimestamptzUTC(at)
	p, err := q.RevokeAPISandboxPartner(ctx, tx, sqlc.RevokeAPISandboxPartnerParams{AppID: id, AtTime: revokedAt})
	if err != nil {
		return mapErr(err)
	}
	if p.ConsumerKeyID.Valid {
		if err := q.RevokeAPISandboxPartnerKey(ctx, tx, sqlc.RevokeAPISandboxPartnerKeyParams{
			AppID: id, KeyID: p.ConsumerKeyID, AtTime: revokedAt,
		}); err != nil {
			return err
		}
	}
	return tx.Commit(ctx)
}

func (s *PgStore) APISandboxPartnerKeyActive(ctx context.Context, appID, keyID string) (bool, error) {
	return sqlc.New().APISandboxPartnerKeyActive(ctx, s.pool, sqlc.APISandboxPartnerKeyActiveParams{AppID: mustPgUUID(appID), KeyID: mustPgUUID(keyID)})
}

func (s *PgStore) RecordAPISandboxPartnerSuccess(ctx context.Context, appID, keyID string, at time.Time) error {
	return sqlc.New().RecordAPISandboxPartnerSuccess(ctx, s.pool, sqlc.RecordAPISandboxPartnerSuccessParams{
		AppID: mustPgUUID(appID), KeyID: mustPgUUID(keyID), SucceededAt: pgTimestamptzUTC(at),
	})
}

func (m *MemStore) GetAPISandboxPartner(_ context.Context, appID string) (APISandboxPartner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiSandboxPartners[appID]
	if !ok {
		return APISandboxPartner{}, ErrNotFound
	}
	p.InviteHash = slices.Clone(p.InviteHash)
	return p, nil
}

func (m *MemStore) SetAPISandboxPartnerInvite(ctx context.Context, appID string, hash []byte, at time.Time) (APISandboxPartner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return APISandboxPartner{}, err
	}
	p, ok := m.apiSandboxPartners[appID]
	if !ok || !APISandboxAcceptsRequests(m.apps[appID], at) || (p.RedeemedAt != nil && p.RevokedAt == nil) {
		return APISandboxPartner{}, ErrNotFound
	}
	p = APISandboxPartner{Label: p.Label, InviteHash: slices.Clone(hash), InvitedAt: &at}
	m.apiSandboxPartners[appID] = p
	p.InviteHash = slices.Clone(p.InviteHash)
	return p, nil
}

func (m *MemStore) RedeemAPISandboxPartnerInvite(_ context.Context, in APISandboxPartnerRedemption) (ConsumerKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app := m.apps[in.App.ID]
	p := m.apiSandboxPartners[app.ID]
	if !APISandboxAcceptsRequests(app, in.At) || !p.MatchesInvite(in.InviteHash) ||
		m.apiConsumers[in.ConsumerID].AppID != app.ID {
		return ConsumerKey{}, ErrNotFound
	}
	key, err := m.createConsumerKeyUnderQuotaLocked(app.AccountID, in.ConsumerID, in.KeyName, in.Prefix, in.KeyHash,
		[]string{"read", "write"}, app.PreviewExpiresAt, in.Quota)
	if err != nil {
		return ConsumerKey{}, err
	}
	p.RedeemedAt, p.ConsumerKeyID, p.InviteHash = &in.At, key.ID, nil
	m.apiSandboxPartners[app.ID] = p
	return key, nil
}

func (m *MemStore) RevokeAPISandboxPartner(ctx context.Context, appID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	p, ok := m.apiSandboxPartners[appID]
	if !ok {
		return ErrNotFound
	}
	if p.RevokedAt == nil {
		p.RevokedAt = &at
	}
	p.InviteHash = nil
	m.apiSandboxPartners[appID] = p
	if key, ok := m.consumerKeys[p.ConsumerKeyID]; ok && key.RevokedAt == nil {
		key.RevokedAt = &at
		m.consumerKeys[key.ID] = key
	}
	return nil
}

func (m *MemStore) RecordAPISandboxPartnerSuccess(_ context.Context, appID, keyID string, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.apiSandboxPartners[appID]
	if p.ActiveKey(keyID) {
		if at.Before(*p.RedeemedAt) {
			at = *p.RedeemedAt
		}
		if p.FirstSuccessAt == nil || at.Before(*p.FirstSuccessAt) {
			p.FirstSuccessAt = &at
			m.apiSandboxPartners[appID] = p
		}
	}
	return nil
}
