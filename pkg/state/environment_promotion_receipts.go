package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentPromotionReceiptStore interface {
	EnvironmentPromotionReceipt(context.Context, string, string, string, string) (api.EnvironmentPromotionReceipt, error)
	CheckEnvironmentPromotionAttempt(context.Context, ProjectEnvironmentPromotion) error
}

var ErrEnvironmentPromotionAttemptAccepted = errors.New("original environment promotion already accepted; inspect its receipt")
var ErrEnvironmentPromotionAttemptConflict = errors.New("environment promotion attempt conflicts with its original request")

type environmentPromotionAttempt struct{ id, hash string }
type environmentPromotionAttemptKey struct{}

// ContextWithEnvironmentPromotionAttempt carries APID-validated original intent.
func ContextWithEnvironmentPromotionAttempt(ctx context.Context, id, hash string) context.Context {
	return context.WithValue(ctx, environmentPromotionAttemptKey{}, environmentPromotionAttempt{id, hash})
}

// HasEnvironmentPromotionAttempt distinguishes opt-in admission from legacy continuation.
func HasEnvironmentPromotionAttempt(ctx context.Context) bool {
	_, ok := ctx.Value(environmentPromotionAttemptKey{}).(environmentPromotionAttempt)
	return ok
}
func environmentPromotionIntent(ctx context.Context) (environmentPromotionAttempt, bool, error) {
	a, ok := ctx.Value(environmentPromotionAttemptKey{}).(environmentPromotionAttempt)
	if ok && (!api.ValidDeploymentAttemptID(a.id) || !environmentRollbackHash.MatchString(a.hash)) {
		return a, ok, ErrInvalidArgument
	}
	return a, ok, nil
}
func environmentPromotionPrior(a environmentPromotionAttempt, p ProjectEnvironmentPromotion, receipt api.EnvironmentPromotionReceipt) error {
	if receipt.AttemptID != a.id || receipt.RequestHash != a.hash || receipt.ProjectID != p.ProjectID || receipt.PromotionHash != p.PromotionHash || api.EnvironmentPromotionRequestHash(receipt.ProjectID, receipt.FromEnvironmentID, receipt.EnvironmentID, p.PromotionHash) != a.hash {
		return ErrEnvironmentPromotionAttemptConflict
	}
	return ErrEnvironmentPromotionAttemptAccepted
}
func newEnvironmentPromotionReceipt(a environmentPromotionAttempt, p ProjectEnvironmentPromotion, from, target string) (*api.EnvironmentPromotionReceipt, error) {
	if p.ID != "" || from == "" || target == "" || from == target || !environmentRollbackHash.MatchString(p.PromotionHash) || api.EnvironmentPromotionRequestHash(p.ProjectID, from, target, p.PromotionHash) != a.hash {
		return nil, ErrEnvironmentPromotionAttemptConflict
	}
	return &api.EnvironmentPromotionReceipt{AttemptID: a.id, RequestHash: a.hash, PromotionID: p.ID, PromotionHash: p.PromotionHash, ProjectID: p.ProjectID, FromEnvironmentID: from, EnvironmentID: target, AcceptedAt: time.Now().UTC()}, nil
}
func readEnvironmentPromotionReceipt(ctx context.Context, db sqlc.DBTX, account, attempt string) (api.EnvironmentPromotionReceipt, error) {
	r, err := sqlc.New().ReadEnvironmentPromotionReceipt(ctx, db, sqlc.ReadEnvironmentPromotionReceiptParams{AccountID: account, AttemptID: attempt})
	if err != nil {
		return api.EnvironmentPromotionReceipt{}, mapErr(err)
	}
	return api.EnvironmentPromotionReceipt{AttemptID: r.AttemptID, RequestHash: r.RequestHash, PromotionID: r.PromotionID, PromotionHash: r.PromotionHash, ProjectID: r.ProjectID, FromEnvironmentID: r.FromEnvironmentID, EnvironmentID: r.EnvironmentID, AcceptedAt: r.AcceptedAt.Time}, nil
}
func prepareEnvironmentPromotionReceipt(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion) (*api.EnvironmentPromotionReceipt, error) {
	a, ok, err := environmentPromotionIntent(ctx)
	if err != nil || !ok {
		return nil, err
	}
	if err := sqlc.New().LockEnvironmentPromotionAttempt(ctx, db, sqlc.LockEnvironmentPromotionAttemptParams{AccountID: p.AccountID, AttemptID: a.id}); err != nil {
		return nil, err
	}
	prior, err := readEnvironmentPromotionReceipt(ctx, db, p.AccountID, a.id)
	if err == nil {
		return nil, environmentPromotionPrior(a, p, prior)
	}
	if !errors.Is(err, ErrNotFound) {
		return nil, err
	}
	scope, err := sqlc.New().EnvironmentRollbackScope(ctx, db, sqlc.EnvironmentRollbackScopeParams{AccountID: p.AccountID, ProjectID: p.ProjectID, ProjectSlug: p.ProjectSlug, FromEnvironment: p.FromEnvironment, Environment: p.ToEnvironment})
	if err != nil {
		return nil, mapErr(err)
	}
	return newEnvironmentPromotionReceipt(a, p, scope.FromEnvironmentID, scope.EnvironmentID)
}
func insertEnvironmentPromotionReceipt(ctx context.Context, db sqlc.DBTX, account string, r api.EnvironmentPromotionReceipt) error {
	err := sqlc.New().InsertEnvironmentPromotionReceipt(ctx, db, sqlc.InsertEnvironmentPromotionReceiptParams{AccountID: account, PromotionID: r.PromotionID, AttemptID: r.AttemptID, RequestHash: r.RequestHash, PromotionHash: r.PromotionHash, ProjectID: r.ProjectID, FromEnvironmentID: r.FromEnvironmentID, EnvironmentID: r.EnvironmentID, AcceptedAt: pgtype.Timestamptz{Time: r.AcceptedAt, Valid: true}})
	if errors.Is(mapErr(err), ErrConflict) {
		return ErrEnvironmentPromotionAttemptConflict
	}
	return err
}
func (s *PgStore) CheckEnvironmentPromotionAttempt(ctx context.Context, p ProjectEnvironmentPromotion) error {
	a, ok, err := environmentPromotionIntent(ctx)
	if err != nil || !ok {
		return err
	}
	r, err := readEnvironmentPromotionReceipt(ctx, s.pool, p.AccountID, a.id)
	if errors.Is(err, ErrNotFound) {
		return ErrEnvironmentPromotionAttemptConflict
	}
	if err != nil {
		return err
	}
	if r.PromotionID != p.ID {
		return ErrEnvironmentPromotionAttemptConflict
	}
	return environmentPromotionPrior(a, p, r)
}
func (s *PgStore) EnvironmentPromotionReceipt(ctx context.Context, account, project, environment, attempt string) (api.EnvironmentPromotionReceipt, error) {
	r, err := readEnvironmentPromotionReceipt(ctx, s.pool, account, attempt)
	if err != nil {
		return r, err
	}
	p, _, err := s.ProjectEnvironmentPromotionByID(ctx, account, project, environment, r.PromotionID)
	if err != nil {
		return api.EnvironmentPromotionReceipt{}, err
	}
	scope, err := sqlc.New().EnvironmentRollbackScope(ctx, s.pool, sqlc.EnvironmentRollbackScopeParams{AccountID: account, ProjectID: p.ProjectID, ProjectSlug: project, FromEnvironment: p.FromEnvironment, Environment: environment})
	if err != nil {
		return api.EnvironmentPromotionReceipt{}, mapErr(err)
	}
	if r.ProjectID != p.ProjectID || r.FromEnvironmentID != scope.FromEnvironmentID || r.EnvironmentID != scope.EnvironmentID {
		return api.EnvironmentPromotionReceipt{}, ErrNotFound
	}
	return r, nil
}
func (m *MemStore) prepareEnvironmentPromotionReceiptLocked(ctx context.Context, p ProjectEnvironmentPromotion) (*api.EnvironmentPromotionReceipt, error) {
	a, ok, err := environmentPromotionIntent(ctx)
	if err != nil || !ok {
		return nil, err
	}
	for _, r := range m.environmentPromotionReceipts {
		if m.projectEnvironmentPromotions[r.PromotionID].AccountID == p.AccountID && r.AttemptID == a.id {
			return nil, environmentPromotionPrior(a, p, r)
		}
	}
	from, target := m.environmentRollbackScopeLocked(p)
	return newEnvironmentPromotionReceipt(a, p, from, target)
}
func (m *MemStore) CheckEnvironmentPromotionAttempt(ctx context.Context, p ProjectEnvironmentPromotion) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return err
	}
	a, ok, err := environmentPromotionIntent(ctx)
	if err != nil || !ok {
		return err
	}
	r, found := m.environmentPromotionReceipts[p.ID]
	if !found {
		return ErrEnvironmentPromotionAttemptConflict
	}
	return environmentPromotionPrior(a, p, r)
}
func (m *MemStore) EnvironmentPromotionReceipt(ctx context.Context, account, project, environment, attempt string) (api.EnvironmentPromotionReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.EnvironmentPromotionReceipt{}, err
	}
	for _, r := range m.environmentPromotionReceipts {
		p := m.projectEnvironmentPromotions[r.PromotionID]
		from, target := m.environmentRollbackScopeLocked(p)
		if p.AccountID == account && p.ProjectSlug == project && p.ToEnvironment == environment && r.AttemptID == attempt && r.ProjectID == p.ProjectID && r.FromEnvironmentID == from && r.EnvironmentID == target {
			return r, nil
		}
	}
	return api.EnvironmentPromotionReceipt{}, ErrNotFound
}
