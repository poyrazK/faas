package datarepair

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
)

// Scope comes from authenticated application configuration, never the repair body.
type Scope struct {
	AccountID        string `json:"account_id"`
	AppID            string `json:"app_id"`
	Environment      string `json:"environment"`
	PlatformTenantID string `json:"platform_tenant_id,omitempty"`
}

func (s Scope) Validate() error {
	for _, v := range []string{s.AccountID, s.AppID} {
		if id, err := uuid.Parse(v); err != nil || id == uuid.Nil || id.String() != v {
			return ErrInvalid
		}
	}
	if s.Environment == "" || len(s.Environment) > 256 {
		return ErrInvalid
	}
	if s.PlatformTenantID != "" {
		if id, err := uuid.Parse(s.PlatformTenantID); err != nil || id == uuid.Nil || id.String() != s.PlatformTenantID {
			return ErrInvalid
		}
	}
	return nil
}

type Item struct {
	RowIdentity     string          `json:"row_identity"`
	Key             json.RawMessage `json:"key"`
	ExpectedVersion string          `json:"expected_version"`
	Before          json.RawMessage `json:"before"`
	After           json.RawMessage `json:"after"`
	Status          string          `json:"status"`
	// Diagnostics are frozen at preparation. Only Before/After and the original
	// expected version participate in the inverse write; observations grant no
	// authority to overwrite a later edit.
	Reason             string          `json:"reason,omitempty"`
	ObservedVersion    string          `json:"observed_version,omitempty"`
	ObservedValues     json.RawMessage `json:"observed_values,omitempty"`
	ObservationOmitted bool            `json:"observation_omitted,omitempty"`
	KeyJSON            string          `json:"key_json,omitempty"`
	BeforeJSON         string          `json:"before_json,omitempty"`
	AfterJSON          string          `json:"after_json,omitempty"`
	ObservedValuesJSON string          `json:"observed_values_json,omitempty"`
	PreviousStatus     string          `json:"previous_status,omitempty"`
}

const (
	ReasonMatchesRecordedWrite = "matches_recorded_write"
	ReasonVersionChanged       = "record_version_changed"
	ReasonValuesChanged        = "recorded_values_changed"
	ReasonRecordMissing        = "record_missing"
)

type Plan struct {
	ID                string            `json:"id"`
	Digest            string            `json:"digest"`
	SourceOperationID string            `json:"source_operation_id"`
	SourceRelease     string            `json:"source_release"`
	Scope             Scope             `json:"scope"`
	EnrollmentID      string            `json:"enrollment_id"`
	Table             string            `json:"table"`
	SchemaFingerprint string            `json:"schema_fingerprint"`
	ChecksSHA256      string            `json:"checks_sha256"`
	Checks            []CheckDescriptor `json:"checks,omitempty"`
	CheckFailure      *CheckFailure     `json:"check_failure,omitempty"`
	ApplyChecks       *CheckReport      `json:"apply_checks,omitempty"`
	Items             []Item            `json:"items"`
	Candidates        int               `json:"candidates"`
	Conflicts         int               `json:"conflicts"`
	Unavailable       int               `json:"unavailable"`
	ExpiresAt         time.Time         `json:"expires_at"`
	RehearsedAt       *time.Time        `json:"rehearsed_at"`
	Rehearsal         *Evidence         `json:"rehearsal"`
	ApprovedAt        *time.Time        `json:"approved_at"`
	ApprovedBy        *string           `json:"approved_by"`
	AppliedAt         *time.Time        `json:"applied_at"`
	RepairOperationID *string           `json:"repair_operation_id"`
	PayloadPurgedAt   *time.Time        `json:"payload_purged_at,omitempty"`
	Replacement       *Replacement      `json:"replacement,omitempty"`
	ReplacementPlanID string            `json:"replacement_plan_id,omitempty"`
	ApplyResolution   *ApplyResolution  `json:"apply_resolution,omitempty"`
}

