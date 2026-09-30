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

// Account first, then app/FK/policy rows. A shared preset can contribute to
// policies belonging to several apps, so an app lock alone is insufficient.
func (s *PgStore) beginTrafficPolicyMutation(ctx context.Context, account pgtype.UUID) (pgx.Tx, error) {
	for {
		tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{})
		if err != nil {
			return nil, fmt.Errorf("state: begin traffic policy mutation: %w", err)
		}
		_, err = sqlc.New().LockTrafficPolicyAccount(ctx, tx, account)
		if err == nil {
			return tx, nil
		}
		_ = tx.Rollback(context.WithoutCancel(ctx))
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != pgerrcode.LockNotAvailable {
			return nil, fmt.Errorf("state: lock traffic policy account: %w", mapErr(err))
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

func (s *PgStore) beginEdgeRuleTrafficMutation(ctx context.Context, id string) (pgx.Tx, error) {
	account, err := sqlc.New().ReadEdgeRuleTrafficAccount(ctx, s.pool, uuidToPgtype(id))
	if err != nil {
		return nil, fmt.Errorf("state: read traffic policy owner: %w", mapErr(err))
	}
	return s.beginTrafficPolicyMutation(ctx, account)
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
	observed, err := memTrafficProjectionSize("edge_rule", projection)
	if err != nil {
		return err
	}
	// Reserve differing JSONB/Go timestamp punctuation and zone spellings.
	return checkTrafficProjectionLimit("edge_rule", observed+16, api.TrafficPolicyMaxHostBytes)
}
