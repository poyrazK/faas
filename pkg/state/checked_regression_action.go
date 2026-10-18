package state

import (
	"context"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"github.com/onebox-faas/faas/pkg/api"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// CheckedRegressionActionStore is PostgreSQL-only, like retained observations.
// Rendering is pure: the update and its rendered receipt commit together.
type CheckedRegressionActionStore interface {
	ApplyCheckedRegressionAction(context.Context, string, [32]byte, string, sqlc.ApplyRegressionActionParams, func(sqlc.DebugRegressionObservation) ([]byte, error)) ([]byte, bool, error)
}

var _ CheckedRegressionActionStore = (*PgStore)(nil)

type regressionActionReceipt struct {
	Reason string          `json:"reason"`
	Body   json.RawMessage `json:"body"`
}

// ApplyCheckedRegressionAction serializes identical reviewed bodies, compares
// the observation version and stores the response in the SAME transaction.
// A native process crash commits both or neither. Native cache GC retains the
// existing 24-hour policy; MCP plans expire after fifteen minutes.
func (s *PgStore) ApplyCheckedRegressionAction(ctx context.Context, accountID string, digest [32]byte, reason string, arg sqlc.ApplyRegressionActionParams, render func(sqlc.DebugRegressionObservation) ([]byte, error)) ([]byte, bool, error) {
	if !arg.ExpectedVersion.Valid || !arg.AppID.Valid || !arg.DeploymentID.Valid || render == nil || strings.TrimSpace(reason) == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 280 {
		return nil, false, ErrInvalidArgument
	}
	var account pgtype.UUID
	if err := account.Scan(accountID); err != nil {
		return nil, false, fmt.Errorf("regression receipt account: %w", err)
	}
	key := "checked-regression:" + hex.EncodeToString(digest[:])
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, false, fmt.Errorf("begin checked regression action: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := &sqlc.Queries{}
	if err := q.ClaimRegressionActionReceipt(ctx, tx, sqlc.ClaimRegressionActionReceiptParams{AccountID: account, Key: key, RequestDigest: digest[:]}); err != nil {
		return nil, false, fmt.Errorf("claim regression receipt: %w", err)
	}
	prior, err := q.LockRegressionActionReceipt(ctx, tx, sqlc.LockRegressionActionReceiptParams{AccountID: account, Key: key})
	if err != nil {
		return nil, false, fmt.Errorf("lock regression receipt: %w", err)
	}
	if len(prior.RequestDigest) != 32 || subtle.ConstantTimeCompare(prior.RequestDigest, digest[:]) != 1 {
		return nil, false, fmt.Errorf("regression receipt request binding changed")
	}
	if prior.ResponseStatus != 0 {
		var saved regressionActionReceipt
		if prior.ResponseStatus != 200 || json.Unmarshal(prior.ResponseBody, &saved) != nil || len(saved.Body) == 0 || saved.Reason != reason {
			return nil, false, fmt.Errorf("invalid committed regression receipt")
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, false, fmt.Errorf("finish regression replay: %w", err)
		}
		return []byte(saved.Body), true, nil
	}

	if arg.State == "dismissed" {
		now := time.Now().UTC()
		if !arg.Column5.Valid || !arg.Column5.Time.After(now) || arg.Column5.Time.After(now.Add(api.DebugRegressionMaxDismissal)) {
			return nil, false, ErrInvalidArgument
		}
	}
	row, err := q.ApplyRegressionAction(ctx, tx, arg)
	if err != nil {
		return nil, false, fmt.Errorf("checked regression action: %w", err)
	}
	body, err := render(row)
	if err != nil {
		return nil, false, fmt.Errorf("render regression receipt: %w", err)
	}
	if len(body) == 0 || !json.Valid(body) {
		return nil, false, fmt.Errorf("invalid rendered regression receipt")
	}
	saved, err := json.Marshal(regressionActionReceipt{Reason: reason, Body: body})
	if err != nil {
		return nil, false, fmt.Errorf("encode regression receipt: %w", err)
	}
	n, err := q.SaveRegressionActionReceipt(ctx, tx, sqlc.SaveRegressionActionReceiptParams{AccountID: account, Key: key, RequestDigest: digest[:], ResponseBody: saved})
	if err != nil {
		return nil, false, fmt.Errorf("save regression receipt: %w", err)
	}
	if n != 1 {
		return nil, false, fmt.Errorf("regression receipt reservation changed")
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, fmt.Errorf("commit checked regression action: %w", err)
	}
	return body, false, nil
}
