package datarepair

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/pkg/api"
)

// RetentionEntry contains only identity, a state digest and logical payload size.
// Row keys, before/after values and executable patches never enter this document.
type RetentionEntry struct {
	Kind                  string     `json:"kind"`
	ID                    string     `json:"id"`
	SourceOperationID     string     `json:"source_operation_id,omitempty"`
	RecordDigest          string     `json:"record_digest"`
	PayloadBytes          int64      `json:"payload_bytes"`
	ChangeRows            int64      `json:"change_rows"`
	Reason                string     `json:"reason"`
	ReplacementPlanID     string     `json:"replacement_plan_id,omitempty"`
	ReplacementPlanDigest string     `json:"replacement_plan_digest,omitempty"`
	SupersededAt          *time.Time `json:"superseded_at,omitempty"`
}

// RetentionHold reports a fixed reason group, rather than an unbounded list of
// held identities. It describes the saved inventory's cutoff and source state.
type RetentionHold struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
	Count  int64  `json:"count"`
}

type RetentionPlan struct {
	ID                    string           `json:"id"`
	Digest                string           `json:"digest"`
	Scope                 Scope            `json:"scope"`
	Before                time.Time        `json:"before"`
	CreatedAt             time.Time        `json:"created_at"`
	ExpiresAt             time.Time        `json:"expires_at"`
	Entries               []RetentionEntry `json:"entries"`
	EstimatedPayloadBytes int64            `json:"estimated_payload_bytes"`
	HeldApprovedPlans     int64            `json:"held_approved_plans"`
	Holds                 []RetentionHold  `json:"holds,omitempty"`
	RequestedByRole       string           `json:"requested_by_role"`
	AppliedAt             *time.Time       `json:"applied_at"`
	AppliedBy             *string          `json:"applied_by"`
	AppliedRole           *string          `json:"applied_role"`
}

var ErrPurgeOutcomeUnknown = errors.New("datarepair: purge commit outcome unknown; inspect the saved retention plan and reuse its ID and digest")

func readRetentionPlan(ctx context.Context, q rowQuerier, query string, args ...any) (RetentionPlan, error) {
	var raw []byte
	var p RetentionPlan
	if err := q.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		return p, classify(err)
	}
	if json.Unmarshal(raw, &p) != nil || !validID(p.ID) || !digestPattern.MatchString(p.Digest) || p.Scope.Validate() != nil || p.Entries == nil {
		return RetentionPlan{}, ErrUnavailable
	}
	return p, nil
}

// PlanRetention saves a frozen, data-free inventory. A zero cutoff selects the
// database's seven-day minimum. Older cutoffs can enforce a longer policy.
func (e *Engine) PlanRetention(parent context.Context, s Scope, before time.Time, limit int) (RetentionPlan, error) {
	if err := e.ready(s); err != nil {
		return RetentionPlan{}, err
	}
	if limit < 1 || limit > api.DataRepairPurgeMaxItems {
		return RetentionPlan{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.DataRepairPurgeTimeoutSeconds)*time.Second)
	defer cancel()
	var cutoff any
	if !before.IsZero() {
		cutoff = before.UTC()
	}
	return readRetentionPlan(ctx, e.Pool, `SELECT gregale_repair.prepare_retention($1::jsonb,$2::timestamptz,$3::integer)`, scopeJSON(s), cutoff, limit)
}

func (e *Engine) GetRetention(parent context.Context, s Scope, id string) (RetentionPlan, error) {
	if err := e.ready(s); err != nil {
		return RetentionPlan{}, err
	}
	if !validID(id) {
		return RetentionPlan{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.DataRepairPurgeTimeoutSeconds)*time.Second)
	defer cancel()
	return readRetentionPlan(ctx, e.Pool, `SELECT gregale_repair.get_retention_plan($1::uuid,$2::jsonb)`, id, scopeJSON(s))
}

// PurgeRetention compacts exactly the reviewed payloads. Identities, receipts,
// enrollment and row versions remain durable. Replays return the original audit
// receipt, including after expiry; mutations are never retried on a lost COMMIT.
func (e *Engine) PurgeRetention(parent context.Context, s Scope, id, digest, actor string) (RetentionPlan, error) {
	if err := e.ready(s); err != nil {
		return RetentionPlan{}, err
	}
	if !validID(id) || !digestPattern.MatchString(digest) || actor == "" || len(actor) > api.DataRepairActorMaxBytes {
		return RetentionPlan{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(api.DataRepairPurgeTimeoutSeconds)*time.Second)
	defer cancel()
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.ReadCommitted})
	if err != nil {
		return RetentionPlan{}, classify(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.WithoutCancel(parent), time.Duration(api.DataRepairPurgeTimeoutSeconds)*time.Second)
		defer stop()
		_ = tx.Rollback(clean)
	}()
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)`, strconv.Itoa(api.DataRepairPurgeTimeoutSeconds*1000), strconv.Itoa(api.DataRepairPurgeLockTimeoutMS)); err != nil {
		return RetentionPlan{}, classify(err)
	}
	p, err := readRetentionPlan(ctx, tx, `SELECT gregale_repair.apply_retention($1::uuid,$2::jsonb,$3,$4)`, id, scopeJSON(s), digest, actor)
	if err != nil {
		return RetentionPlan{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			return RetentionPlan{}, classify(err)
		}
		return RetentionPlan{}, ErrPurgeOutcomeUnknown
	}
	return p, nil
}
