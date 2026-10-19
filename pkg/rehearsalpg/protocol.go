// Package rehearsalpg defines independent PostgreSQL rehearsal evidence (ADR-20261017201009).
package rehearsalpg

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"regexp"
	"sort"
)

// Bounds describe the first evidence format, not account entitlements.
const (
	Version          = 1
	ProxyPort        = 10083
	MaxFixtureBytes  = 64 << 10
	MaxTables        = 16
	MaxColumns       = 32
	MaxRows          = 1000
	MaxEvidenceBytes = 1 << 20
	Provenance       = "platform_postgres_collector"
)

var identifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Table struct {
	Schema  string   `json:"schema" yaml:"schema"`
	Name    string   `json:"name" yaml:"name"`
	Columns []string `json:"columns" yaml:"columns"`
}

type Spec struct {
	Version       int     `json:"version" yaml:"version"`
	FixtureSHA256 string  `json:"fixture_sha256" yaml:"-"`
	Tables        []Table `json:"tables" yaml:"tables"`
	MaxLockWaitMS int64   `json:"max_lock_wait_ms" yaml:"max_lock_wait_ms"`
}

type CreateRequest struct {
	AppSlug string `json:"app_slug"`
	Spec    Spec   `json:"spec"`
	SQL     string `json:"sql"`
}

type SealRequest struct {
	DeploymentID string `json:"deployment_id"`
}

type Receipt struct {
	Version        int    `json:"version"`
	RunID          string `json:"run_id"`
	AppID          string `json:"app_id"`
	ResourceID     string `json:"resource_id"`
	FixtureSHA256  string `json:"fixture_sha256"`
	ContractSHA256 string `json:"contract_sha256"`
	InitialSHA256  string `json:"initial_sha256"`
}

type Snapshot struct {
	Table string            `json:"table"`
	Rows  []json.RawMessage `json:"rows"`
}

type Delta struct {
	Table    string            `json:"table"`
	Inserted []json.RawMessage `json:"inserted"`
	Deleted  []json.RawMessage `json:"deleted"`
}

type Evidence struct {
	Receipt
	Status                string     `json:"status"`
	Provenance            string     `json:"provenance"`
	DeploymentID          string     `json:"deployment_id"`
	Spec                  Spec       `json:"spec"`
	Before                []Snapshot `json:"before"`
	After                 []Snapshot `json:"after"`
	Changes               []Delta    `json:"changes"`
	LockSamples           int64      `json:"lock_samples"`
	MaxObservedLockWaitMS int64      `json:"max_observed_lock_wait_ms"`
	LockBudgetPassed      bool       `json:"lock_budget_passed"`
	CleanupConfirmed      bool       `json:"cleanup_confirmed"`
	Reason                string     `json:"reason,omitempty"`
}

type Comparison struct {
	Status string  `json:"status"`
	Reason string  `json:"reason,omitempty"`
	Before []Delta `json:"before,omitempty"`
	After  []Delta `json:"after,omitempty"`
}

func Hash(v any) string         { b, _ := json.Marshal(v); return HashBytes(b) }
func HashBytes(b []byte) string { h := sha256.Sum256(b); return hex.EncodeToString(h[:]) }

func (s Spec) Validate() error {
	if s.Version != Version || !digest.MatchString(s.FixtureSHA256) || len(s.Tables) < 1 || len(s.Tables) > MaxTables || s.MaxLockWaitMS < 1 || s.MaxLockWaitMS > 10000 {
		return errors.New("PostgreSQL requires version 1, a fixture digest, 1–16 tables and a 1–10000ms lock budget")
	}
	seen := map[string]bool{}
	for _, t := range s.Tables {
		key := t.Schema + "." + t.Name
		if t.Schema != "public" || !identifier.MatchString(t.Name) || seen[key] || len(t.Columns) < 1 || len(t.Columns) > MaxColumns {
			return errors.New("PostgreSQL tables require unique public table names and 1–32 explicit columns")
		}
		seen[key] = true
		cols := map[string]bool{}
		for _, c := range t.Columns {
			if !identifier.MatchString(c) || cols[c] {
				return errors.New("PostgreSQL columns must be unique plain identifiers")
			}
			cols[c] = true
		}
	}
	return nil
}

func (s Spec) ContractSHA256() string {
	copy := s
	copy.FixtureSHA256 = ""
	copy.Tables = append([]Table(nil), s.Tables...)
	for i, t := range copy.Tables {
		copy.Tables[i].Columns = append([]string(nil), t.Columns...)
		sort.Strings(copy.Tables[i].Columns)
	}
	sort.Slice(copy.Tables, func(i, j int) bool { return copy.Tables[i].Name < copy.Tables[j].Name })
	return Hash(copy)
}

// ErrSnapshotBound reports a captured inventory larger than its evidence bound.
var ErrSnapshotBound = errors.New("PostgreSQL snapshot exceeds evidence bound")

