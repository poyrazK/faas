package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func routePolicyReadError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	return err
}

func pgRoutePolicySnapshot(ctx context.Context, tx pgx.Tx, accountID, appID string, lock bool) (RoutePolicySnapshot, error) {
	q := &sqlc.Queries{}
	var account, app []byte
	var rules [][]byte
	var err error
	if lock {
		account, err = q.LockRoutePolicyAccount(ctx, tx, accountID)
	} else {
		account, err = q.ReadRoutePolicyAccount(ctx, tx, accountID)
	}
	if err != nil {
		return RoutePolicySnapshot{}, routePolicyReadError(err)
	}
	if lock {
		app, err = q.LockRoutePolicyApp(ctx, tx, sqlc.LockRoutePolicyAppParams{AppID: appID, AccountID: accountID})
	} else {
		app, err = q.ReadRoutePolicyApp(ctx, tx, sqlc.ReadRoutePolicyAppParams{AppID: appID, AccountID: accountID})
	}
	if err != nil {
		return RoutePolicySnapshot{}, routePolicyReadError(err)
	}
	if lock {
		rules, err = q.LockRoutePolicyRules(ctx, tx, appID)
	} else {
		rules, err = q.ReadRoutePolicyRules(ctx, tx, appID)
	}
	if err != nil {
		return RoutePolicySnapshot{}, err
	}
	snapshot := RoutePolicySnapshot{Rules: make([]api.EdgeRuleResponse, 0, len(rules))}
	if err := json.Unmarshal(account, &snapshot.Account); err != nil {
		return snapshot, err
	}
	if err := json.Unmarshal(app, &snapshot.App); err != nil {
		return snapshot, err
	}
	for _, body := range rules {
		var rule api.EdgeRuleResponse
		if err := json.Unmarshal(body, &rule); err != nil {
			return snapshot, err
		}
		snapshot.Rules = append(snapshot.Rules, rule)
	}
	return snapshot, nil
}

