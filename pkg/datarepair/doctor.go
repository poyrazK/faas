package datarepair

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
)

// DoctorReport describes observed executor configuration, never authority to
// capture, rehearse, approve or apply. It contains no row values or check SQL.
type DoctorReport struct {
	Version   int             `json:"version"`
	Profile   string          `json:"profile,omitempty"`
	Scope     Scope           `json:"scope"`
	Table     string          `json:"table"`
	CheckedAt time.Time       `json:"checked_at"`
	Status    string          `json:"status"`
	Findings  []DoctorFinding `json:"findings"`
	Policy    DoctorPolicy    `json:"policy"`
}

type DoctorFinding struct {
	ID      string   `json:"id"`
	Status  string   `json:"status"`
	Code    string   `json:"code"`
	Summary string   `json:"summary"`
	Action  string   `json:"action"`
	Objects []string `json:"objects"`
}

type DoctorPolicy struct {
	Mode   string `json:"mode"`
	Digest string `json:"digest,omitempty"`
	Count  int    `json:"count"`
}

type doctorMessage struct{ id, status, summary, action string }

// Fixed messages keep database errors, SQL and credentials out of all readers.
func doctorMessages() map[string]doctorMessage {
	return map[string]doctorMessage{
		"connected":              {"connection", "ready", "The executor reached a read-only database snapshot.", "Run doctor again after changing database configuration."},
		"connection_unavailable": {"connection", "unknown", "The database snapshot could not be inspected.", "Check the executor database connection and catalog access, then run doctor again."},
		"postgres16":             {"postgres", "ready", "PostgreSQL 16 is installed.", "Keep the source on the qualified PostgreSQL major version."},
		"postgres_unsupported":   {"postgres", "blocked", "This PostgreSQL major version is unsupported.", "Use PostgreSQL 16 for selective repair."},
		"not_inspected":          {"", "unknown", "Prerequisites prevented this inspection.", "Resolve the preceding blocked or unknown findings, then run doctor again."},
		"schema_current":         {"installation", "ready", "Required repair tables and columns are present.", "Apply future schema upgrades explicitly as the database owner."},
		"schema_missing":         {"installation", "blocked", "The repair schema is not installed.", "Review gregale data-repair schema and have the database owner install it, then enroll declared fields."},
		"schema_incompatible":    {"installation", "blocked", "Required repair tables or columns are missing or incompatible.", "Have the database owner review gregale data-repair schema --upgrade; investigate incompatible objects before applying it."},
		"functions_current":      {"functions", "ready", "Repair function definitions match this executor version.", "Keep the executor and owner-installed repair schema in sync."},
		"functions_incompatible": {"functions", "blocked", "Repair functions are missing, changed or overloaded.", "Have the database owner review gregale data-repair schema --upgrade and restore the matching function definitions; base capture functions require review against gregale data-repair schema."},
		"enrolled":               {"enrollment", "ready", "The named table has a valid field enrollment.", "Use the enrolled fields in opted-in capture commands."},
		"not_enrolled":           {"enrollment", "blocked", "No enrollment matches the exact qualified table name.", "Have the database owner enroll the intended table and fields with gregale data-repair enroll."},
		"enrollment_invalid":     {"enrollment", "blocked", "The enrollment no longer identifies a supported table and fields.", "Investigate table replacement, renamed or removed fields, row security and registry changes with the database owner; do not rewrite stored fingerprints."},
		"fingerprint_matches":    {"fingerprint", "ready", "The table catalog fingerprint matches its enrollment.", "Review any later schema change before preparing a repair."},
		"fingerprint_changed":    {"fingerprint", "blocked", "The table schema differs from its enrollment fingerprint.", "Have the database owner investigate schema drift and restore the reviewed schema; do not refresh the fingerprint to bypass this finding."},
		"triggers_enabled":       {"triggers", "ready", "Capture and row-version triggers have the expected definitions and are enabled.", "Keep both triggers enabled for ordinary application writes."},
		"triggers_invalid":       {"triggers", "blocked", "Capture or row-version triggers are missing, disabled or changed, or replication mode bypasses them.", "Have the database owner restore the enrolled trigger definitions and origin replication mode, then assess any writes made while capture was bypassed."},
		"permissions_present":    {"permissions", "ready", "The connected executor role has the required runtime privileges.", "Check SDK writer roles separately; this report covers the connected executor role only."},
		"permissions_missing":    {"permissions", "blocked", "The connected executor role lacks required runtime privileges.", "Have the database owner grant only the listed schema, table, column or function privileges to the executor role; maintenance privileges are not required."},
		"policy_configured":      {"policy", "ready", "A check policy is configured and hashed; no check was executed.", "Review the declared checks and run a separate rehearsal before approval."},
		"policy_default":         {"policy", "blocked", "Only the default SELECT true check is configured.", "Configure a trusted checks file or named checks policy with business invariants before relying on repairs."},
		"policy_invalid":         {"policy", "blocked", "The executor check policy fails static configuration validation.", "Correct the operator-configured checks file or named checks policy, then restart the executor and run doctor again."},
		"binding_configured":     {"binding", "ready", "One current operator-configured source binding matches the requested scope.", "Run doctor again after changing the binding generation or executor configuration."},
		"executor_unavailable":   {"connection", "unknown", "The configured executor did not return a valid readiness report.", "Check executor availability, credentials and doctor support for this exact configured binding."},
	}
}

