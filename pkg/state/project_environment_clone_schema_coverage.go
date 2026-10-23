package state

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
)

// A schema policy is an explicit inventory boundary, not evidence that rows
// have been captured or copied. Configuration and customer-data tables declare
// the isolated clone capabilities the code implements for them; a table stays
// a blocker until it has every capability its kind requires. Capabilities are
// registry facts only: they never enter the schema hash or an observed table.
type ProjectEnvironmentCloneSchemaPolicy struct {
	TableName    string    `json:"table"`
	Kind         string    `json:"kind"`
	Columns      []string  `json:"columns"`
	Capabilities *[]string `json:"capabilities,omitempty"`
}

const (
	CloneSchemaConfiguration = "configuration"
	CloneSchemaData          = "customer_data"
	CloneSchemaOperational   = "operational"
	CloneSchemaIdentity      = "account_identity"
	CloneSchemaPlatform      = "platform_configuration"
)

// Clone capabilities (ADR-590). A configuration table needs all six; retained
// customer data needs a checkpointed capture and an isolated target copy, and
// is never promoted back.
const (
	CloneCapabilityCapture     = "capture"
	CloneCapabilityMaterialize = "materialize"
	CloneCapabilityDiff        = "diff"
	CloneCapabilityQualify     = "qualify"
	CloneCapabilityPromote     = "promote"
	CloneCapabilityRollback    = "rollback"
)

var cloneSchemaRequiredCapabilities = map[string][]string{
	CloneSchemaConfiguration: {CloneCapabilityCapture, CloneCapabilityMaterialize, CloneCapabilityDiff, CloneCapabilityQualify, CloneCapabilityPromote, CloneCapabilityRollback},
	CloneSchemaData:          {CloneCapabilityCapture, CloneCapabilityMaterialize},
}

// MissingCloneCapabilities lists, in canonical order, the capabilities a
// registered table still lacks; it is empty for kinds that need none.
func MissingCloneCapabilities(policy ProjectEnvironmentCloneSchemaPolicy) []string {
	have := map[string]bool{}
	if policy.Capabilities != nil {
		for _, capability := range *policy.Capabilities {
			have[capability] = true
		}
	}
	var missing []string
	for _, capability := range cloneSchemaRequiredCapabilities[policy.Kind] {
		if !have[capability] {
			missing = append(missing, capability)
		}
	}
	return missing
}

// validCloneCapabilities requires every configuration and customer-data table
// to declare its capabilities explicitly, even as an empty list, so a new
// table is a deliberate entry in the clone gap rather than a silent default.
func validCloneCapabilities(policy ProjectEnvironmentCloneSchemaPolicy) bool {
	required, declares := cloneSchemaRequiredCapabilities[policy.Kind]
	if !declares {
		return policy.Capabilities == nil
	}
	if policy.Capabilities == nil {
		return false
	}
	seen := map[string]bool{}
	for _, capability := range *policy.Capabilities {
		if seen[capability] || !slices.Contains(required, capability) {
			return false
		}
		seen[capability] = true
	}
	return true
}

// CloneSchemaCapabilityProgress counts, per kind that needs capabilities, the
// registered tables and how many already have each capability and all of them.
type CloneSchemaCapabilityProgress struct {
	Kind         string
	Tables       int
	Complete     int
	Capabilities map[string]int
}

func ProjectEnvironmentCloneCapabilityProgress() ([]CloneSchemaCapabilityProgress, error) {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		return nil, err
	}
	var progress []CloneSchemaCapabilityProgress
	for _, kind := range []string{CloneSchemaConfiguration, CloneSchemaData} {
		p := CloneSchemaCapabilityProgress{Kind: kind, Capabilities: map[string]int{}}
		for _, policy := range policies {
			if policy.Kind != kind {
				continue
			}
			p.Tables++
			for _, capability := range *policy.Capabilities {
				p.Capabilities[capability]++
			}
			if len(MissingCloneCapabilities(policy)) == 0 {
				p.Complete++
			}
		}
		progress = append(progress, p)
	}
	return progress, nil
}

type ProjectEnvironmentCloneCoverageBlocker struct {
	Table, Column, Code string
}

type ProjectEnvironmentCloneSchemaCoverage struct {
	Hash     string
	Known    bool
	Tables   []ProjectEnvironmentCloneSchemaPolicy
	Blockers []ProjectEnvironmentCloneCoverageBlocker
}

type ProjectEnvironmentCloneSchemaCoverageStore interface {
	ProjectEnvironmentCloneSchemaCoverage(context.Context, string, string) (ProjectEnvironmentCloneSchemaCoverage, error)
}

//go:embed project_environment_clone_schema_registry.json
var projectCloneSchemaRegistryJSON []byte

// Return a fresh registry so callers cannot mutate the policies used by capture.
func ProjectEnvironmentCloneSchemaPolicies() ([]ProjectEnvironmentCloneSchemaPolicy, error) {
	var policies []ProjectEnvironmentCloneSchemaPolicy
	if err := json.Unmarshal(projectCloneSchemaRegistryJSON, &policies); err != nil {
		return nil, fmt.Errorf("decode clone schema registry: %w", err)
	}
	seen := map[string]bool{}
	for _, policy := range policies {
		if policy.TableName == "" || seen[policy.TableName] || len(policy.Columns) == 0 ||
			(policy.Kind != CloneSchemaConfiguration && policy.Kind != CloneSchemaData && policy.Kind != CloneSchemaOperational && policy.Kind != CloneSchemaIdentity && policy.Kind != CloneSchemaPlatform) ||
			!validCloneCapabilities(policy) {
			return nil, ErrConflict
		}
		seen[policy.TableName] = true
		fields := map[string]bool{}
		for _, column := range policy.Columns {
			if column == "" || fields[column] {
				return nil, ErrConflict
			}
			fields[column] = true
		}
	}
	return policies, nil
}