type Evidence struct {
	Passed         bool         `json:"passed"`
	PlanDigest     string       `json:"plan_digest"`
	SnapshotSHA256 string       `json:"snapshot_sha256"`
	Runner         string       `json:"runner"`
	ChecksSHA256   string       `json:"checks_sha256"`
	TaskID         string       `json:"task_id,omitempty"`
	NodeID         string       `json:"node_id,omitempty"`
	RunnerSHA256   string       `json:"runner_sha256,omitempty"`
	RetiredAt      *time.Time   `json:"retired_at,omitempty"`
	CheckResults   *CheckReport `json:"check_results,omitempty"`
}

var (
	ErrInvalid        = errors.New("datarepair: invalid request")
	ErrNotFound       = errors.New("datarepair: unavailable in this scope")
	ErrConflict       = errors.New("datarepair: plan, schema, or row changed; review a new plan")
	ErrUnavailable    = errors.New("datarepair: database or rehearsal unavailable")
	ErrOutcomeUnknown = errors.New("datarepair: commit outcome unknown; inspect the plan and reuse the same repair operation ID")
	ErrPurged         = errors.New("datarepair: captured payload has been purged; retained identities cannot be reused")
)

var digestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Rehearser is a trusted runner, configured by the application operator. Only
// this runner may produce rehearsal evidence; the HTTP API accepts no evidence.
type Rehearser interface {
	Rehearse(context.Context, Plan) (Evidence, error)
}

type Engine struct {
	Pool      *pgxpool.Pool
	Rehearser Rehearser
	// CheckSQL is an operator-reviewed single SELECT returning one boolean.
	// It runs after inverse writes, before commit, and in the isolated rehearsal.
	CheckSQL      string
	Checks        []Check
	rehearsalLock sync.Mutex
}

func checkSQL(raw string) (string, string, error) {
	if raw == "" {
		raw = "SELECT true"
	}
	if len(raw) > api.DataRepairMaxRequestBytes || !utf8.ValidString(raw) || strings.ContainsRune(raw, 0) || !checkSelectPattern.MatchString(strings.TrimSpace(raw)) {
		return "", "", ErrInvalid
	}
	digest := sha256.Sum256([]byte(raw))
	return raw, hex.EncodeToString(digest[:]), nil
}

type rowQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func validID(v string) bool {
	id, err := uuid.Parse(v)
	return err == nil && id != uuid.Nil && id.String() == v
}

func (e *Engine) ready(scope Scope) error {
	if err := scope.Validate(); err != nil {
		return err
	}
	if e == nil || e.Pool == nil {
		return ErrUnavailable
	}
	return nil
}

// classify intentionally drops database messages, which can contain row values.
func classify(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) {
		if pg.Code == "55000" && pg.Message == "repair_payload_purged" {
			return ErrPurged
		}
		switch pg.Code {
		case "P0002":
			return ErrNotFound
		case "40001", "40P01", "23505", "23503", "23514", "55000":
			return ErrConflict
		case "22023", "22P02", "54000":
			return ErrInvalid
		}
	}
	return ErrUnavailable
}

func readPlan(ctx context.Context, q rowQuerier, query string, args ...any) (Plan, error) {
	var raw []byte
	var p Plan
	if err := q.QueryRow(ctx, query, args...).Scan(&raw); err != nil {
		return p, classify(err)
	}
	if err := json.Unmarshal(raw, &p); err != nil {
		return p, ErrUnavailable
	}
	// Browser JSON numbers cannot represent every PostgreSQL integer/decimal.
	// Exact JSON text is presentation-only and never becomes an executable patch.
	for i := range p.Items {
		p.Items[i].KeyJSON = string(p.Items[i].Key)
		p.Items[i].BeforeJSON = string(p.Items[i].Before)
		p.Items[i].AfterJSON = string(p.Items[i].After)
		p.Items[i].ObservedValuesJSON = string(p.Items[i].ObservedValues)
	}
	return p, nil
}

func scopeJSON(s Scope) []byte { b, _ := json.Marshal(s); return b }

func (e *Engine) Plan(ctx context.Context, s Scope, operation string) (Plan, error) {
	return e.Prepare(ctx, s, operation, "")
}

