// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// Session locks precede the repeatable-read snapshot, then the account row
// lock precedes app/FK/policy rows. Shared presets can affect several apps.
func (s *PgStore) beginTrafficPolicyMutation(ctx context.Context, account pgtype.UUID) (pgx.Tx, error) {
	return s.beginTrafficPolicyMutationWithRoutes(ctx, account, false)
}

func (s *PgStore) beginRuleTrafficPolicyMutation(ctx context.Context, account pgtype.UUID, kind EdgeRuleKind) (pgx.Tx, error) {
	return s.beginTrafficPolicyMutationWithRoutes(ctx, account, kind == EdgeRuleKindRoute)
}

func (s *PgStore) beginTrafficPolicyMutationWithRoutes(ctx context.Context, account pgtype.UUID, globalRoutes bool) (pgx.Tx, error) {
	for {
		conn, release, busy, err := s.tryAcquireTrafficPolicySession(ctx, account, globalRoutes)
		if err != nil {
			return nil, err
		}
		if !busy {
			tx, beginErr := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead})
			if beginErr != nil {
				release(ctx)
				return nil, fmt.Errorf("state: begin traffic policy mutation: %w", beginErr)
			}
			_, err = sqlc.New().LockTrafficPolicyAccount(ctx, tx, account)
			if err == nil {
				guarded := &trafficPolicyMutationTx{Tx: tx, account: account, globalRoutes: globalRoutes, release: release}
				if err := guarded.readBefore(ctx); err != nil {
					_ = guarded.Rollback(context.WithoutCancel(ctx))
					return nil, err
				}
				return guarded, nil
			}
			_ = tx.Rollback(context.WithoutCancel(ctx))
			release(ctx)
			var pgErr *pgconn.PgError
			if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.LockNotAvailable && pgErr.Code != pgerrcode.SerializationFailure {
				return nil, fmt.Errorf("state: lock traffic policy account: %w", mapErr(err))
			}
		}
		// The holder may need ordinary pool reads; wait without a connection.
		timer := time.NewTimer(api.TrafficPolicyMutationLockRetry)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
}

// Keep the verdict and the saved intent inside the same account lock. Every
// caller already rolls back on a failed Commit, including its change ledger.
type trafficPolicyMutationTx struct {
	pgx.Tx
	account      pgtype.UUID
	before       trafficHostAnalysis
	globalRoutes bool
	globalBefore trafficHostAnalysis
	release      func(context.Context)
}

func (tx *trafficPolicyMutationTx) readBefore(ctx context.Context) error {
	if err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		var err error
		tx.before, err = readTrafficHostAnalysis(bounded, tx.Tx, tx.account)
		return err
	}); err != nil {
		return err
	}
	if tx.globalRoutes {
		return globalTrafficPolicyError(boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
			var err error
			tx.globalBefore, err = readTrafficHostAnalysis(bounded, tx.Tx, pgtype.UUID{})
			return err
		}))
	}
	return nil
}

func (tx *trafficPolicyMutationTx) Commit(ctx context.Context) error {
	if err := boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
		after, err := readTrafficHostAnalysis(bounded, tx.Tx, tx.account)
		if err != nil {
			return err
		}
		return checkTrafficHostAnalysis(bounded, tx.before, after)
	}); err != nil {
		return err
	}
	if tx.globalRoutes {
		if err := globalTrafficPolicyError(boundedTrafficPolicyAnalysis(ctx, func(bounded context.Context) error {
			after, err := readTrafficHostAnalysis(bounded, tx.Tx, pgtype.UUID{})
			if err != nil {
				return err
			}
			return checkTrafficHostAnalysis(bounded, tx.globalBefore, after)
		})); err != nil {
			return err
		}
	}
	defer tx.release(ctx)
	return tx.Tx.Commit(ctx)
}

func (tx *trafficPolicyMutationTx) Rollback(ctx context.Context) error {
	defer tx.release(ctx)
	return tx.Tx.Rollback(ctx)
}

func boundedTrafficPolicyAnalysis(ctx context.Context, analyze func(context.Context) error) error {
	start := time.Now()
	bounded, cancel := context.WithTimeout(ctx, api.TrafficPolicyAnalysisTimeout)
	defer cancel()
	err := analyze(bounded)
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(bounded.Err(), context.DeadlineExceeded) {
		limit := api.TrafficPolicyAnalysisTimeout.Milliseconds()
		return analysisLimit("time", "milliseconds", limit, max(limit+1, time.Since(start).Milliseconds()))
	}
	return err
}

