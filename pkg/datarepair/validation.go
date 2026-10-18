package datarepair

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// ValidationReport is an observation of the original candidate set, not repair
// evidence or approval. It deliberately contains no current business values.
type ValidationReport struct {
	PlanID     string          `json:"plan_id"`
	PlanDigest string          `json:"plan_digest"`
	Scope      Scope           `json:"scope"`
	CheckedAt  time.Time       `json:"checked_at"`
	ExpiresAt  time.Time       `json:"expires_at"`
	Matches    bool            `json:"matches"`
	Candidates int             `json:"candidates"`
	Excluded   int             `json:"excluded"`
	Checked    int             `json:"checked"`
	Unchanged  int             `json:"unchanged"`
	Changed    int             `json:"changed"`
	Issues     []string        `json:"issues"`
	Rows       []ValidationRow `json:"rows"`
}

type ValidationRow struct {
	RowIdentity     string `json:"row_identity"`
	KeyJSON         string `json:"key_json,omitempty"`
	KeyOmitted      bool   `json:"key_omitted,omitempty"`
	ExpectedVersion string `json:"expected_version"`
	ObservedVersion string `json:"observed_version,omitempty"`
	Reason          string `json:"reason"`
}

func (r ValidationReport) Valid() bool {
	if !validID(r.PlanID) || !digestPattern.MatchString(r.PlanDigest) || r.Scope.Validate() != nil || r.CheckedAt.IsZero() || r.ExpiresAt.IsZero() ||
		r.Candidates < 0 || r.Candidates > api.DataRepairMaxRows || r.Excluded < 0 || r.Excluded > api.DataRepairMaxRows || r.Candidates+r.Excluded > api.DataRepairMaxRows ||
		r.Checked < 0 || r.Checked > r.Candidates || r.Unchanged < 0 || r.Changed < 0 || r.Checked != r.Unchanged+r.Changed ||
		r.Rows == nil || len(r.Rows) != r.Changed || r.Issues == nil {
		return false
	}
	issues := map[string]bool{}
	for _, issue := range r.Issues {
		switch issue {
		case "plan_expired", "plan_applied", "plan_replaced", "apply_closed", "payload_purged", "no_candidates", "schema_changed", "checks_changed", "candidate_drift":
		default:
			return false
		}
		if issues[issue] {
			return false
		}
		issues[issue] = true
	}
	if issues["plan_expired"] != !r.CheckedAt.Before(r.ExpiresAt) || issues["no_candidates"] != (r.Candidates == 0) ||
		issues["candidate_drift"] != (r.Changed > 0) || r.Matches != (len(issues) == 0) ||
		(r.Checked != r.Candidates && !issues["schema_changed"] && !issues["plan_applied"] && !issues["plan_replaced"] && !issues["apply_closed"] && !issues["payload_purged"]) {
		return false
	}
	return validValidationRows(r.Rows)
}

func validValidationRows(rows []ValidationRow) bool {
	seen, keyBytes := map[string]bool{}, 0
	for _, row := range rows {
		if !validID(row.RowIdentity) || seen[row.RowIdentity] || !validVersion(row.ExpectedVersion) ||
			(row.KeyOmitted && row.KeyJSON != "") || (!row.KeyOmitted && !json.Valid([]byte(row.KeyJSON))) || len(row.KeyJSON) > api.DataRepairMaxObservedRowBytes {
			return false
		}
		seen[row.RowIdentity] = true
		keyBytes += len(row.KeyJSON)
		switch row.Reason {
		case ReasonRecordMissing:
			if row.ObservedVersion != "" {
				return false
			}
		case ReasonVersionChanged:
			if !validVersion(row.ObservedVersion) || row.ObservedVersion == row.ExpectedVersion {
				return false
			}
		case ReasonValuesChanged:
			if row.ObservedVersion != row.ExpectedVersion {
				return false
			}
		default:
			return false
		}
	}
	return keyBytes <= api.DataRepairMaxObservedPlanBytes
}

func validVersion(value string) bool {
	n, err := strconv.ParseInt(value, 10, 64)
	return err == nil && n > 0 && strconv.FormatInt(n, 10) == value
}

// Validate uses one read-only repeatable-read snapshot. It neither executes
// business checks nor refreshes candidates, expiry, rehearsal, or approval.
func (e *Engine) Validate(ctx context.Context, s Scope, id, digest string) (ValidationReport, error) {
	if err := e.ready(s); err != nil {
		return ValidationReport{}, err
	}
	if !validID(id) || !digestPattern.MatchString(digest) {
		return ValidationReport{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairTimeoutSeconds)*time.Second)
	defer cancel()
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return ValidationReport{}, classify(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(clean)
	}()
	if _, err := tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true)`, fmt.Sprint(api.DataRepairTimeoutSeconds*1000)); err != nil {
		return ValidationReport{}, classify(err)
	}
	p, err := readPlan(ctx, tx, `SELECT gregale_repair.get_plan($1::uuid,$2::jsonb)`, id, scopeJSON(s))
	if err != nil {
		return ValidationReport{}, err
	}
	if p.Digest != digest {
		return ValidationReport{}, ErrConflict
	}
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT gregale_repair.validate_plan($1::uuid,$2::jsonb,$3)`, id, scopeJSON(s), digest).Scan(&raw); err != nil {
		return ValidationReport{}, classify(err)
	}
	var report ValidationReport
	if len(raw) > api.DataRepairMaxPlanBytes || json.Unmarshal(raw, &report) != nil || !report.attachKeys(p) {
		return ValidationReport{}, ErrUnavailable
	}
	set, policyErr := configuredChecks(e.CheckSQL, e.Checks)
	if policyErr != nil || !set.matches(p) {
		report.Issues = append(report.Issues, "checks_changed")
		report.Matches = false
	}
	if !report.Valid() {
		return ValidationReport{}, ErrUnavailable
	}
	return report, nil
}

func (r *ValidationReport) attachKeys(p Plan) bool {
	if r.PlanID != p.ID || r.PlanDigest != p.Digest || r.Scope != p.Scope || !r.ExpiresAt.Equal(p.ExpiresAt) || r.Candidates != p.Candidates || r.Excluded != p.Conflicts+p.Unavailable {
		return false
	}
	candidates := map[string]Item{}
	for _, item := range p.Items {
		if item.Status == "candidate" {
			candidates[item.RowIdentity] = item
		}
	}
	keyBytes := 0
	for i := range r.Rows {
		row := &r.Rows[i]
		item, ok := candidates[row.RowIdentity]
		if !ok || row.ExpectedVersion != item.ExpectedVersion {
			return false
		}
		key := string(item.Key)
		if len(key) > api.DataRepairMaxObservedRowBytes || keyBytes+len(key) > api.DataRepairMaxObservedPlanBytes {
			row.KeyOmitted = true
		} else {
			row.KeyJSON = key
			keyBytes += len(key)
		}
	}
	return true
}