// Prepare reserves a caller-chosen immutable plan identity before network
// submission. Replaying it never expands the selection or extends its expiry.
func (e *Engine) Prepare(ctx context.Context, s Scope, operation, id string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(operation) || (id != "" && !validID(id)) {
		return Plan{}, ErrInvalid
	}
	set, err := configuredChecks(e.CheckSQL, e.Checks)
	if err != nil {
		return Plan{}, err
	}
	var planID any
	if id != "" {
		planID = id
	}
	if set.checks != nil {
		manifest, _ := json.Marshal(set.descriptors)
		return readPlan(ctx, e.Pool, `SELECT gregale_repair.create_plan_with_checks($1::uuid,$2::jsonb,$3,$4::uuid,$5::jsonb)`, operation, scopeJSON(s), set.digest, planID, manifest)
	}
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.create_plan($1::uuid,$2::jsonb,$3,$4::uuid)`, operation, scopeJSON(s), set.digest, planID)
}

func (e *Engine) Get(ctx context.Context, s Scope, id string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(id) {
		return Plan{}, ErrInvalid
	}
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.get_plan($1::uuid,$2::jsonb)`, id, scopeJSON(s))
}

func (e *Engine) Rehearse(ctx context.Context, s Scope, id, digest string) (Plan, error) {
	p, err := e.Get(ctx, s, id)
	if err != nil {
		return p, err
	}
	if !digestPattern.MatchString(digest) || p.Digest != digest || p.Candidates < 1 || p.PayloadPurgedAt != nil || p.ReplacementPlanID != "" || p.ApplyResolution != nil || p.ApprovedAt != nil || !time.Now().Before(p.ExpiresAt) {
		return Plan{}, ErrConflict
	}
	set, checkErr := configuredChecks(e.CheckSQL, e.Checks)
	if checkErr != nil || !set.matches(p) {
		return Plan{}, ErrConflict
	}
	if e.Rehearser == nil {
		return Plan{}, ErrUnavailable
	}
	if !e.rehearsalLock.TryLock() {
		return Plan{}, ErrUnavailable
	}
	defer e.rehearsalLock.Unlock()
	evidence, err := e.Rehearser.Rehearse(ctx, p)
	if err != nil {
		var stage *RehearsalError
		if errors.As(err, &stage) {
			return Plan{}, stage
		}
		return Plan{}, ErrUnavailable
	}
	if !p.ValidCheckResults(evidence) || evidence.PlanDigest != digest || evidence.ChecksSHA256 != p.ChecksSHA256 || !digestPattern.MatchString(evidence.SnapshotSHA256) || evidence.Runner == "" {
		return Plan{}, ErrUnavailable
	}
	if !evidence.Passed {
		failed, err := e.recordCheckFailure(ctx, s, p, "rehearsal", evidence)
		if err != nil {
			return Plan{}, err
		}
		return failed, &CheckError{Report: evidence.CheckResults}
	}
	b, _ := json.Marshal(evidence)
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.record_rehearsal($1::uuid,$2::jsonb,$3,$4::jsonb)`, id, scopeJSON(s), digest, b)
}

func (e *Engine) Approve(ctx context.Context, s Scope, id, digest, actor string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(id) || !digestPattern.MatchString(digest) || actor == "" || len(actor) > api.DataRepairActorMaxBytes {
		return Plan{}, ErrInvalid
	}
	p, err := e.Get(ctx, s, id)
	if err != nil {
		return Plan{}, err
	}
	if len(p.Checks) != 0 {
		set, err := configuredChecks(e.CheckSQL, e.Checks)
		if err != nil || !set.matches(p) {
			return Plan{}, ErrConflict
		}
	}
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.approve_plan($1::uuid,$2::jsonb,$3,$4)`, id, scopeJSON(s), digest, actor)
}

