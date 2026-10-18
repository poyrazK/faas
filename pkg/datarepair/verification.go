package datarepair

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// VerificationReport separates the durable commit result from current,
// read-only observations. Later edits do not change whether the repair committed.
// No current business values, check execution or new write authority are exposed.
type VerificationReport struct {
	PlanID            string          `json:"plan_id"`
	PlanDigest        string          `json:"plan_digest"`
	Scope             Scope           `json:"scope"`
	SourceOperationID string          `json:"source_operation_id"`
	RepairOperationID string          `json:"repair_operation_id"`
	AppliedAt         time.Time       `json:"applied_at"`
	CheckedAt         time.Time       `json:"checked_at"`
	Restored          int             `json:"restored"`
	Excluded          int             `json:"excluded"`
	Available         bool            `json:"available"`
	Checked           int             `json:"checked"`
	Unchanged         int             `json:"unchanged"`
	Edited            int             `json:"edited"`
	Missing           int             `json:"missing"`
	Issues            []string        `json:"issues"`
	Rows              []ValidationRow `json:"rows"`
}

func (r VerificationReport) Valid() bool {
	if !validID(r.PlanID) || !digestPattern.MatchString(r.PlanDigest) || r.Scope.Validate() != nil ||
		!validID(r.SourceOperationID) || !validID(r.RepairOperationID) || r.SourceOperationID == r.RepairOperationID ||
		r.AppliedAt.IsZero() || r.CheckedAt.IsZero() || r.Restored < 1 || r.Restored > api.DataRepairMaxRows ||
		r.Excluded < 0 || r.Excluded > api.DataRepairMaxRows-r.Restored || r.Checked < 0 || r.Checked > r.Restored ||
		r.Unchanged < 0 || r.Unchanged > r.Checked || r.Edited < 0 || r.Edited > r.Checked || r.Missing < 0 || r.Missing > r.Checked || r.Checked != r.Unchanged+r.Edited+r.Missing ||
		r.Rows == nil || len(r.Rows) != r.Edited+r.Missing || r.Issues == nil {
		return false
	}
	if r.Available {
		if len(r.Issues) != 0 || r.Checked != r.Restored {
			return false
		}
	} else if len(r.Issues) != 1 || (r.Issues[0] != "journal_purged" && r.Issues[0] != "schema_changed") || r.Checked != 0 {
		return false
	}
	missing := 0
	for _, row := range r.Rows {
		if row.Reason == ReasonRecordMissing {
			missing++
		}
	}
	return missing == r.Missing && validValidationRows(r.Rows)
}

// MatchesPlan binds observations to the source's exact committed receipt,
// including after plan compaction. It never authorizes another apply.
func (r VerificationReport) MatchesPlan(p Plan) bool {
	return r.Valid() && r.PlanID == p.ID && r.PlanDigest == p.Digest && r.Scope == p.Scope && r.SourceOperationID == p.SourceOperationID &&
		p.AppliedAt != nil && r.AppliedAt.Equal(*p.AppliedAt) && p.RepairOperationID != nil && r.RepairOperationID == *p.RepairOperationID &&
		r.Restored == p.Candidates && r.Excluded == p.Conflicts+p.Unavailable && p.ApplyResolution == nil
}

// Verify reads one repeatable-read snapshot using only the committed repair
// journal and qualified live rows. Plan expiry and current check configuration
// cannot alter the historical receipt or cause business checks to run again.
func (e *Engine) Verify(ctx context.Context, s Scope, id, digest string) (VerificationReport, error) {
	if err := e.ready(s); err != nil {
		return VerificationReport{}, err
	}
	if !validID(id) || !digestPattern.MatchString(digest) {
		return VerificationReport{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return VerificationReport{}, classify(err)
	}
	defer func() {
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
		defer stop()
		_ = tx.Rollback(clean)
	}()
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true)`, strconv.Itoa(api.DataRepairTimeoutSeconds*1000)); err != nil {
		return VerificationReport{}, classify(err)
	}
	p, err := readPlan(ctx, tx, `SELECT gregale_repair.get_plan($1::uuid,$2::jsonb)`, id, scopeJSON(s))
	if err != nil {
		return VerificationReport{}, err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT gregale_repair.verify_plan($1::uuid,$2::jsonb,$3)`, id, scopeJSON(s), digest).Scan(&raw); err != nil {
		return VerificationReport{}, classify(err)
	}
	var report VerificationReport
	if len(raw) > api.DataRepairMaxPlanBytes || json.Unmarshal(raw, &report) != nil || !report.MatchesPlan(p) {
		return VerificationReport{}, ErrUnavailable
	}
	return report, nil
}