func (s *PgStore) beginEdgeRuleTrafficMutation(ctx context.Context, id string) (pgx.Tx, error) {
	owner, err := sqlc.New().ReadEdgeRuleTrafficAccount(ctx, s.pool, uuidToPgtype(id))
	if err != nil {
		return nil, fmt.Errorf("state: read traffic policy owner: %w", mapErr(err))
	}
	return s.beginRuleTrafficPolicyMutation(ctx, owner.AccountID, EdgeRuleKind(owner.Kind))
}

func (s *PgStore) beginAppTrafficMutation(ctx context.Context, id string) (pgx.Tx, error) {
	account, err := sqlc.New().ReadAppTrafficAccount(ctx, s.pool, uuidToPgtype(id))
	if err != nil {
		return nil, fmt.Errorf("state: read app traffic owner: %w", mapErr(err))
	}
	return s.beginTrafficPolicyMutation(ctx, account)
}

func (s *PgStore) beginAppConfigMutation(ctx context.Context, id string, p UpdateAppParams) (pgx.Tx, error) {
	if appConfigIntroducesPublicScope(p) {
		return s.beginAppTrafficMutation(ctx, id)
	}
	return s.pool.BeginTx(ctx, pgx.TxOptions{})
}

func appConfigIntroducesPublicScope(p UpdateAppParams) bool {
	if p.SetVisibility && api.NormalizeAppVisibility(derefAppVisibility(p.Visibility)) == api.AppVisibilityInternal {
		return false
	}
	if p.Status != nil {
		return *p.Status != AppDeleted
	}
	return p.SetVisibility
}

func (s *PgStore) compareAndSetAppTrafficStatus(ctx context.Context, id string, from, to AppStatus) (bool, error) {
	tx, err := s.beginAppTrafficMutation(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		return false, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	changed, err := sqlc.New().CompareAndSetTrafficAppStatus(ctx, tx, sqlc.CompareAndSetTrafficAppStatusParams{
		AppID: uuidToPgtype(id), Prior: string(from), Next: string(to)})
	if err != nil || changed == 0 {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	return true, nil
}

func readBoundedEdgeRuleTrafficProjection(ctx context.Context, tx pgx.Tx, mutation pgx.Row) (EdgeRule, error) {
	var id string
	if err := mutation.Scan(&id); err != nil {
		return EdgeRule{}, mapErr(err)
	}
	row, err := sqlc.New().ReadBoundedTrafficEdgeRule(ctx, tx, sqlc.ReadBoundedTrafficEdgeRuleParams{
		RuleID: uuidToPgtype(id), MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err != nil {
		return EdgeRule{}, fmt.Errorf("state: read edge rule traffic projection: %w", mapErr(err))
	}
	if err := checkTrafficProjectionLimit("edge_rule", row.Observed, api.TrafficPolicyMaxHostBytes); err != nil {
		return EdgeRule{}, err
	}
	var rule EdgeRule
	if err := json.Unmarshal(row.Data, &rule); err != nil {
		return EdgeRule{}, fmt.Errorf("state: decode edge rule traffic projection: %w", err)
	}
	// Preserve the existing store's nullable FK-to-action mirror.
	if rule.CorsPresetID != nil && rule.Action.CORS != nil {
		rule.Action.CORS.CorsPresetID = rule.CorsPresetID
	}
	return rule, nil
}

func validateMemEdgeRuleTrafficProjection(rule EdgeRule) error {
	observed, err := memEdgeRuleTrafficProjectionSize(rule)
	if err != nil {
		return err
	}
	// Reserve differing JSONB/Go timestamp punctuation and zone spellings.
	return checkTrafficProjectionLimit("edge_rule", observed+16, api.TrafficPolicyMaxHostBytes)
}

func memEdgeRuleTrafficProjectionSize(rule EdgeRule) (int64, error) {
	// ReadPublicHostEdgeRules emits JSON null for absent manifest keys, but
	// empty arrays/objects for the non-null match fields.
	var manifest any
	if rule.ManifestKey != "" {
		manifest = rule.ManifestKey
	}
	headers := rule.MatchHeaders
	if headers == nil {
		headers = map[string]string{}
	}
	mode := rule.ValidateMode
	if mode == "" {
		mode = "block"
	}
	projection := []any{map[string]any{
		"ID": rule.ID, "AccountID": rule.AccountID, "AppID": rule.AppID,
		"MatchHost": rule.MatchHost, "MatchPath": rule.MatchPath, "MatchMethods": nilToEmpty(rule.MatchMethods),
		"MatchHeaders": headers, "Priority": rule.Priority, "Enabled": rule.Enabled,
		"Kind": rule.Kind, "Action": rule.Action, "CorsPresetID": rule.CorsPresetID,
		"ValidateMode": mode, "CreatedAt": rule.CreatedAt, "UpdatedAt": rule.UpdatedAt, "ManifestKey": manifest,
	}}
	return memTrafficProjectionSize("edge_rule", projection)
}