func (s *PgStore) PlanRoutePolicy(ctx context.Context, accountID, appID string, request api.RoutePolicyPlanRequest, planner RoutePolicyPlanner) (api.RoutePolicyPlan, error) {
	if err := ValidateRoutePolicySource(request); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return api.RoutePolicyPlan{}, fmt.Errorf("begin route policy snapshot: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, false)
	if err != nil {
		return api.RoutePolicyPlan{}, fmt.Errorf("read route policy snapshot: %w", err)
	}
	if err := pgRoutePolicyContract(ctx, tx, &snapshot, request.DeploymentID, false); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	if err := pgRoutePolicySavedRequirements(ctx, tx, &snapshot, request); err != nil {
		return api.RoutePolicyPlan{}, err
	}
	plan, err := planner(snapshot)
	if err != nil {
		return plan, err
	}
	return plan, tx.Commit(ctx)
}

func pgFindRoutePolicyReceipt(ctx context.Context, db sqlc.DBTX, accountID, appID, key string, request api.RoutePolicyApplyRequest) (api.RoutePolicyReceipt, error) {
	q := &sqlc.Queries{}
	stored, err := q.ReadRoutePolicyReceiptByKey(ctx, db, sqlc.ReadRoutePolicyReceiptByKeyParams{AccountID: accountID, AppID: appID, IdempotencyKey: key})
	if err != nil {
		return api.RoutePolicyReceipt{}, routePolicyReadError(err)
	}
	if stored.RequestSha256 != RoutePolicyRequestSHA256(request) {
		return api.RoutePolicyReceipt{}, ErrRoutePolicyKeyReused
	}
	var receipt api.RoutePolicyReceipt
	err = json.Unmarshal(stored.Receipt, &receipt)
	return receipt, err
}

func (s *PgStore) FindRoutePolicyReceipt(ctx context.Context, accountID, appID, key string, request api.RoutePolicyApplyRequest) (api.RoutePolicyReceipt, error) {
	return pgFindRoutePolicyReceipt(ctx, s.pool, accountID, appID, key, request)
}

func (s *PgStore) ApplyRoutePolicy(ctx context.Context, accountID, appID, key string, request api.RoutePolicyApplyRequest, planner RoutePolicyPlanner) (api.RoutePolicyReceipt, bool, error) {
	if err := ValidateRoutePolicyApply(key, request); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return api.RoutePolicyReceipt{}, false, fmt.Errorf("begin route policy apply: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	snapshot, err := pgRoutePolicySnapshot(ctx, tx, accountID, appID, true)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, fmt.Errorf("lock route policy snapshot: %w", err)
	}
	if receipt, err := pgFindRoutePolicyReceipt(ctx, tx, accountID, appID, key, request); err == nil || !errors.Is(err, ErrNotFound) {
		return receipt, err == nil, err
	}
	if err := pgRoutePolicyContract(ctx, tx, &snapshot, request.DeploymentID, true); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	if err := pgRoutePolicySavedRequirements(ctx, tx, &snapshot, request.RoutePolicyPlanRequest); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	return pgCommitRoutePolicy(ctx, tx, snapshot, key, request, planner)
}

// SaveRouteRequirements takes the same app lock before changing intent. The
// apply read is therefore fenced until commit; planning uses repeatable read.
func pgRoutePolicySavedRequirements(ctx context.Context, tx pgx.Tx, snapshot *RoutePolicySnapshot, request api.RoutePolicyPlanRequest) error {
	if !request.Saved {
		return nil
	}
	saved, err := pgSavedRouteRequirements(ctx, tx, snapshot.Account.ID, snapshot.App.ID)
	if err != nil {
		return err
	}
	return bindRoutePolicySavedRequirements(snapshot, request, saved)
}

func pgCommitRoutePolicy(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, key string, request api.RoutePolicyApplyRequest, planner RoutePolicyPlanner) (api.RoutePolicyReceipt, bool, error) {
	plan, err := routePolicyPlanForApply(snapshot, request, planner)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	now := time.Now().UTC()
	staged, changes, err := stageRoutePolicy(snapshot, plan, now)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	if err := pgWriteRoutePolicyChanges(ctx, tx, staged, changes); err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	// Verify persisted JSONB and real rule IDs inside the same transaction.
	persisted, err := pgRoutePolicySnapshot(ctx, tx, snapshot.Account.ID, snapshot.App.ID, false)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	persisted.Contract = snapshot.Contract                   // Capture row remains locked until commit.
	persisted.SavedRequirements = snapshot.SavedRequirements // App lock fences intent updates.
	receipt, err := verifyRoutePolicy(persisted, planner, plan, changes, now)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	body, err := json.Marshal(receipt)
	if err != nil {
		return api.RoutePolicyReceipt{}, false, err
	}
	q := &sqlc.Queries{}
	err = q.InsertRoutePolicyReceipt(ctx, tx, sqlc.InsertRoutePolicyReceiptParams{ID: receipt.ID, AccountID: snapshot.Account.ID,
		AppID: snapshot.App.ID, IdempotencyKey: key, RequestSha256: RoutePolicyRequestSHA256(request), Receipt: body})
	if err != nil {
		return api.RoutePolicyReceipt{}, false, fmt.Errorf("record route policy receipt: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return api.RoutePolicyReceipt{}, false, fmt.Errorf("commit route policy: %w", err)
	}
	return receipt, false, nil
}

func pgWriteRoutePolicyChanges(ctx context.Context, tx pgx.Tx, snapshot RoutePolicySnapshot, changes []api.RoutePolicyAppliedChange) error {
	q := &sqlc.Queries{}
	for _, change := range changes {
		for _, rule := range snapshot.Rules {
			if rule.ID != change.RuleID {
				continue
			}
			if change.Operation == "create" {
				if rule.Priority < 0 || rule.Priority > api.RoutePlanPriorityMax {
					return ErrRoutePolicyUnresolved
				}
				if err := q.CreateRoutePolicyRule(ctx, tx, sqlc.CreateRoutePolicyRuleParams{ID: rule.ID, AccountID: rule.AccountID, AppID: rule.AppID,
					MatchHost: rule.MatchHost, MatchPath: rule.MatchPath, MatchMethods: rule.MatchMethods, Priority: int16(rule.Priority), Kind: rule.Kind, Action: rule.Action}); err != nil {
					return fmt.Errorf("create planned route rule: %w", err)
				}
			} else {
				count, err := q.UpdateRoutePolicyRuleAction(ctx, tx, sqlc.UpdateRoutePolicyRuleActionParams{ID: rule.ID, AccountID: rule.AccountID,
					AppID: rule.AppID, Kind: rule.Kind, Action: rule.Action})
				if err != nil {
					return fmt.Errorf("update planned route rule: %w", err)
				}
				if count != 1 {
					return ErrNotFound
				}
			}
		}
	}
	return nil
}

func (s *PgStore) GetRoutePolicyReceipt(ctx context.Context, accountID, appID, id string) (api.RoutePolicyReceipt, error) {
	q := &sqlc.Queries{}
	body, err := q.ReadRoutePolicyReceipt(ctx, s.pool, sqlc.ReadRoutePolicyReceiptParams{ID: id, AccountID: accountID, AppID: appID})
	if err != nil {
		return api.RoutePolicyReceipt{}, routePolicyReadError(err)
	}
	var receipt api.RoutePolicyReceipt
	err = json.Unmarshal(body, &receipt)
	return receipt, err
}
