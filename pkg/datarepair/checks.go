package datarepair

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// Check SQL is trusted operator configuration. Only names and hashes enter
// reviewed plans/results; queries never come from repair HTTP requests.
type Check struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

type CheckPolicy struct {
	Version int     `json:"version"`
	Checks  []Check `json:"checks"`
}

type CheckDescriptor struct {
	Name      string `json:"name"`
	SQLSHA256 string `json:"sql_sha256"`
}

type CheckResult struct {
	Name      string `json:"name"`
	SQLSHA256 string `json:"sql_sha256"`
	Status    string `json:"status"`
}

type CheckReport struct {
	Results []CheckResult `json:"results"`
}

// CheckFailure is review context, not approval or proof of successful rehearsal.
type CheckFailure struct {
	Stage      string   `json:"stage"`
	Evidence   Evidence `json:"evidence"`
	RecordedAt string   `json:"recorded_at"`
}

type CheckError struct{ Report *CheckReport }

func (*CheckError) Error() string { return "datarepair: declared business checks failed" }
func (*CheckError) Unwrap() error { return ErrConflict }

var checkNamePattern = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9 ._-]*$`)
var checkSelectPattern = regexp.MustCompile(`(?i)^SELECT[[:space:]]`)

type checkSet struct {
	query       string
	checks      []Check
	digest      string
	descriptors []CheckDescriptor
}

func configuredChecks(legacy string, named []Check) (checkSet, error) {
	if named == nil {
		query, digest, err := checkSQL(legacy)
		return checkSet{query: query, digest: digest}, err
	}
	if legacy != "" || len(named) < 1 || len(named) > api.DataRepairMaxChecks {
		return checkSet{}, ErrInvalid
	}
	set := checkSet{checks: named}
	seen := map[string]bool{}
	for _, c := range named {
		if c.Name == "" || len(c.Name) > api.DataRepairCheckNameMaxBytes || strings.TrimSpace(c.Name) != c.Name ||
			!checkNamePattern.MatchString(c.Name) || seen[c.Name] || c.SQL == "" {
			return checkSet{}, ErrInvalid
		}
		_, hash, err := checkSQL(c.SQL)
		if err != nil {
			return checkSet{}, err
		}
		seen[c.Name] = true
		set.descriptors = append(set.descriptors, CheckDescriptor{Name: c.Name, SQLSHA256: hash})
	}
	b, err := json.Marshal(CheckPolicy{Version: 1, Checks: named})
	if err != nil || len(b) > api.DataRepairMaxRequestBytes {
		return checkSet{}, ErrInvalid
	}
	set.digest = fmt.Sprintf("%x", sha256.Sum256(append([]byte("gregale-data-repair-checks-v1\n"), b...)))
	return set, nil
}

func ParseCheckPolicy(raw []byte) (CheckPolicy, error) {
	var p CheckPolicy
	if len(raw) > api.DataRepairMaxRequestBytes {
		return p, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || p.Version != 1 || p.Checks == nil {
		return p, ErrInvalid
	}
	_, err := configuredChecks("", p.Checks)
	return p, err
}

func (s checkSet) matches(p Plan) bool {
	return s.digest == p.ChecksSHA256 && slices.Equal(s.descriptors, p.Checks)
}

// Valid checks report structure and consistency with the reported outcome.
// A failure stops execution; the remaining declarations are marked not_run.
func (r *CheckReport) Valid(passed bool) bool {
	if r == nil || len(r.Results) < 1 || len(r.Results) > api.DataRepairMaxChecks {
		return false
	}
	seen, failed := map[string]bool{}, false
	for _, result := range r.Results {
		if len(result.Name) > api.DataRepairCheckNameMaxBytes || strings.TrimSpace(result.Name) != result.Name || !checkNamePattern.MatchString(result.Name) || seen[result.Name] || !digestPattern.MatchString(result.SQLSHA256) {
			return false
		}
		seen[result.Name] = true
		if failed {
			if result.Status != "not_run" {
				return false
			}
		} else {
			switch result.Status {
			case "passed":
			case "failed", "error":
				failed = true
			default:
				return false
			}
		}
	}
	return passed == !failed
}

func (p Plan) ValidCheckResults(e Evidence) bool {
	if len(p.Checks) == 0 {
		return e.Passed && e.CheckResults == nil
	}
	if !e.CheckResults.Valid(e.Passed) || len(e.CheckResults.Results) != len(p.Checks) {
		return false
	}
	for i, d := range p.Checks {
		result := e.CheckResults.Results[i]
		if result.Name != d.Name || result.SQLSHA256 != d.SQLSHA256 {
			return false
		}
	}
	return true
}

func evaluateChecks(ctx context.Context, tx pgx.Tx, set checkSet, p Plan) (*CheckReport, error) {
	policy, _ := json.Marshal(CheckPolicy{Version: 1, Checks: set.checks})
	var raw []byte
	if err := tx.QueryRow(ctx, `SELECT gregale_repair.evaluate_checks($1::jsonb)`, policy).Scan(&raw); err != nil {
		return nil, classify(err)
	}
	var report CheckReport
	if len(raw) > api.DataRepairMaxRequestBytes || json.Unmarshal(raw, &report) != nil {
		return nil, ErrUnavailable
	}
	if p.ValidCheckResults(Evidence{Passed: true, CheckResults: &report}) {
		return &report, nil
	}
	if !p.ValidCheckResults(Evidence{CheckResults: &report}) {
		return nil, ErrUnavailable
	}
	return &report, &CheckError{Report: &report}
}

func (e *Engine) recordCheckFailure(ctx context.Context, s Scope, p Plan, stage string, evidence Evidence) (Plan, error) {
	b, _ := json.Marshal(evidence)
	return readPlan(ctx, e.Pool, `SELECT gregale_repair.record_check_failure($1::uuid,$2::jsonb,$3,$4,$5::jsonb)`, p.ID, scopeJSON(s), p.Digest, stage, b)
}
