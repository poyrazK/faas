package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// RouteContractDocStore reads the OpenAPI document a declared-route contract
// enforces. A PR preview inherits its production app's contract
// (only_declared_routes) but has no imported document of its own; it is held
// to its production app's document. Without that, an OpenAPI-backed contract
// answered every preview request with declared_route_policy_unavailable.
type RouteContractDocStore interface {
	GetRouteContractOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error)
}

var (
	_ RouteContractDocStore = (*PgStore)(nil)
	_ RouteContractDocStore = (*MemStore)(nil)
)

func (s *PgStore) GetRouteContractOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error) {
	row := s.pool.QueryRow(ctx,
		`select `+appOpenAPIDocSelectCols+` from app_openapi_docs
		 where account_id = $2
		   and app_id in (
		     select $1::uuid
		     union all
		     select parent.id from apps preview
		       join apps parent on parent.account_id = preview.account_id
		        and parent.slug = preview.preview_of_slug
		        and parent.preview_of_slug is null
		        and parent.status <> 'deleted'
		      where preview.id = $1::uuid and preview.account_id = $2)
		 order by (app_id = $1::uuid) desc
		 limit 1`,
		appID, accountID)
	doc, meta, err := scanAppOpenAPIDocCols(row.Scan)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, AppOpenAPIDocMeta{}, ErrNotFound
		}
		return nil, AppOpenAPIDocMeta{}, fmt.Errorf("state: get route contract openapi doc: %w", err)
	}
	return doc, meta, nil
}

func (m *MemStore) GetRouteContractOpenAPIDoc(ctx context.Context, appID, accountID string) ([]byte, AppOpenAPIDocMeta, error) {
	doc, meta, err := m.GetAppOpenAPIDoc(ctx, appID, accountID)
	if !errors.Is(err, ErrNotFound) {
		return doc, meta, err
	}
	m.mu.Lock()
	parentID := ""
	if preview, ok := m.apps[appID]; ok && preview.AccountID == accountID && preview.PreviewOfSlug != "" {
		for id, app := range m.apps {
			if app.AccountID == accountID && app.Slug == preview.PreviewOfSlug && app.PreviewOfSlug == "" && app.Status != AppDeleted {
				parentID = id
				break
			}
		}
	}
	m.mu.Unlock()
	if parentID == "" {
		return nil, AppOpenAPIDocMeta{}, ErrNotFound
	}
	return m.GetAppOpenAPIDoc(ctx, parentID, accountID)
}
