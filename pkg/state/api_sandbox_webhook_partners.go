package state

import (
	"bytes"
	"context"
	"slices"
	"sort"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type APISandboxWebhookPartner struct {
	RunID, SourceDeploymentID, ScenarioRevision       string
	ExpiresAt                                         time.Time
	InviteHash, OutcomeTokenHash, SigningSecretSealed []byte
	TargetURL                                         string
	ReadyAt, OutcomeAt                                *time.Time
	OutcomeCount                                      int
}

func (p APISandboxWebhookPartner) ReadyFor(sourceID, revision string, at time.Time) bool {
	return p.SourceDeploymentID == sourceID && p.ScenarioRevision == revision &&
		p.ReadyAt != nil && p.ExpiresAt.After(at) && p.TargetURL != "" && len(p.SigningSecretSealed) != 0
}

func (p APISandboxWebhookPartner) InviteStatus(at time.Time) api.APISandboxReceiverInvite {
	status := "pending"
	switch {
	case !p.ExpiresAt.After(at):
		status = "expired"
	case p.ReadyAt != nil:
		status = "ready"
	case p.TargetURL != "":
		status = "verified"
	}
	return api.APISandboxReceiverInvite{
		RunID: p.RunID, ScenarioRevision: p.ScenarioRevision, ExpiresAt: p.ExpiresAt, Status: status,
	}
}

type APISandboxWebhookPartnerStore interface {
	ListAPISandboxWebhookPartnerSetups(context.Context, string, time.Time) ([]api.APISandboxReceiverInvite, error)
	CreateAPISandboxWebhookPartner(context.Context, APISandboxWebhookPartner) error
	GetAPISandboxWebhookPartner(context.Context, string) (APISandboxWebhookPartner, error)
	RedeemAPISandboxWebhookPartner(context.Context, APISandboxWebhookPartner, time.Time) error
	ReadyAPISandboxWebhookPartner(context.Context, string, []byte, time.Time) error
	ParkAPISandboxWebhookPartnerRun(context.Context, APISandboxRun, time.Time, time.Time) error
	RecordAPISandboxWebhookPartnerOutcome(context.Context, string, []byte, int, time.Time) error
	DeleteExpiredAPISandboxWebhookPartners(context.Context, time.Time) error
}

func sandboxWebhookPartnerUpdate(n int64, err error) error {
	if err == nil && n != 1 {
		return ErrConflict
	}
	return mapErr(err)
}

func (s *PgStore) CreateAPISandboxWebhookPartner(ctx context.Context, p APISandboxWebhookPartner) error {
	return sandboxWebhookPartnerUpdate(sqlc.New().CreateAPISandboxWebhookPartner(ctx, s.pool, sqlc.CreateAPISandboxWebhookPartnerParams{
		RunID: mustPgUUID(p.RunID), SourceDeploymentID: mustPgUUID(p.SourceDeploymentID),
		ScenarioRevision: mustPgUUID(p.ScenarioRevision), InviteHash: p.InviteHash, ExpiresAt: pgTimestamptzUTC(p.ExpiresAt),
	}))
}

func (s *PgStore) GetAPISandboxWebhookPartner(ctx context.Context, id string) (APISandboxWebhookPartner, error) {
	row, err := sqlc.New().GetAPISandboxWebhookPartner(ctx, s.pool, mustPgUUID(id))
	if err != nil {
		return APISandboxWebhookPartner{}, mapErr(err)
	}
	return APISandboxWebhookPartner{
		RunID: pgUUIDString(row.RunID), SourceDeploymentID: pgUUIDString(row.SourceDeploymentID),
		ScenarioRevision: pgUUIDString(row.ScenarioRevision), ExpiresAt: row.ExpiresAt.Time.UTC(),
		InviteHash: row.InviteHash, SigningSecretSealed: row.SigningSecretSealed, OutcomeTokenHash: row.OutcomeTokenHash,
		TargetURL: row.TargetUrl, ReadyAt: optionalPgTime(row.ReadyAt), OutcomeAt: optionalPgTime(row.OutcomeAt),
		OutcomeCount: int(row.OutcomeCount.Int32),
	}, nil
}

func (s *PgStore) RedeemAPISandboxWebhookPartner(ctx context.Context, p APISandboxWebhookPartner, at time.Time) error {
	return sandboxWebhookPartnerUpdate(sqlc.New().RedeemAPISandboxWebhookPartner(ctx, s.pool, sqlc.RedeemAPISandboxWebhookPartnerParams{
		RunID: mustPgUUID(p.RunID), InviteHash: p.InviteHash, TargetUrl: p.TargetURL,
		SigningSecretSealed: p.SigningSecretSealed, OutcomeTokenHash: p.OutcomeTokenHash, AtTime: pgTimestamptzUTC(at),
	}))
}

func (s *PgStore) ReadyAPISandboxWebhookPartner(ctx context.Context, id string, hash []byte, at time.Time) error {
	return sandboxWebhookPartnerUpdate(sqlc.New().ReadyAPISandboxWebhookPartner(ctx, s.pool, sqlc.ReadyAPISandboxWebhookPartnerParams{
		RunID: mustPgUUID(id), OutcomeTokenHash: hash, AtTime: pgTimestamptzUTC(at),
	}))
}

func (s *PgStore) ParkAPISandboxWebhookPartnerRun(ctx context.Context, run APISandboxRun, at, until time.Time) error {
	return sandboxWebhookPartnerUpdate(sqlc.New().ParkAPISandboxWebhookPartnerRun(ctx, s.pool, sqlc.ParkAPISandboxWebhookPartnerRunParams{
		RunID: mustPgUUID(run.ID), LeaseToken: mustPgUUID(run.LeaseToken), AtTime: pgTimestamptzUTC(at), OutcomeUntil: pgTimestamptzUTC(until),
	}))
}

func (s *PgStore) RecordAPISandboxWebhookPartnerOutcome(ctx context.Context, id string, hash []byte, count int, at time.Time) error {
	return sandboxWebhookPartnerUpdate(sqlc.New().RecordAPISandboxWebhookPartnerOutcome(ctx, s.pool, sqlc.RecordAPISandboxWebhookPartnerOutcomeParams{
		RunID: mustPgUUID(id), OutcomeTokenHash: hash, AtTime: pgTimestamptzUTC(at), OutcomeCount: pgtype.Int4{Int32: int32(count), Valid: true},
	}))
}

func (s *PgStore) DeleteExpiredAPISandboxWebhookPartners(ctx context.Context, at time.Time) error {
	return mapErr(sqlc.New().DeleteExpiredAPISandboxWebhookPartners(ctx, s.pool, pgTimestamptzUTC(at)))
}

func (m *MemStore) CreateAPISandboxWebhookPartner(_ context.Context, p APISandboxWebhookPartner) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.apiSandboxWebhookPartners[p.RunID]; ok {
		return ErrConflict
	}
	if m.apiSandboxWebhookPartners == nil {
		m.apiSandboxWebhookPartners = make(map[string]APISandboxWebhookPartner)
	}
	p.InviteHash = slices.Clone(p.InviteHash)
	m.apiSandboxWebhookPartners[p.RunID] = p
	return nil
}