// Unknown/missing schema must fail before reserving an operation. Known
// configuration blockers remain explicit; the internal typed-capture path
// alone does not enable full admission or establish complete row coverage.
type ProjectEnvironmentCloneSchemaCoverageError struct {
	Blockers []ProjectEnvironmentCloneCoverageBlocker
}

func (e *ProjectEnvironmentCloneSchemaCoverageError) Error() string {
	if len(e.Blockers) == 0 {
		return "clone schema coverage unavailable"
	}
	b := e.Blockers[0]
	field := b.Table
	if b.Column != "" {
		field += "." + b.Column
	}
	return fmt.Sprintf("clone schema coverage unavailable: %s (%s)", field, b.Code)
}

func (e *ProjectEnvironmentCloneSchemaCoverageError) Unwrap() error { return ErrConflict }

func cloneSchemaCoverage(tables []ProjectEnvironmentCloneSchemaPolicy) (ProjectEnvironmentCloneSchemaCoverage, error) {
	policies, err := ProjectEnvironmentCloneSchemaPolicies()
	if err != nil {
		return ProjectEnvironmentCloneSchemaCoverage{}, err
	}
	expected := make(map[string]ProjectEnvironmentCloneSchemaPolicy, len(policies))
	for _, policy := range policies {
		expected[policy.TableName] = policy
	}
	report := ProjectEnvironmentCloneSchemaCoverage{Known: true, Tables: make([]ProjectEnvironmentCloneSchemaPolicy, 0, len(tables)), Blockers: []ProjectEnvironmentCloneCoverageBlocker{}}
	seen := map[string]bool{}
	for _, table := range tables {
		if table.TableName == "" || seen[table.TableName] || len(table.Columns) == 0 {
			return report, ErrConflict
		}
		seen[table.TableName] = true
		table.Columns = append([]string(nil), table.Columns...)
		sort.Strings(table.Columns)
		for i, column := range table.Columns {
			if column == "" || (i > 0 && table.Columns[i-1] == column) {
				return report, ErrConflict
			}
		}
		policy, registered := expected[table.TableName]
		table.Kind = policy.Kind // only the registry can classify an observed table
		table.Capabilities = nil // registry facts, outside the schema hash
		report.Tables = append(report.Tables, table)
		if !registered {
			report.Known = false
			report.Blockers = append(report.Blockers, ProjectEnvironmentCloneCoverageBlocker{Table: table.TableName, Code: "unregistered_table"})
			continue
		}
		wanted, observed := map[string]bool{}, map[string]bool{}
		for _, column := range policy.Columns {
			wanted[column] = true
		}
		for _, column := range table.Columns {
			observed[column] = true
			if !wanted[column] {
				report.Known = false
				report.Blockers = append(report.Blockers, ProjectEnvironmentCloneCoverageBlocker{Table: table.TableName, Column: column, Code: "unregistered_column"})
			}
		}
		for _, column := range policy.Columns {
			if !observed[column] {
				report.Known = false
				report.Blockers = append(report.Blockers, ProjectEnvironmentCloneCoverageBlocker{Table: table.TableName, Column: column, Code: "missing_column"})
			}
		}
		if len(MissingCloneCapabilities(policy)) > 0 {
			code := "isolated_strategy_unavailable"
			if policy.Kind == CloneSchemaData {
				code = "isolated_data_strategy_unavailable"
			}
			report.Blockers = append(report.Blockers, ProjectEnvironmentCloneCoverageBlocker{Table: table.TableName, Code: code})
		}
	}
	for _, policy := range policies {
		if !seen[policy.TableName] {
			report.Known = false
			report.Blockers = append(report.Blockers, ProjectEnvironmentCloneCoverageBlocker{Table: policy.TableName, Code: "missing_table"})
		}
	}
	sort.Slice(report.Tables, func(i, j int) bool { return report.Tables[i].TableName < report.Tables[j].TableName })
	sort.Slice(report.Blockers, func(i, j int) bool {
		a, b := report.Blockers[i], report.Blockers[j]
		if a.Table != b.Table {
			return a.Table < b.Table
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Code < b.Code
	})
	raw, err := json.Marshal(report.Tables)
	if err != nil {
		return report, err
	}
	hash := sha256.Sum256(raw)
	report.Hash = hex.EncodeToString(hash[:])
	return report, nil
}

func requireKnownCloneSchema(report ProjectEnvironmentCloneSchemaCoverage) error {
	if report.Known {
		return nil
	}
	var blockers []ProjectEnvironmentCloneCoverageBlocker
	for _, blocker := range report.Blockers {
		if blocker.Code != "isolated_strategy_unavailable" && blocker.Code != "isolated_data_strategy_unavailable" {
			blockers = append(blockers, blocker)
		}
	}
	return &ProjectEnvironmentCloneSchemaCoverageError{Blockers: blockers}
}