func (r *DoctorReport) finding(id, code string, objects ...string) {
	m := doctorMessages()[code]
	f := DoctorFinding{ID: id, Status: m.status, Code: code, Summary: m.summary, Action: m.action, Objects: objects}
	if f.Objects == nil {
		f.Objects = []string{}
	}
	for i, old := range r.Findings {
		if old.ID == id {
			r.Findings[i] = f
			return
		}
	}
	r.Findings = append(r.Findings, f)
}

func (r *DoctorReport) aggregate() {
	r.Status = "ready"
	for _, f := range r.Findings {
		if f.Status == "blocked" {
			r.Status = "blocked"
			return
		}
		if f.Status == "unknown" {
			r.Status = "unknown"
		}
	}
}

// DoctorTable validates an exact registry name, without resolving SQL names or
// accepting a search path. Quoted qualified names are supported as stored.
func DoctorTable(table string) bool {
	return table != "" && len(table) <= api.DataRepairSourceMaxFilterBytes && strings.TrimSpace(table) == table &&
		strings.Contains(table, ".") && !strings.ContainsRune(table, 0) && utf8.ValidString(table)
}

// UnavailableDoctor preserves unknown observations when an older or unavailable
// executor cannot report readiness. A transport failure can never become ready.
func UnavailableDoctor(s Scope, table string) DoctorReport {
	r := DoctorReport{Version: 1, Profile: "executor", Scope: s, Table: table, CheckedAt: time.Now().UTC(), Policy: DoctorPolicy{Mode: "unknown"}}
	for _, id := range []string{"connection", "postgres", "installation", "functions", "enrollment", "fingerprint", "triggers", "permissions", "policy"} {
		r.finding(id, "not_inspected")
	}
	r.finding("connection", "executor_unavailable")
	r.aggregate()
	return r
}

// BoundDoctor records only an already authenticated, exact current hosted
// binding. It adds no plan evidence, operations or write permissions.
func (r *DoctorReport) BoundDoctor() {
	r.finding("binding", "binding_configured")
	r.aggregate()
}

