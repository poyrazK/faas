package state

import (
	"cmp"
	"context"
	"slices"
	"time"
)

// DeploymentPromotion records when a traffic change last took a deployment to
// 100% of its scope.
type DeploymentPromotion struct {
	DeploymentID string
	CommitSHA    string
	PromotedAt   time.Time
}

// DeploymentPromotionHistoryStore reads recent recorded promotions, newest
// first, strictly before the given time.
type DeploymentPromotionHistoryStore interface {
	ListDeploymentPromotions(ctx context.Context, accountID, appID, scope string, before time.Time, limit int) ([]DeploymentPromotion, error)
}

var (
	_ DeploymentPromotionHistoryStore = (*PgStore)(nil)
	_ DeploymentPromotionHistoryStore = (*MemStore)(nil)
)

func (s *PgStore) ListDeploymentPromotions(ctx context.Context, accountID, appID, scope string, before time.Time, limit int) ([]DeploymentPromotion, error) {
	rows, err := s.pool.Query(ctx, `
		select d.id, coalesce(d.commit_sha, ''), d.promoted_at
		from deployments d
		join apps a on a.id = d.app_id
		where a.account_id = $1 and d.app_id = $2 and d.scope = $3
		  and a.status != 'deleted' and d.deleted_at is null
		  and d.promoted_at < $4
		order by d.promoted_at desc, d.id desc
		limit $5
	`, accountID, appID, normalizedDeploymentScope(scope), before, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var promotions []DeploymentPromotion
	for rows.Next() {
		var p DeploymentPromotion
		if err := rows.Scan(&p.DeploymentID, &p.CommitSHA, &p.PromotedAt); err != nil {
			return nil, err
		}
		promotions = append(promotions, p)
	}
	return promotions, rows.Err()
}

func (m *MemStore) ListDeploymentPromotions(_ context.Context, accountID, appID, scope string, before time.Time, limit int) ([]DeploymentPromotion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	app, ok := m.apps[appID]
	if !ok || app.AccountID != accountID || app.Status == AppDeleted {
		return nil, nil
	}
	scope = normalizedDeploymentScope(scope)
	var promotions []DeploymentPromotion
	for id, at := range m.deploymentPromotedAt {
		d, ok := m.deployments[id]
		if !ok || d.AppID != appID || normalizedDeploymentScope(d.Scope) != scope || d.DeletedAt != nil || !at.Before(before) {
			continue
		}
		promotions = append(promotions, DeploymentPromotion{DeploymentID: id, CommitSHA: d.CommitSHA, PromotedAt: at})
	}
	slices.SortFunc(promotions, func(a, b DeploymentPromotion) int {
		if order := b.PromotedAt.Compare(a.PromotedAt); order != 0 {
			return order
		}
		return cmp.Compare(b.DeploymentID, a.DeploymentID)
	})
	return promotions[:min(len(promotions), limit)], nil
}