func (m *MemStore) GetAPISandboxWebhookPartner(_ context.Context, id string) (APISandboxWebhookPartner, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	p, ok := m.apiSandboxWebhookPartners[id]
	if !ok {
		return APISandboxWebhookPartner{}, ErrNotFound
	}
	p.InviteHash = slices.Clone(p.InviteHash)
	p.OutcomeTokenHash = slices.Clone(p.OutcomeTokenHash)
	p.SigningSecretSealed = slices.Clone(p.SigningSecretSealed)
	return p, nil
}

func (m *MemStore) RedeemAPISandboxWebhookPartner(_ context.Context, p APISandboxWebhookPartner, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxWebhookPartners[p.RunID]
	if len(current.InviteHash) == 0 || !bytes.Equal(current.InviteHash, p.InviteHash) || !current.ExpiresAt.After(at) {
		return ErrConflict
	}
	current.TargetURL, current.InviteHash = p.TargetURL, nil
	current.SigningSecretSealed, current.OutcomeTokenHash = slices.Clone(p.SigningSecretSealed), slices.Clone(p.OutcomeTokenHash)
	m.apiSandboxWebhookPartners[p.RunID] = current
	return nil
}

func (m *MemStore) ReadyAPISandboxWebhookPartner(_ context.Context, id string, hash []byte, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	p := m.apiSandboxWebhookPartners[id]
	if len(p.OutcomeTokenHash) == 0 || !bytes.Equal(p.OutcomeTokenHash, hash) || !p.ExpiresAt.After(at) {
		return ErrConflict
	}
	if p.ReadyAt == nil {
		p.ReadyAt = &at
		m.apiSandboxWebhookPartners[id] = p
	}
	return nil
}