// CanonicalSnapshots preserves duplicate rows; a repeated insert must remain visible.
func CanonicalSnapshots(v []Snapshot) ([]Snapshot, error) {
	if len(v) < 1 || len(v) > MaxTables {
		return nil, errors.New("invalid PostgreSQL snapshot inventory")
	}
	out := make([]Snapshot, 0, len(v))
	seen := map[string]bool{}
	total := 0
	for _, t := range v {
		if seen[t.Table] || len(t.Rows) > MaxRows {
			return nil, errors.New("invalid PostgreSQL snapshot rows")
		}
		seen[t.Table] = true
		rows := make([]json.RawMessage, 0, len(t.Rows))
		for _, r := range t.Rows {
			var row map[string]json.RawMessage
			if json.Unmarshal(r, &row) != nil || row == nil {
				return nil, errors.New("invalid PostgreSQL row")
			}
			b, _ := json.Marshal(row)
			total += len(b)
			if total > MaxEvidenceBytes/4 {
				return nil, ErrSnapshotBound
			}
			rows = append(rows, b)
		}
		sort.Slice(rows, func(i, j int) bool { return string(rows[i]) < string(rows[j]) })
		out = append(out, Snapshot{t.Table, rows})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out, nil
}

func Changes(before, after []Snapshot) []Delta {
	var out []Delta
	old := map[string][]json.RawMessage{}
	current := map[string][]json.RawMessage{}
	for _, t := range before {
		old[t.Table] = t.Rows
	}
	for _, t := range after {
		current[t.Table] = t.Rows
	}
	for table, rows := range old {
		counts := map[string]int{}
		for _, r := range rows {
			counts[string(r)]--
		}
		for _, r := range current[table] {
			counts[string(r)]++
		}
		d := Delta{Table: table, Inserted: []json.RawMessage{}, Deleted: []json.RawMessage{}}
		for r, n := range counts {
			for ; n > 0; n-- {
				d.Inserted = append(d.Inserted, json.RawMessage(r))
			}
			for ; n < 0; n++ {
				d.Deleted = append(d.Deleted, json.RawMessage(r))
			}
		}
		sort.Slice(d.Inserted, func(i, j int) bool { return string(d.Inserted[i]) < string(d.Inserted[j]) })
		sort.Slice(d.Deleted, func(i, j int) bool { return string(d.Deleted[i]) < string(d.Deleted[j]) })
		out = append(out, d)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Table < out[j].Table })
	return out
}

func (e Evidence) Validate() error {
	if e.Version != Version || e.Provenance != Provenance || e.Status != "complete" || e.Reason != "" || !e.CleanupConfirmed || e.LockSamples < 1 || e.MaxObservedLockWaitMS < 0 || e.Spec.Validate() != nil || e.ContractSHA256 != e.Spec.ContractSHA256() || e.FixtureSHA256 != e.Spec.FixtureSHA256 || !digest.MatchString(e.InitialSHA256) || e.RunID == "" || e.AppID == "" || e.ResourceID == "" || e.DeploymentID == "" {
		return errors.New("incomplete PostgreSQL evidence")
	}
	b, err := CanonicalSnapshots(e.Before)
	if err != nil {
		return err
	}
	a, err := CanonicalSnapshots(e.After)
	if err != nil {
		return err
	}
	if len(b) != len(e.Spec.Tables) || len(a) != len(b) || Hash(b) != e.InitialSHA256 || Hash(Changes(b, a)) != Hash(e.Changes) || e.LockBudgetPassed != (e.MaxObservedLockWaitMS <= e.Spec.MaxLockWaitMS) {
		return errors.New("PostgreSQL evidence does not match its contract")
	}
	for _, snapshots := range [][]Snapshot{b, a} {
		for _, t := range e.Spec.Tables {
			found := false
			for _, snapshot := range snapshots {
				if snapshot.Table != t.Schema+"."+t.Name {
					continue
				}
				found = true
				for _, r := range snapshot.Rows {
					var row map[string]json.RawMessage
					_ = json.Unmarshal(r, &row)
					if len(row) != len(t.Columns) {
						return errors.New("PostgreSQL row columns changed")
					}
					for _, c := range t.Columns {
						if _, ok := row[c]; !ok {
							return errors.New("PostgreSQL row column missing")
						}
					}
				}
			}
			if !found {
				return errors.New("PostgreSQL table missing")
			}
		}
	}
	body, _ := json.Marshal(e)
	if len(body) > MaxEvidenceBytes {
		return errors.New("PostgreSQL evidence exceeds bound")
	}
	return nil
}

func Compare(before, after *Evidence) *Comparison {
	if before == nil && after == nil {
		return nil
	}
	r := &Comparison{Status: "inconclusive"}
	if before == nil || after == nil || before.Validate() != nil || after.Validate() != nil {
		r.Reason = "independent PostgreSQL evidence is incomplete"
		return r
	}
	if before.ContractSHA256 != after.ContractSHA256 || before.FixtureSHA256 != after.FixtureSHA256 || before.InitialSHA256 != after.InitialSHA256 {
		r.Reason = "PostgreSQL collector contract, fixture or starting data changed"
		return r
	}
	r.Status = "passed"
	r.Before = before.Changes
	r.After = after.Changes
	if Hash(before.Changes) != Hash(after.Changes) || !before.LockBudgetPassed || !after.LockBudgetPassed {
		r.Status = "failed"
	}
	return r
}