func (r DoctorReport) Valid() bool {
	if r.Version != 1 || r.Profile != "" && r.Profile != "executor" || r.Scope.Validate() != nil || !DoctorTable(r.Table) || r.CheckedAt.IsZero() || len(r.Findings) < 9 || len(r.Findings) > 10 {
		return false
	}
	seen := map[string]bool{}
	policyCode := ""
	for _, f := range r.Findings {
		m, ok := doctorMessages()[f.Code]
		if !ok || m.id != "" && m.id != f.ID || !slices.Contains([]string{"connection", "postgres", "installation", "functions", "enrollment", "fingerprint", "triggers", "permissions", "policy", "binding"}, f.ID) ||
			seen[f.ID] || m.status != f.Status || m.summary != f.Summary || m.action != f.Action || f.Objects == nil || len(f.Objects) > api.DataRepairDoctorMaxObjects {
			return false
		}
		seen[f.ID] = true
		if f.ID == "policy" {
			policyCode = f.Code
		}
		for _, obj := range f.Objects {
			if obj == "" || len(obj) > api.DataRepairSourceMaxFilterBytes || !utf8.ValidString(obj) || strings.ContainsRune(obj, 0) {
				return false
			}
		}
	}
	for _, id := range []string{"connection", "postgres", "installation", "functions", "enrollment", "fingerprint", "triggers", "permissions", "policy"} {
		if !seen[id] {
			return false
		}
	}
	if !slices.Contains([]string{"unknown", "invalid", "default", "legacy", "named"}, r.Policy.Mode) || r.Policy.Count < 0 || r.Policy.Count > api.DataRepairMaxChecks ||
		(r.Policy.Mode == "unknown" || r.Policy.Mode == "invalid") != (r.Policy.Digest == "") || r.Policy.Digest != "" && !digestPattern.MatchString(r.Policy.Digest) {
		return false
	}
	switch r.Policy.Mode {
	case "unknown":
		if policyCode != "not_inspected" || r.Policy.Count != 0 {
			return false
		}
	case "invalid":
		if policyCode != "policy_invalid" || r.Policy.Count != 0 {
			return false
		}
	case "default":
		if policyCode != "policy_default" || r.Policy.Count != 1 {
			return false
		}
	case "legacy":
		if policyCode != "policy_configured" || r.Policy.Count != 1 {
			return false
		}
	case "named":
		if policyCode != "policy_configured" || r.Policy.Count < 1 {
			return false
		}
	}
	copy := r
	copy.aggregate()
	return copy.Status == r.Status
}

// Doctor reads catalogs and one owner-maintained enrollment in a bounded,
// read-only snapshot. It never invokes installed repair/check functions,
// resolves arbitrary SQL identifiers, scans business rows or installs upgrades.
func (e *Engine) Doctor(ctx context.Context, s Scope, table string) (DoctorReport, error) {
	if s.Validate() != nil || !DoctorTable(table) {
		return DoctorReport{}, ErrInvalid
	}
	r := UnavailableDoctor(s, table)
	r.finding("connection", "connection_unavailable")
	e.doctorPolicy(&r)
	if e == nil || e.Pool == nil {
		r.aggregate()
		return r, nil
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
	defer cancel()
	tx, err := e.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		r.aggregate()
		return r, nil
	}
	defer func() {
		clean, stop := context.WithTimeout(context.WithoutCancel(ctx), time.Duration(api.DataRepairDoctorTimeoutSeconds)*time.Second)
		defer stop()
		_ = tx.Rollback(clean)
	}()
	if _, err = tx.Exec(ctx, `SELECT set_config('search_path','pg_catalog',true),set_config('statement_timeout',$1,true),set_config('lock_timeout',$1,true)`, strconv.Itoa(api.DataRepairDoctorTimeoutSeconds*1000)); err == nil {
		err = doctorInspect(ctx, tx, &r)
	}
	if err != nil {
		r.finding("connection", "connection_unavailable")
	}
	r.aggregate()
	return r, nil
}

func (e *Engine) doctorPolicy(r *DoctorReport) {
	if e == nil {
		return
	}
	set, err := configuredChecks(e.CheckSQL, e.Checks)
	if err != nil {
		r.Policy = DoctorPolicy{Mode: "invalid"}
		r.finding("policy", "policy_invalid")
		return
	}
	r.Policy = DoctorPolicy{Mode: "legacy", Digest: set.digest, Count: 1}
	code := "policy_configured"
	if e.Checks != nil {
		r.Policy.Mode, r.Policy.Count = "named", len(e.Checks)
	} else if strings.EqualFold(strings.TrimSpace(set.query), "SELECT true") || strings.EqualFold(strings.TrimSpace(set.query), "SELECT true;") {
		r.Policy.Mode, code = "default", "policy_default"
	}
	r.finding("policy", code)
}

func doctorJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
