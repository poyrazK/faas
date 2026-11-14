package state

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/lazyregexp"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

type EnvironmentRollbackReceiptStore interface {
	EnvironmentRollbackReceipt(context.Context, string, string, string, string, string) (api.EnvironmentRollbackReceipt, error)
}

var ErrEnvironmentRollbackAttemptAccepted = errors.New("original environment rollback already accepted; inspect its receipt")
var ErrEnvironmentRollbackAttemptConflict = errors.New("environment rollback attempt conflicts with its original request")
var environmentRollbackHash = lazyregexp.New(`^[0-9a-f]{64}$`)

type environmentRollbackAttempt struct{ id, hash string }
type environmentRollbackAttemptKey struct{}

// ContextWithEnvironmentRollbackAttempt carries APID-validated original intent.
func ContextWithEnvironmentRollbackAttempt(ctx context.Context, id, hash string) context.Context {
	return context.WithValue(ctx, environmentRollbackAttemptKey{}, environmentRollbackAttempt{id, hash})
}
func environmentRollbackPrior(ctx context.Context, prior *api.EnvironmentRollbackReceipt) error {
	attempt, checked := ctx.Value(environmentRollbackAttemptKey{}).(environmentRollbackAttempt)
	if checked && (!api.ValidDeploymentAttemptID(attempt.id) || !environmentRollbackHash.MatchString(attempt.hash)) {
		return ErrInvalidArgument
	}
	// The opt-in attempt contract rejects re-admission. Existing explicit
	// original-key callers may still resume native unfinished checkpoints;
	// they cannot create or alter this immutable receipt.
	if prior == nil || !checked {
		return nil
	}
	if checked && (attempt.id != prior.AttemptID || attempt.hash != prior.RequestHash) {
		return ErrEnvironmentRollbackAttemptConflict
	}
	return ErrEnvironmentRollbackAttemptAccepted
}
func newEnvironmentRollbackReceipt(ctx context.Context, p ProjectEnvironmentPromotion, fromID, environmentID string) (*api.EnvironmentRollbackReceipt, error) {
	attempt, checked := ctx.Value(environmentRollbackAttemptKey{}).(environmentRollbackAttempt)
	if !checked {
		return nil, nil
	}
	if p.Status == "running" || p.RollbackStatus != "" || p.RollbackIdempotencyKey != "" || p.RollbackStartedAt != nil || !environmentRollbackHash.MatchString(p.PromotionHash) {
		return nil, ErrEnvironmentRollbackAttemptConflict
	}
	hash := api.EnvironmentRollbackRequestHash(p.ProjectID, fromID, environmentID, p.ID, p.PromotionHash)
	if hash != attempt.hash || fromID == "" || environmentID == "" || fromID == environmentID {
		return nil, ErrEnvironmentRollbackAttemptConflict
	}
	return &api.EnvironmentRollbackReceipt{AttemptID: attempt.id, RequestHash: hash, PromotionID: p.ID, PromotionHash: p.PromotionHash, ProjectID: p.ProjectID, FromEnvironmentID: fromID, EnvironmentID: environmentID, AcceptedAt: time.Now().UTC()}, nil
}
func readEnvironmentRollbackReceipt(ctx context.Context, db sqlc.DBTX, accountID, promotionID string) (api.EnvironmentRollbackReceipt, error) {
	row, err := sqlc.New().ReadEnvironmentRollbackReceipt(ctx, db, sqlc.ReadEnvironmentRollbackReceiptParams{AccountID: accountID, PromotionID: promotionID})
	if err != nil {
		return api.EnvironmentRollbackReceipt{}, mapErr(err)
	}
	return api.EnvironmentRollbackReceipt{AttemptID: row.AttemptID, RequestHash: row.RequestHash, PromotionID: row.PromotionID, PromotionHash: row.PromotionHash, ProjectID: row.ProjectID, FromEnvironmentID: row.FromEnvironmentID, EnvironmentID: row.EnvironmentID, AcceptedAt: row.AcceptedAt.Time}, nil
}
func prepareEnvironmentRollbackReceipt(ctx context.Context, db sqlc.DBTX, p ProjectEnvironmentPromotion) error {
	prior, err := readEnvironmentRollbackReceipt(ctx, db, p.AccountID, p.ID)
	if err == nil {
		return environmentRollbackPrior(ctx, &prior)
	}
	if !errors.Is(err, ErrNotFound) {
		return err
	}
	if err := environmentRollbackPrior(ctx, nil); err != nil {
		return err
	}
	if _, checked := ctx.Value(environmentRollbackAttemptKey{}).(environmentRollbackAttempt); !checked {
		return nil
	}
	scope, err := sqlc.New().EnvironmentRollbackScope(ctx, db, sqlc.EnvironmentRollbackScopeParams{AccountID: p.AccountID, ProjectID: p.ProjectID, ProjectSlug: p.ProjectSlug, FromEnvironment: p.FromEnvironment, Environment: p.ToEnvironment})
	if err != nil {
		return mapErr(err)
	}
	receipt, err := newEnvironmentRollbackReceipt(ctx, p, scope.FromEnvironmentID, scope.EnvironmentID)
	if err != nil {
		return err
	}
	err = sqlc.New().InsertEnvironmentRollbackReceipt(ctx, db, sqlc.InsertEnvironmentRollbackReceiptParams{AccountID: p.AccountID, PromotionID: p.ID, AttemptID: receipt.AttemptID, RequestHash: receipt.RequestHash, PromotionHash: receipt.PromotionHash, ProjectID: receipt.ProjectID, FromEnvironmentID: receipt.FromEnvironmentID, EnvironmentID: receipt.EnvironmentID, AcceptedAt: pgtype.Timestamptz{Time: receipt.AcceptedAt, Valid: true}})
	if errors.Is(mapErr(err), ErrConflict) {
		return ErrEnvironmentRollbackAttemptConflict
	}
	return err
}
func (s *PgStore) EnvironmentRollbackReceipt(ctx context.Context, accountID, project, environment, promotion, attempt string) (api.EnvironmentRollbackReceipt, error) {
	p, _, err := s.ProjectEnvironmentPromotionByID(ctx, accountID, project, environment, promotion)
	if err != nil {
		return api.EnvironmentRollbackReceipt{}, err
	}
	receipt, err := readEnvironmentRollbackReceipt(ctx, s.pool, accountID, promotion)
	if err != nil {
		return receipt, err
	}
	scope, err := sqlc.New().EnvironmentRollbackScope(ctx, s.pool, sqlc.EnvironmentRollbackScopeParams{AccountID: accountID, ProjectID: p.ProjectID, ProjectSlug: project, FromEnvironment: p.FromEnvironment, Environment: environment})
	if err != nil {
		return api.EnvironmentRollbackReceipt{}, mapErr(err)
	}
	if receipt.AttemptID != attempt || receipt.ProjectID != p.ProjectID || receipt.FromEnvironmentID != scope.FromEnvironmentID || receipt.EnvironmentID != scope.EnvironmentID {
		return api.EnvironmentRollbackReceipt{}, ErrNotFound
	}
	return receipt, nil
}
func (m *MemStore) environmentRollbackScopeLocked(p ProjectEnvironmentPromotion) (string, string) {
	project, ok := m.projects[p.ProjectID]
	if !ok || project.AccountID != p.AccountID || project.Slug != p.ProjectSlug {
		return "", ""
	}
	var from, target string
	for _, env := range m.projectEnvironments {
		if env.AccountID == p.AccountID && env.ProjectID == p.ProjectID {
			if env.Slug == p.FromEnvironment {
				from = env.ID
			}
			if env.Slug == p.ToEnvironment {
				target = env.ID
			}
		}
	}
	return from, target
}
func (m *MemStore) prepareEnvironmentRollbackReceiptLocked(ctx context.Context, p ProjectEnvironmentPromotion) (*api.EnvironmentRollbackReceipt, error) {
	if prior, found := m.environmentRollbackReceipts[p.ID]; found {
		return nil, environmentRollbackPrior(ctx, &prior)
	}
	if err := environmentRollbackPrior(ctx, nil); err != nil {
		return nil, err
	}
	if _, checked := ctx.Value(environmentRollbackAttemptKey{}).(environmentRollbackAttempt); !checked {
		return nil, nil
	}
	from, target := m.environmentRollbackScopeLocked(p)
	receipt, err := newEnvironmentRollbackReceipt(ctx, p, from, target)
	if err != nil {
		return nil, err
	}
	for _, prior := range m.environmentRollbackReceipts {
		if m.projectEnvironmentPromotions[prior.PromotionID].AccountID == p.AccountID && prior.AttemptID == receipt.AttemptID {
			return nil, ErrEnvironmentRollbackAttemptConflict
		}
	}
	return receipt, nil
}
func (m *MemStore) EnvironmentRollbackReceipt(ctx context.Context, accountID, project, environment, promotion, attempt string) (api.EnvironmentRollbackReceipt, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if err := m.checkOrgAccessLocked(ctx); err != nil {
		return api.EnvironmentRollbackReceipt{}, err
	}
	p, ok := m.projectEnvironmentPromotions[promotion]
	receipt, found := m.environmentRollbackReceipts[promotion]
	from, target := m.environmentRollbackScopeLocked(p)
	if !ok || !found || p.AccountID != accountID || p.ProjectSlug != project || p.ToEnvironment != environment || receipt.AttemptID != attempt || receipt.ProjectID != p.ProjectID || from != receipt.FromEnvironmentID || target != receipt.EnvironmentID {
		return api.EnvironmentRollbackReceipt{}, ErrNotFound
	}
	return receipt, nil
}
