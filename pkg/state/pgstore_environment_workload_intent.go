package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ EnvironmentWorkloadIntentStore = (*PgStore)(nil)

func workloadIntentFromSQL(row sqlc.AppEnvironmentWorkloadIntent) EnvironmentWorkloadIntent {
	out := EnvironmentWorkloadIntent{AccountID: pgUUIDString(row.AccountID), AppID: pgUUIDString(row.AppID), EnvironmentID: pgUUIDString(row.EnvironmentID), SourceRevision: row.SourceRevision.String, CreatedAt: row.CreatedAt.Time, UpdatedAt: row.UpdatedAt.Time}
	_ = json.Unmarshal(row.Source, &out.Source)
	_ = json.Unmarshal(row.Runtime, &out.Runtime)
	_ = json.Unmarshal(row.ServiceBindings, &out.ServiceBindings)
	return cloneWorkloadIntent(out)
}

func putWorkloadIntentTx(ctx context.Context, tx sqlc.DBTX, row EnvironmentWorkloadIntent) (EnvironmentWorkloadIntent, error) {
	var source []byte
	if row.Source != nil {
		source, _ = json.Marshal(row.Source)
	}
	runtime, _ := json.Marshal(cloneWorkloadIntent(row).Runtime)
	bindings, _ := json.Marshal(cloneWorkloadIntent(row).ServiceBindings)
	stored, err := sqlc.New().PutEnvironmentWorkloadIntent(ctx, tx, sqlc.PutEnvironmentWorkloadIntentParams{AccountID: mustPgUUID(row.AccountID), AppID: mustPgUUID(row.AppID), EnvironmentID: mustPgUUID(row.EnvironmentID), Source: source, Runtime: runtime, SourceRevision: row.SourceRevision, ServiceBindings: bindings})
	if err != nil {
		return row, mapErr(err)
	}
	return workloadIntentFromSQL(stored), nil
}

func (s *PgStore) EnvironmentWorkloadIntent(ctx context.Context, accountID, appID, environmentID string) (EnvironmentWorkloadIntent, error) {
	row, err := sqlc.New().EnvironmentWorkloadIntent(ctx, s.pool, sqlc.EnvironmentWorkloadIntentParams{AccountID: mustPgUUID(accountID), AppID: mustPgUUID(appID), EnvironmentID: mustPgUUID(environmentID)})
	if err != nil {
		return EnvironmentWorkloadIntent{}, mapErr(err)
	}
	return workloadIntentFromSQL(row), nil
}

func (s *PgStore) PutEnvironmentWorkloadIntent(ctx context.Context, row EnvironmentWorkloadIntent) (EnvironmentWorkloadIntent, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return row, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if _, err := q.EnvironmentWorkloadIntentLockSource(ctx, tx, sqlc.EnvironmentWorkloadIntentLockSourceParams{AccountID: mustPgUUID(row.AccountID), EnvironmentID: mustPgUUID(row.EnvironmentID)}); err != nil {
		return row, mapErr(err)
	}
	raw, err := q.EnvironmentWorkloadIntentContext(ctx, tx, sqlc.EnvironmentWorkloadIntentContextParams{AccountID: mustPgUUID(row.AccountID), AppID: mustPgUUID(row.AppID), EnvironmentID: mustPgUUID(row.EnvironmentID)})
	if err != nil {
		return row, mapErr(err)
	}
	var scope struct {
		Type          AppType       `json:"type"`
		Runtime       string        `json:"runtime"`
		Manifest      AppManifest   `json:"manifest"`
		WorkloadClass WorkloadClass `json:"workload_class"`
		Environment   string        `json:"environment"`
		Plan          api.Plan      `json:"plan"`
	}
	if err := json.Unmarshal(raw, &scope); err != nil {
		return row, err
	}
	prior, err := q.EnvironmentWorkloadIntent(ctx, tx, sqlc.EnvironmentWorkloadIntentParams{
		AccountID: mustPgUUID(row.AccountID), AppID: mustPgUUID(row.AppID), EnvironmentID: mustPgUUID(row.EnvironmentID)})
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return row, mapErr(err)
	}
	row, err = validateWorkloadIntentWrite(row, workloadIntentFromSQL(prior), App{Type: scope.Type, Runtime: scope.Runtime, Manifest: scope.Manifest, WorkloadClass: scope.WorkloadClass}, scope.Environment, scope.Plan)
	if err != nil {
		return row, err
	}
	if len(row.ServiceBindings) != 0 {
		count, err := q.CountAppEnvironmentIntent(ctx, tx, sqlc.CountAppEnvironmentIntentParams{AccountID: mustPgUUID(row.AccountID), AppID: mustPgUUID(row.AppID)})
		if err != nil {
			return row, mapErr(err)
		}
		limits, _ := api.LimitsFor(scope.Plan)
		if int(count)-len(workloadIntentFromSQL(prior).ServiceBindings)+len(row.ServiceBindings) > limits.EnvVarsMax {
			return row, ErrConflict
		}
	}
	row, err = putWorkloadIntentTx(ctx, tx, row)
	if err != nil {
		return row, err
	}
	if err := tx.Commit(ctx); err != nil {
		return row, err
	}
	return row, nil
}
