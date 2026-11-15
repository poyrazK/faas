package state

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// PlanFitNotice is the plan and over-plan app set a billing subject was last
// emailed about (billing review H17-4). schedd parks and refuses to wake apps
// outside the subject's plan (ADR-20261103131742); meterd mails the subject
// when that set changes.
type PlanFitNotice struct {
	Plan       api.Plan
	AppsDigest string
}

// PlanFitNoticeStore reads and writes plan-fit notices. The subject is an
// account or a resource-owning organization.
type PlanFitNoticeStore interface {
	// PlanFitNotice returns the subject's last notice; false when none.
	PlanFitNotice(ctx context.Context, subjectID string) (PlanFitNotice, bool, error)
	// PutPlanFitNotice records the notice just sent.
	PutPlanFitNotice(ctx context.Context, subjectID string, notice PlanFitNotice) error
	// ClearPlanFitNotice removes the subject's notice once it fits its plan.
	ClearPlanFitNotice(ctx context.Context, subjectID string) error
}

var (
	_ PlanFitNoticeStore = (*PgStore)(nil)
	_ PlanFitNoticeStore = (*MemStore)(nil)
)

// PlanFitDigest is the stable digest of an over-plan app set: the sha256 of
// its sorted app IDs.
func PlanFitDigest(appIDs []string) string {
	ids := append([]string(nil), appIDs...)
	sort.Strings(ids)
	h := sha256.New()
	for _, id := range ids {
		h.Write([]byte(id))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// PlanFitNotice implements PlanFitNoticeStore.
func (s *PgStore) PlanFitNotice(ctx context.Context, subjectID string) (PlanFitNotice, bool, error) {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return PlanFitNotice{}, false, ErrInvalidArgument
	}
	row, err := sqlc.New().GetPlanFitNotice(ctx, s.pool, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return PlanFitNotice{}, false, nil
	}
	if err != nil {
		return PlanFitNotice{}, false, fmt.Errorf("state: read plan fit notice: %w", mapErr(err))
	}
	return PlanFitNotice{Plan: api.Plan(row.Plan), AppsDigest: row.AppsDigest}, true, nil
}

// PutPlanFitNotice implements PlanFitNoticeStore.
func (s *PgStore) PutPlanFitNotice(ctx context.Context, subjectID string, notice PlanFitNotice) error {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return ErrInvalidArgument
	}
	if err := sqlc.New().PutPlanFitNotice(ctx, s.pool, sqlc.PutPlanFitNoticeParams{SubjectID: id, Plan: string(notice.Plan), AppsDigest: notice.AppsDigest}); err != nil {
		return fmt.Errorf("state: put plan fit notice: %w", mapErr(err))
	}
	return nil
}

// ClearPlanFitNotice implements PlanFitNoticeStore.
func (s *PgStore) ClearPlanFitNotice(ctx context.Context, subjectID string) error {
	id, err := parsePgUUID(subjectID)
	if err != nil {
		return ErrInvalidArgument
	}
	if err := sqlc.New().ClearPlanFitNotice(ctx, s.pool, id); err != nil {
		return fmt.Errorf("state: clear plan fit notice: %w", mapErr(err))
	}
	return nil
}

// PlanFitNotice implements PlanFitNoticeStore.
func (m *MemStore) PlanFitNotice(_ context.Context, subjectID string) (PlanFitNotice, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	n, ok := m.planFitNotices[subjectID]
	return n, ok, nil
}

// PutPlanFitNotice implements PlanFitNoticeStore.
func (m *MemStore) PutPlanFitNotice(_ context.Context, subjectID string, notice PlanFitNotice) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.planFitNotices == nil {
		m.planFitNotices = map[string]PlanFitNotice{}
	}
	m.planFitNotices[subjectID] = notice
	return nil
}

// ClearPlanFitNotice implements PlanFitNoticeStore.
func (m *MemStore) ClearPlanFitNotice(_ context.Context, subjectID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.planFitNotices, subjectID)
	return nil
}
