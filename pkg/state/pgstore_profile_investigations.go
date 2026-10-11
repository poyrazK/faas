package state

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func pgProfileInvestigation(ctx context.Context, db sqlc.DBTX, accountID, appID, id string) (api.ProfileInvestigation, error) {
	body, err := sqlc.New().ReadProfileInvestigation(ctx, db, sqlc.ReadProfileInvestigationParams{ID: id, AppID: appID, AccountID: accountID})
	if err != nil {
		return api.ProfileInvestigation{}, routePolicyReadError(err)
	}
	var out api.ProfileInvestigation
	if err := json.Unmarshal(body, &out); err != nil {
		return out, fmt.Errorf("decode saved investigation: %w", err)
	}
	return out, nil
}

func (s *PgStore) GetProfileInvestigation(ctx context.Context, accountID, appID, id string) (api.ProfileInvestigation, error) {
	return pgProfileInvestigation(ctx, s.pool, accountID, appID, id)
}

func (s *PgStore) ListProfileInvestigations(ctx context.Context, accountID, appID string) ([]api.ProfileInvestigation, error) {
	q := sqlc.New()
	owned, err := q.ProfileInvestigationAppOwned(ctx, s.pool, sqlc.ProfileInvestigationAppOwnedParams{AppID: appID, AccountID: accountID})
	if err != nil {
		return nil, fmt.Errorf("read investigation app: %w", err)
	}
	if !owned {
		return nil, ErrNotFound
	}
	rows, err := q.ListProfileInvestigations(ctx, s.pool, sqlc.ListProfileInvestigationsParams{AppID: appID, AccountID: accountID, MaxRows: api.ProfileInvestigationMaxPerApp})
	if err != nil {
		return nil, fmt.Errorf("list saved investigations: %w", err)
	}
	out := make([]api.ProfileInvestigation, 0, len(rows))
	for _, body := range rows {
		var row api.ProfileInvestigation
		if err := json.Unmarshal(body, &row); err != nil {
			return nil, fmt.Errorf("decode saved investigation: %w", err)
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *PgStore) SaveProfileInvestigation(ctx context.Context, accountID, appID, id string, req api.SaveProfileInvestigationRequest) (api.ProfileInvestigation, error) {
	if err := ValidateProfileInvestigation(req); err != nil {
		return api.ProfileInvestigation{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.ProfileInvestigation{}, fmt.Errorf("begin investigation update: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.ProfileInvestigation{}, routePolicyReadError(err)
	}
	current := api.ProfileInvestigation{}
	if id == "" {
		if *req.ExpectedRevision != 0 {
			return current, ErrProfileInvestigationRevision
		}
		count, err := q.CountProfileInvestigations(ctx, tx, appID)
		if err != nil {
			return current, fmt.Errorf("count saved investigations: %w", err)
		}
		if count >= api.ProfileInvestigationMaxPerApp {
			return current, ErrProfileInvestigationQuota
		}
		id = uuid.NewString()
	} else {
		current, err = pgProfileInvestigation(ctx, tx, accountID, appID, id)
		if err != nil {
			return current, err
		}
		if current.Revision != *req.ExpectedRevision {
			return current, ErrProfileInvestigationRevision
		}
	}
	for _, selection := range []api.ProfileQuery{req.Investigation.Baseline, req.Investigation.Candidate} {
		owned, err := q.ProfileInvestigationDeploymentOwned(ctx, tx, sqlc.ProfileInvestigationDeploymentOwnedParams{ID: selection.DeploymentID, AppID: appID})
		if err != nil {
			return current, fmt.Errorf("read investigation deployment: %w", err)
		}
		unchanged := sameInvestigationSelection(selection, current.Investigation.Baseline) || sameInvestigationSelection(selection, current.Investigation.Candidate)
		if !owned && !unchanged {
			return current, ErrNotFound
		}
	}
	body, err := json.Marshal(req.Investigation)
	if err != nil {
		return current, fmt.Errorf("encode investigation: %w", err)
	}
	var assessment []byte
	if req.InitialAssessment != nil {
		assessment, err = json.Marshal(req.InitialAssessment)
		if err != nil {
			return current, fmt.Errorf("encode initial assessment: %w", err)
		}
	}
	if err = q.WriteProfileInvestigation(ctx, tx, sqlc.WriteProfileInvestigationParams{ID: id, AppID: appID, AccountID: accountID, Revision: current.Revision + 1, Investigation: body, Assessment: assessment}); err != nil {
		return current, fmt.Errorf("write investigation: %w", err)
	}
	out, err := pgProfileInvestigation(ctx, tx, accountID, appID, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) DeleteProfileInvestigation(ctx context.Context, accountID, appID, id string, revision int64) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin investigation deletion: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err = q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return routePolicyReadError(err)
	}
	current, err := pgProfileInvestigation(ctx, tx, accountID, appID, id)
	if err != nil {
		return err
	}
	if current.Revision != revision {
		return ErrProfileInvestigationRevision
	}
	if _, err = q.DeleteProfileInvestigation(ctx, tx, sqlc.DeleteProfileInvestigationParams{ID: id, AppID: appID, AccountID: accountID, Revision: revision}); err != nil {
		return fmt.Errorf("delete investigation: %w", err)
	}
	return tx.Commit(ctx)
}

func (s *PgStore) SaveProfileRegressionAssessment(ctx context.Context, accountID, appID, id string, revision int64, assessment api.ProfileRegressionAssessment) (api.ProfileInvestigation, error) {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.ProfileInvestigation{}, fmt.Errorf("begin profile assessment: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID}); err != nil {
		return api.ProfileInvestigation{}, routePolicyReadError(err)
	}
	current, err := pgProfileInvestigation(ctx, tx, accountID, appID, id)
	if err != nil {
		return current, err
	}
	body, err := validateProfileAssessment(current, revision, assessment)
	if err != nil {
		return current, err
	}
	if err := q.WriteProfileRegressionAssessment(ctx, tx, sqlc.WriteProfileRegressionAssessmentParams{ID: id, AppID: appID, AccountID: accountID, Revision: revision, Assessment: body}); err != nil {
		return current, fmt.Errorf("write profile assessment: %w", err)
	}
	out, err := pgProfileInvestigation(ctx, tx, accountID, appID, id)
	if err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}