// Apply commits the repair journal and receipt with the inverse writes. An
// ambiguous COMMIT is never retried here. Get establishes the durable outcome.
func (e *Engine) Apply(ctx context.Context, s Scope, id, digest, repairID, release string) (Plan, error) {
	if err := e.ready(s); err != nil {
		return Plan{}, err
	}
	if !validID(id) || !validID(repairID) || !digestPattern.MatchString(digest) || release == "" || len(release) > 256 {
		return Plan{}, ErrInvalid
	}
	tx, err := e.Pool.Begin(ctx)
	if err != nil {
		return Plan{}, classify(err)
	}
	defer func() {
		clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = tx.Rollback(clean)
	}()
	if _, err = tx.Exec(ctx, `SELECT set_config('statement_timeout',$1,true),set_config('lock_timeout',$2,true)`, fmt.Sprint(api.DataRepairTimeoutSeconds*1000), "2000"); err != nil {
		return Plan{}, classify(err)
	}
	p, err := readPlan(ctx, tx, `SELECT gregale_repair.apply_plan($1::uuid,$2::jsonb,$3,$4::uuid,$5)`, id, scopeJSON(s), digest, repairID, release)
	if err != nil {
		return Plan{}, err
	}
	// A receipt replay returns its original result without rerunning checks or
	// callbacks against later business state. An original application below uses
	// the transaction marker to distinguish its freshly created receipt.
	var fresh bool
	if err = tx.QueryRow(ctx, `SELECT transaction_id=pg_current_xact_id()::text FROM gregale_repair.operations WHERE id=$1`, repairID).Scan(&fresh); err != nil {
		return Plan{}, classify(err)
	}
	if fresh {
		set, checkErr := configuredChecks(e.CheckSQL, e.Checks)
		if checkErr != nil || !set.matches(p) {
			return Plan{}, ErrConflict
		}
		if set.checks != nil {
			report, checkErr := evaluateChecks(ctx, tx, set, p)
			if checkErr != nil {
				var failed *CheckError
				if errors.As(checkErr, &failed) {
					clean, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
					defer cancel()
					if err := tx.Rollback(clean); err == nil {
						_, _ = e.recordCheckFailure(clean, s, p, "apply", Evidence{PlanDigest: p.Digest, ChecksSHA256: p.ChecksSHA256, Runner: "live-transaction", CheckResults: report})
					}
				}
				return Plan{}, checkErr
			}
			b, _ := json.Marshal(report)
			if _, err := tx.Exec(ctx, `UPDATE gregale_repair.plans SET apply_checks=$2::jsonb,check_failure=NULL WHERE id=$1::uuid`, p.ID, b); err != nil {
				return Plan{}, classify(err)
			}
			p.ApplyChecks, p.CheckFailure = report, nil
		} else if !runCheck(ctx, tx, set.query) {
			return Plan{}, ErrConflict
		}
	}
	if err = tx.Commit(ctx); err != nil {
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			return Plan{}, classify(err)
		}
		return Plan{}, ErrOutcomeUnknown
	}
	return p, nil
}

func runCheck(ctx context.Context, tx pgx.Tx, query string) bool {
	rows, err := tx.Query(ctx, query)
	if err != nil {
		return false
	}
	defer rows.Close()
	var passed *bool
	if !rows.Next() || rows.Scan(&passed) != nil || passed == nil || !*passed {
		return false
	}
	return !rows.Next() && rows.Err() == nil
}

// Capture runs inside the caller-owned business transaction. Use the supplied
// transaction only, and propagate every error so the enclosing wrapper rolls back.
func Capture(ctx context.Context, tx pgx.Tx, s Scope, operation, release, table string, handler func(pgx.Tx) error) error {
	if err := s.Validate(); err != nil {
		return err
	}
	if tx == nil || handler == nil || !validID(operation) || release == "" || len(release) > 256 || table == "" {
		return ErrInvalid
	}
	if _, err := tx.Exec(ctx, `SELECT gregale_repair.begin_operation($1::uuid,$2::jsonb,$3,$4::regclass)`, operation, scopeJSON(s), release, table); err != nil {
		return classify(err)
	}
	if err := handler(tx); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `SELECT gregale_repair.finish_operation($1::uuid)`, operation)
	return classify(err)
}