func (m *MemStore) ParkAPISandboxWebhookPartnerRun(_ context.Context, run APISandboxRun, at, until time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	current := m.apiSandboxRuns[run.ID]
	if !current.ownsLease(run.LeaseToken, at) || !current.PartnerWebhook || current.PartnerOutcomeUntil != nil || current.SandboxDeploymentID == "" {
		return ErrConflict
	}
	current.Status, current.PartnerOutcomeUntil = api.APISandboxRunQueued, &until
	current.LeaseToken, current.LeaseUntil = "", nil
	m.apiSandboxRuns[run.ID] = current
	return nil
}

func (m *MemStore) RecordAPISandboxWebhookPartnerOutcome(_ context.Context, id string, hash []byte, count int, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	run, p := m.apiSandboxRuns[id], m.apiSandboxWebhookPartners[id]
	if run.Status != api.APISandboxRunQueued || !run.PartnerWebhook || run.PartnerOutcomeUntil == nil ||
		!run.PartnerOutcomeUntil.After(at) || !p.ExpiresAt.After(at) || p.ReadyAt == nil ||
		!bytes.Equal(p.OutcomeTokenHash, hash) || (p.OutcomeAt != nil && p.OutcomeCount != count) {
		return ErrConflict
	}
	if p.OutcomeAt == nil {
		p.OutcomeCount, p.OutcomeAt = count, &at
		m.apiSandboxWebhookPartners[id] = p
	}
	return nil
}

func (m *MemStore) DeleteExpiredAPISandboxWebhookPartners(_ context.Context, at time.Time) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, p := range m.apiSandboxWebhookPartners {
		run := m.apiSandboxRuns[id]
		if !p.ExpiresAt.After(at) && (run.ID == "" || run.Terminal()) {
			delete(m.apiSandboxWebhookPartners, id)
		}
	}
	return nil
}

func (s *PgStore) ListAPISandboxWebhookPartnerSetups(ctx context.Context, sourceID string, at time.Time) ([]api.APISandboxReceiverInvite, error) {
	rows, err := sqlc.New().ListAPISandboxWebhookPartnerSetups(ctx, s.pool, sqlc.ListAPISandboxWebhookPartnerSetupsParams{
		SourceID: mustPgUUID(sourceID), AtTime: pgTimestamptzUTC(at),
	})
	if err != nil {
		return nil, mapErr(err)
	}
	invites := make([]api.APISandboxReceiverInvite, 0, len(rows))
	for _, row := range rows {
		status := "pending"
		if row.ReadyAt.Valid {
			status = "ready"
		} else if row.Verified {
			status = "verified"
		}
		invites = append(invites, api.APISandboxReceiverInvite{
			RunID: pgUUIDString(row.RunID), ScenarioRevision: pgUUIDString(row.ScenarioRevision),
			ExpiresAt: row.ExpiresAt.Time.UTC(), Status: status,
		})
	}
	return invites, nil
}

func (m *MemStore) ListAPISandboxWebhookPartnerSetups(_ context.Context, sourceID string, at time.Time) ([]api.APISandboxReceiverInvite, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	invites := make([]api.APISandboxReceiverInvite, 0)
	for id, partner := range m.apiSandboxWebhookPartners {
		if partner.SourceDeploymentID == sourceID && partner.ExpiresAt.After(at) && m.apiSandboxRuns[id].ID == "" {
			invites = append(invites, partner.InviteStatus(at))
		}
	}
	sort.Slice(invites, func(i, j int) bool {
		if invites[i].ExpiresAt.Equal(invites[j].ExpiresAt) {
			return invites[i].RunID > invites[j].RunID
		}
		return invites[i].ExpiresAt.After(invites[j].ExpiresAt)
	})
	return invites[:min(len(invites), 25)], nil
}
