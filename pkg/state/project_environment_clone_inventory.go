package state

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
)

// CloneInventoryPolicy says how a configuration or customer-data table's rows
// belong to a clone's source, so admission can name exactly the tables the
// source uses (ADR-20261024160000). A clone admits a source only when every
// table it holds rows in has an isolated strategy; tables it does not use
// cannot be omitted from a copy, so they never block.
//
// Owner is environment (Column holds the environment ID, or SlugColumn the
// environment slug beside project_id), project, app (the captured workloads),
// deployment (the captured source deployments), or account, org or platform
// for configuration outside the environment boundary. Via lists foreign-key
// hops [column, parent table, parent column] from the table to the one that
// carries the owner column.
//
// Strategy marks a table the clone deliberately does not copy: shared
// configuration every environment uses alike; derived rows the clone writes
// for the target from what it captured elsewhere (the target environment row
// and its workload heads); or target_identity, a physical identity the stage
// must not inherit (it gets its own, such as a generated stage URL instead of a
// custom domain). Unavailable names the blocker for a table whose rows have a
// strategy the clone cannot run yet.
type CloneInventoryPolicy struct {
	Owner       string      `json:"owner"`
	Column      string      `json:"column,omitempty"`
	SlugColumn  string      `json:"slug_column,omitempty"`
	Via         [][3]string `json:"via,omitempty"`
	Strategy    string      `json:"strategy,omitempty"`
	Unavailable string      `json:"unavailable,omitempty"`
}

const (
	CloneInventoryEnvironment = "environment"
	CloneInventoryProject     = "project"
	CloneInventoryApp         = "app"
	CloneInventoryDeployment  = "deployment"
	CloneInventoryAccount     = "account"
	CloneInventoryOrg         = "org"
	CloneInventoryPlatform    = "platform"

	CloneInventoryShared         = "shared"
	CloneInventoryDerived        = "derived"
	CloneInventoryTargetIdentity = "target_identity"
)

// Blocker codes for tables a source uses but the clone cannot copy.
const (
	CloneBlockerStrategyUnavailable       = "clone_strategy_unavailable"
	CloneBlockerDataStrategyUnavailable   = "isolated_data_strategy_unavailable"
	CloneBlockerDataCheckpointUnavailable = "data_checkpoint_unavailable"
	CloneBlockerWorkActivationUnavailable = "work_activation_unavailable"
	// A bound managed database without a restore window cannot be copied at
	// the clone's point in time (ADR-20261024224230).
	CloneBlockerPointInTimeRestoreUnavailable = "point_in_time_restore_unavailable"
)

var cloneInventoryIdentifier = regexp.MustCompile(`^[a-z_][a-z0-9_]{0,62}$`)

// Clone admission needs a frozen source and an isolated target copy; diff,
// qualification, promotion and rollback belong to full promotion.
var cloneAdmissionCapabilities = []string{CloneCapabilityCapture, CloneCapabilityMaterialize}

func validCloneInventory(policy ProjectEnvironmentCloneSchemaPolicy) bool {
	if _, declares := cloneSchemaRequiredCapabilities[policy.Kind]; !declares {
		return policy.Clone == nil
	}
	c := policy.Clone
	if c == nil || len(c.Via) > 3 {
		return false
	}
	for _, hop := range c.Via {
		for _, name := range hop {
			if !cloneInventoryIdentifier.MatchString(name) {
				return false
			}
		}
	}
	for _, name := range []string{c.Column, c.SlugColumn} {
		if name != "" && !cloneInventoryIdentifier.MatchString(name) {
			return false
		}
	}
	switch c.Strategy {
	case "":
	case CloneInventoryShared, CloneInventoryDerived, CloneInventoryTargetIdentity:
		if c.Unavailable != "" {
			return false
		}
	default:
		return false
	}
	switch c.Unavailable {
	case "", CloneBlockerDataCheckpointUnavailable, CloneBlockerWorkActivationUnavailable:
	default:
		return false
	}
	switch c.Owner {
	case CloneInventoryEnvironment:
		return (c.Column == "") != (c.SlugColumn == "")
	case CloneInventoryProject, CloneInventoryApp, CloneInventoryDeployment:
		return c.SlugColumn == ""
	case CloneInventoryAccount, CloneInventoryOrg, CloneInventoryPlatform:
		// Configuration outside the environment boundary is shared by every
		// environment; the clone neither copies nor isolates it.
		return c.Strategy == CloneInventoryShared && c.Column == "" && c.SlugColumn == "" && len(c.Via) == 0
	}
	return false
}

