// adr: 1302
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func baselineFromSQL(row sqlc.WorkflowAutomationLiveBaselinePromotion) (api.AutomationLiveBaseline, error) {
	var receipt api.AutomationLiveBaseline
	err := json.Unmarshal(row.Receipt, &receipt)
	return receipt, err
}
func getBaselineHeadSQL(ctx context.Context, db sqlc.DBTX, appID, name, alias string) (api.AutomationLiveBaseline, error) {
	row, err := sqlc.New().GetAutomationLiveBaselineHead(ctx, db, sqlc.GetAutomationLiveBaselineHeadParams{AppID: mustPgUUID(appID), Name: name, BaselineName: alias})
	if errors.Is(err, pgx.ErrNoRows) {
		return api.AutomationLiveBaseline{}, ErrNotFound
	}
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	return baselineFromSQL(row)
}
func (s *PgStore) GetAutomationLiveBaseline(ctx context.Context, appID, name, alias string) (api.AutomationLiveBaseline, error) {
	if !api.ValidAutomationLiveBaselineName(alias) {
		return api.AutomationLiveBaseline{}, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	retired, err := baselineRetirementSQL(ctx, tx, appID, name, alias)
	if err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if retired != nil {
		return api.AutomationLiveBaseline{}, baselineRetiredError()
	}
	return getBaselineHeadSQL(ctx, tx, appID, name, alias)
}
func (s *PgStore) GetAutomationLiveBaselineHistory(ctx context.Context, appID, name, alias string) (api.AutomationLiveBaselineHistory, error) {
	out := api.AutomationLiveBaselineHistory{Promotions: []api.AutomationLiveBaseline{}}
	if !api.ValidAutomationLiveBaselineName(alias) {
		return out, ErrNotFound
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	out.Retirement, err = baselineRetirementSQL(ctx, tx, appID, name, alias)
	if err != nil {
		return out, err
	}
	rows, err := sqlc.New().GetAutomationLiveBaselineHistory(ctx, tx, sqlc.GetAutomationLiveBaselineHistoryParams{AppID: mustPgUUID(appID), Name: name, BaselineName: alias, Limit: api.AutomationLiveBaselineHistoryLimit})
	if err != nil {
		return out, err
	}
	if len(rows) == 0 {
		return out, ErrNotFound
	}
	for _, row := range rows {
		receipt, err := baselineFromSQL(row)
		if err != nil {
			return out, err
		}
		out.Promotions = append(out.Promotions, receipt)
	}
	return out, nil
}
func baselineStoreError(err error) error {
	var failure *pgconn.PgError
	if errors.As(err, &failure) && (failure.Code == "40001" || failure.Code == "40P01" || failure.Code == "23505") {
		return ErrAutomationLiveBaselineConflict
	}
	return err
}
func (s *PgStore) PromoteAutomationLiveBaseline(ctx context.Context, appID, name, alias, promotionID, actor, key string, request api.PromoteAutomationLiveBaselineRequest) (receipt api.AutomationLiveBaseline, err error) {
	policy, err := validateBaselinePromotion(alias, promotionID, actor, key, &request)
	if err != nil {
		return receipt, err
	}
	promotionID = uuid.MustParse(promotionID).String()
	// A concurrent identical request can lose the snapshot/unique-key race after
	// the winner commits. Resolve its committed receipt after rollback, without
	// changing the head or requiring the caller to invent a different intent.
	defer func() {
		mapped := baselineStoreError(err)
		if errors.Is(mapped, ErrAutomationLiveBaselineConflict) && ctx.Err() == nil {
			row, lookupErr := sqlc.New().GetAutomationLiveBaselinePromotion(ctx, s.pool, mustPgUUID(promotionID))
			if lookupErr == nil {
				prior, decodeErr := baselineFromSQL(row)
				if decodeErr == nil && baselineReplay(prior, appID, name, alias, actor, key, request, policy) {
					receipt = prior
					err = nil
					return
				}
			}
		}
		err = mapped
	}()

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
	if err != nil {
		return receipt, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := sqlc.New()
	if err = q.LockWorkflowRunAdmission(ctx, tx, appID); err != nil {
		return receipt, err
	}
	existing, lookupErr := q.GetAutomationLiveBaselinePromotion(ctx, tx, mustPgUUID(promotionID))
	if lookupErr == nil {
		prior, decodeErr := baselineFromSQL(existing)
		if decodeErr != nil {
			return receipt, decodeErr
		}
		if prior.AppID != appID || prior.Name != name {
			return receipt, ErrNotFound
		}
		if !baselineReplay(prior, appID, name, alias, actor, key, request, policy) {
			return receipt, ErrAutomationLiveBaselineConflict
		}
		return prior, nil
	}
	if !errors.Is(lookupErr, pgx.ErrNoRows) {
		return receipt, lookupErr
	}
	if err = lockManagedSuiteTarget(ctx, tx, appID, name); err != nil {
		return receipt, err
	}
	if err = q.FenceAutomationLiveBaselineMutation(ctx, tx, sqlc.FenceAutomationLiveBaselineMutationParams{AppID: mustPgUUID(appID), Name: name}); err != nil {
		return receipt, err
	}
	retired, err := baselineRetirementSQL(ctx, tx, appID, name, alias)
	if err != nil {
		return receipt, err
	}
	if retired != nil {
		return receipt, baselineRetiredError()
	}
	prior, err := getBaselineHeadSQL(ctx, tx, appID, name, alias)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return receipt, err
	}
	if prior.Revision != request.ExpectedRevision {
		return receipt, ErrAutomationLiveBaselineConflict
	}
	if prior.Revision == 0 {
		total, countErr := q.CountAutomationLiveBaselineNames(ctx, tx, sqlc.CountAutomationLiveBaselineNamesParams{AppID: mustPgUUID(appID), Name: name})
		if countErr != nil {
			return receipt, countErr
		}
		if total >= api.AutomationLiveBaselineLifetimeNameLimit {
			return receipt, baselineLifetimeLimitError(total + 1)
		}
		count, countErr := q.CountAutomationLiveBaselines(ctx, tx, sqlc.CountAutomationLiveBaselinesParams{AppID: mustPgUUID(appID), Name: name})
		if countErr != nil {
			return receipt, countErr
		}
		if count >= api.AutomationLiveBaselineLimit {
			return receipt, baselineLimitError(count + 1)
		}
	}
	record, err := getLiveSuiteSQL(ctx, tx, appID, name, request.SuiteID)
	if err != nil {
		return receipt, err
	}
	view, err := liveSuiteViewSQL(ctx, tx, record)
	if err != nil {
		return receipt, err
	}
	receipt, err = promotedBaseline(appID, name, alias, promotionID, actor, key, request, policy, prior, view)
	if err != nil {
		return receipt, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return receipt, err
	}
	if err = q.InsertAutomationLiveBaselinePromotion(ctx, tx, sqlc.InsertAutomationLiveBaselinePromotionParams{PromotionID: mustPgUUID(promotionID), AppID: mustPgUUID(appID), Name: name, BaselineName: alias, Revision: receipt.Revision, SuiteID: mustPgUUID(receipt.Suite.ID), Receipt: raw, PromotedAt: pgtypeTimestamptzFromTime(receipt.PromotedAt)}); err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if err = q.PruneAutomationLiveBaselineHistory(ctx, tx, sqlc.PruneAutomationLiveBaselineHistoryParams{AppID: mustPgUUID(appID), Name: name, BaselineName: alias, Revision: receipt.Revision - api.AutomationLiveBaselineHistoryLimit}); err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return api.AutomationLiveBaseline{}, err
	}
	return receipt, nil
}
