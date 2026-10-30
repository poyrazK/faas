package state

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
)

// OpenAPIDocQuotaError is returned by UpsertDeploymentOpenAPIDocIfUnderQuota
// when the app's billing subject (ADR-202610171904) holds its plan's
// allowance of OpenAPI documents already.
type OpenAPIDocQuotaError struct {
	Limit    int
	Observed int
}

func (e *OpenAPIDocQuotaError) Error() string {
	return fmt.Sprintf("state: openapi doc quota exceeded (limit=%d, observed=%d)", e.Limit, e.Observed)
}

func (s *PgStore) UpsertDeploymentOpenAPIDocIfUnderQuota(ctx context.Context, deploymentID, accountID, appID string, doc []byte, source string,
	truncated bool, limit int) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("state: begin openapi doc capture: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() //nolint:errcheck // no-op after Commit
	var depApp string
	err = tx.QueryRow(ctx, `select app_id::text from deployments where id = $1`, deploymentID).Scan(&depApp)
	if errors.Is(err, pgx.ErrNoRows) || err == nil && depApp != appID {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("state: read openapi doc deployment: %w", err)
	}
	// The billing subject's lock serializes every capture that spends its
	// allowance, so the count below holds until the save commits.
	subject, _, err := lockQuotaSubjectTx(ctx, tx, appID)
	if err != nil {
		return err
	}
	var replacing bool
	if err := tx.QueryRow(ctx, `select exists(select 1 from deployment_openapi_docs where deployment_id = $1)`,
		deploymentID).Scan(&replacing); err != nil {
		return fmt.Errorf("state: read openapi doc: %w", err)
	}
	// Replacing a deployment's document is not a new one.
	if !replacing {
		var observed int
		if err := tx.QueryRow(ctx, `select count(*)
			from deployment_openapi_docs d
			join apps a on a.id = d.app_id
			left join orgs o on o.id = a.org_id
			where (a.account_id = $1::uuid or a.org_id = $1::uuid)
			  and `+quotaSubjectSQL+` = $1::uuid and a.status <> 'deleted'`, subject).Scan(&observed); err != nil {
			return fmt.Errorf("state: count openapi docs of billing subject %s: %w", subject, err)
		}
		if observed >= limit {
			return &OpenAPIDocQuotaError{Limit: limit, Observed: observed}
		}
	}
	if err := upsertDeploymentOpenAPIDoc(ctx, tx, deploymentID, accountID, appID, doc, source, truncated); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("state: commit openapi doc capture: %w", err)
	}
	return nil
}

func (m *MemStore) UpsertDeploymentOpenAPIDocIfUnderQuota(_ context.Context, deploymentID, accountID, appID string, doc []byte, source string,
	truncated bool, limit int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	dep, ok := m.deployments[deploymentID]
	if !ok || dep.AppID != appID {
		return ErrNotFound
	}
	app, ok := m.apps[appID]
	if !ok || app.Status == AppDeleted {
		return ErrNotFound
	}
	if _, replacing := m.openAPIDocs[deploymentID]; !replacing {
		subject := m.appBillingSubjectLocked(app.AccountID, app.ID)
		observed := 0
		for _, row := range m.openAPIDocs {
			if m.countsTowardQuotaSubjectLocked(row.AccountID, row.AppID, subject) {
				observed++
			}
		}
		if observed >= limit {
			return &OpenAPIDocQuotaError{Limit: limit, Observed: observed}
		}
	}
	return m.upsertDeploymentOpenAPIDocLocked(deploymentID, accountID, appID, doc, source, truncated)
}