// cloneInventoryProbe is one table the source inventory must test for rows.
type cloneInventoryProbe struct {
	Table      string      `json:"table"`
	Owner      string      `json:"owner"`
	Column     string      `json:"column,omitempty"`
	SlugColumn string      `json:"slug_column,omitempty"`
	Via        [][3]string `json:"via,omitempty"`
}

// cloneInventoryProbes lists, in table order, every table whose rows would
// matter to a clone: tables the clone carries or cannot carry, but not tables
// it deliberately leaves behind.
func cloneInventoryProbes(policies []ProjectEnvironmentCloneSchemaPolicy) ([]byte, error) {
	probes := []cloneInventoryProbe{}
	for _, policy := range policies {
		if policy.Clone == nil || policy.Clone.Strategy != "" {
			continue
		}
		probes = append(probes, cloneInventoryProbe{Table: policy.TableName, Owner: policy.Clone.Owner,
			Column: policy.Clone.Column, SlugColumn: policy.Clone.SlugColumn, Via: policy.Clone.Via})
	}
	sort.Slice(probes, func(i, j int) bool { return probes[i].Table < probes[j].Table })
	return json.Marshal(probes)
}

// cloneInventoryBlockers names every table the source holds rows in that the
// clone cannot copy into an isolated target, in table order.
func cloneInventoryBlockers(policies []ProjectEnvironmentCloneSchemaPolicy, present map[string]bool) []ProjectEnvironmentCloneCoverageBlocker {
	blockers := []ProjectEnvironmentCloneCoverageBlocker{}
	for _, policy := range policies {
		if !present[policy.TableName] || policy.Clone == nil || policy.Clone.Strategy != "" {
			continue
		}
		code := ""
		switch {
		case policy.Clone.Unavailable != "":
			code = policy.Clone.Unavailable
		case !hasCloneCapabilities(policy, cloneAdmissionCapabilities):
			code = CloneBlockerStrategyUnavailable
			if policy.Kind == CloneSchemaData {
				code = CloneBlockerDataStrategyUnavailable
			}
		}
		if code != "" {
			blockers = append(blockers, ProjectEnvironmentCloneCoverageBlocker{Table: policy.TableName, Code: code})
		}
	}
	sort.Slice(blockers, func(i, j int) bool { return blockers[i].Table < blockers[j].Table })
	return blockers
}

func hasCloneCapabilities(policy ProjectEnvironmentCloneSchemaPolicy, required []string) bool {
	have := map[string]bool{}
	if policy.Capabilities != nil {
		for _, capability := range *policy.Capabilities {
			have[capability] = true
		}
	}
	for _, capability := range required {
		if !have[capability] {
			return false
		}
	}
	return true
}

// ProjectEnvironmentCloneInventoryError names the tables a clone's source uses
// that have no isolated strategy yet; no operation is created.
type ProjectEnvironmentCloneInventoryError struct {
	Blockers []ProjectEnvironmentCloneCoverageBlocker
}

func (e *ProjectEnvironmentCloneInventoryError) Error() string {
	if len(e.Blockers) == 0 {
		return "clone inventory unavailable"
	}
	return fmt.Sprintf("clone source uses %d tables without an isolated strategy, first %s (%s)", len(e.Blockers), e.Blockers[0].Table, e.Blockers[0].Code)
}

func (e *ProjectEnvironmentCloneInventoryError) Unwrap() error { return ErrConflict }
