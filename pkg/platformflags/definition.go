package platformflags

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/flags"
)

// Definition is the operator-owned value of one platform flag, stored as the
// JSON value of its runtime configuration row (ADR-1700).
//
// Enabled is the kill switch: false turns the feature off for every account
// regardless of rules or Default. When Enabled is true, rules run in order and
// the first match decides; an account no rule matches gets Default.
type Definition struct {
	Enabled bool   `json:"enabled"`
	Default bool   `json:"default"`
	Rules   []Rule `json:"rules,omitempty"`
}

// Rule targets accounts. Accounts, Orgs, Group, Plans and Rollout combine with
// AND; a rule must set at least one of them. Orgs matches the organization a
// request acts under (ADR 20261021080640), never an account's other
// memberships. Rollout is in basis points of the accounts the other
// constraints admit (1000 = 10%, 10000 = all) and uses the same stable hash as
// customer Flags, so raising it keeps the earlier cohort. Value is required so
// a rule can never enable a feature by omission.
type Rule struct {
	ID       string   `json:"id"`
	Accounts []string `json:"accounts,omitempty"`
	Orgs     []string `json:"orgs,omitempty"`
	Group    string   `json:"group,omitempty"`
	Plans    []string `json:"plans,omitempty"`
	Rollout  *int     `json:"rollout,omitempty"`
	Value    *bool    `json:"value"`
}

// Groups are the shared named account sets rules reference by name. They are
// stored once under GroupsConfigKey so "internal" or "design-partners" is
// maintained in one place for every flag.
type Groups map[string][]string

// ParseDefinition decodes and validates a stored or submitted definition.
// Unknown fields are rejected so a misspelled "acounts" cannot silently
// produce a rule that matches nobody.
func ParseDefinition(raw json.RawMessage) (Definition, error) {
	if len(raw) > api.PlatformFlagsMaxDefinitionBytes {
		return Definition{}, fmt.Errorf("flag definition exceeds %d bytes", api.PlatformFlagsMaxDefinitionBytes)
	}
	var def Definition
	if err := decodeStrict(raw, &def); err != nil {
		return Definition{}, fmt.Errorf("flag definition must be an object with enabled, default and rules: %w", err)
	}
	def = NormalizeDefinition(def)
	if err := ValidateDefinition(def, nil); err != nil {
		return Definition{}, err
	}
	return def, nil
}

// ParseGroups decodes and validates the shared group map.
func ParseGroups(raw json.RawMessage) (Groups, error) {
	if len(raw) > api.PlatformFlagsMaxGroupsBytes {
		return nil, fmt.Errorf("flag groups exceed %d bytes", api.PlatformFlagsMaxGroupsBytes)
	}
	groups := Groups{}
	if err := decodeStrict(raw, &groups); err != nil {
		return nil, fmt.Errorf("flag groups must be an object of group name to account IDs: %w", err)
	}
	groups = NormalizeGroups(groups)
	if err := ValidateGroups(groups); err != nil {
		return nil, err
	}
	return groups, nil
}

func decodeStrict(raw json.RawMessage, out any) error {
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after JSON value")
	}
	return nil
}

// NormalizeDefinition canonicalizes account IDs to lowercase UUID form and
// plans to lowercase so equality and hashing never depend on input casing.
func NormalizeDefinition(def Definition) Definition {
	out := Definition{Enabled: def.Enabled, Default: def.Default}
	if len(def.Rules) == 0 {
		return out
	}
	out.Rules = make([]Rule, 0, len(def.Rules))
	for _, rule := range def.Rules {
		next := Rule{ID: strings.TrimSpace(rule.ID), Group: strings.TrimSpace(rule.Group)}
		if rule.Rollout != nil {
			rollout := *rule.Rollout
			next.Rollout = &rollout
		}
		if rule.Value != nil {
			value := *rule.Value
			next.Value = &value
		}
		next.Accounts = normalizeAccounts(rule.Accounts)
		next.Orgs = normalizeAccounts(rule.Orgs)
		for _, plan := range rule.Plans {
			next.Plans = append(next.Plans, strings.ToLower(strings.TrimSpace(plan)))
		}
		out.Rules = append(out.Rules, next)
	}
	return out
}

// NormalizeGroups canonicalizes every member account ID.
func NormalizeGroups(groups Groups) Groups {
	out := make(Groups, len(groups))
	for name, accounts := range groups {
		out[strings.TrimSpace(name)] = normalizeAccounts(accounts)
	}
	return out
}

func normalizeAccounts(accounts []string) []string {
	if len(accounts) == 0 {
		return nil
	}
	out := make([]string, 0, len(accounts))
	for _, account := range accounts {
		account = strings.TrimSpace(account)
		if id, err := uuid.Parse(account); err == nil {
			account = id.String()
		}
		out = append(out, account)
	}
	return out
}

// ValidateDefinition bounds a definition. When groups is non-nil, every
// referenced group must exist in it; nil skips the cross-reference check for
// callers that only hold the flag value.
func ValidateDefinition(def Definition, groups Groups) error {
	if len(def.Rules) > api.PlatformFlagsMaxRules {
		return fmt.Errorf("a flag may have at most %d rules", api.PlatformFlagsMaxRules)
	}
	ids := make(map[string]bool, len(def.Rules))
	for _, rule := range def.Rules {
		if !flags.ValidKey(rule.ID) {
			return fmt.Errorf("rule id %q must match ^[a-z][a-z0-9_-]{0,63}$", rule.ID)
		}
		if ids[rule.ID] {
			return fmt.Errorf("duplicate rule id %q", rule.ID)
		}
		ids[rule.ID] = true
		if rule.Value == nil {
			return fmt.Errorf("rule %q requires an explicit boolean value", rule.ID)
		}
		if len(rule.Accounts) == 0 && len(rule.Orgs) == 0 && rule.Group == "" && len(rule.Plans) == 0 && rule.Rollout == nil {
			return fmt.Errorf("rule %q requires accounts, orgs, group, plans or rollout targeting", rule.ID)
		}
		if err := validateAccounts(rule.Accounts, api.PlatformFlagsMaxAccountsPerRule); err != nil {
			return fmt.Errorf("rule %q: %w", rule.ID, err)
		}
		if err := validateIDs(rule.Orgs, api.PlatformFlagsMaxOrgsPerRule, "organization"); err != nil {
			return fmt.Errorf("rule %q: %w", rule.ID, err)
		}
		if rule.Group != "" {
			if !flags.ValidKey(rule.Group) {
				return fmt.Errorf("rule %q: invalid group name %q", rule.ID, rule.Group)
			}
			if groups != nil {
				if _, ok := groups[rule.Group]; !ok {
					return fmt.Errorf("rule %q references unknown group %q", rule.ID, rule.Group)
				}
			}
		}
		seenPlans := map[string]bool{}
		for _, plan := range rule.Plans {
			if !api.Plan(plan).Valid() || seenPlans[plan] {
				return fmt.Errorf("rule %q: invalid or duplicate plan %q", rule.ID, plan)
			}
			seenPlans[plan] = true
		}
		if rule.Rollout != nil && (*rule.Rollout < 0 || *rule.Rollout > 10000) {
			return fmt.Errorf("rule %q: rollout must be 0..10000 basis points", rule.ID)
		}
	}
	return nil
}

// ValidateGroups bounds the shared group map.
func ValidateGroups(groups Groups) error {
	if len(groups) > api.PlatformFlagsMaxGroups {
		return fmt.Errorf("at most %d flag groups are allowed", api.PlatformFlagsMaxGroups)
	}
	for name, accounts := range groups {
		if !flags.ValidKey(name) {
			return fmt.Errorf("group name %q must match ^[a-z][a-z0-9_-]{0,63}$", name)
		}
		if err := validateAccounts(accounts, api.PlatformFlagsMaxAccountsPerGroup); err != nil {
			return fmt.Errorf("group %q: %w", name, err)
		}
	}
	return nil
}

func validateAccounts(accounts []string, limit int) error {
	return validateIDs(accounts, limit, "account")
}

// validateIDs bounds a list of canonical UUIDs naming accounts or
// organizations.
func validateIDs(ids []string, limit int, noun string) error {
	if len(ids) > limit {
		return fmt.Errorf("at most %d %ss are allowed", limit, noun)
	}
	seen := make(map[string]bool, len(ids))
	for _, raw := range ids {
		id, err := uuid.Parse(raw)
		if err != nil || id.String() != raw {
			return fmt.Errorf("%s %q is not a canonical %s UUID", noun, raw, noun)
		}
		if seen[raw] {
			return fmt.Errorf("duplicate %s %q", noun, raw)
		}
		seen[raw] = true
	}
	return nil
}

// GroupReferences maps each group name to the flags whose rules use it, so a
// group cannot be deleted while a flag still depends on it.
func GroupReferences(defs map[string]Definition) map[string][]string {
	refs := map[string][]string{}
	for key, def := range defs {
		for _, rule := range def.Rules {
			if rule.Group != "" && !slices.Contains(refs[rule.Group], key) {
				refs[rule.Group] = append(refs[rule.Group], key)
			}
		}
	}
	for group := range refs {
		slices.Sort(refs[group])
	}
	return refs
}

// FromAccountList converts the legacy release-gate shape (an on/off switch
// plus an optional account allowlist) into a definition. It is the boot
// fallback for gates that predate platform flags: enabled=false is off for
// everyone; all=true admits every account; otherwise only the listed accounts.
func FromAccountList(enabled, all bool, accounts []string) Definition {
	if !enabled {
		return Definition{}
	}
	if all {
		return Definition{Enabled: true, Default: true}
	}
	value := true
	normalized := normalizeAccounts(accounts)
	if len(normalized) == 0 {
		return Definition{Enabled: true}
	}
	return Definition{Enabled: true, Rules: []Rule{{ID: "bootstrap-accounts", Accounts: normalized, Value: &value}}}
}

// Marshal encodes a definition in its stored form.
func (d Definition) Marshal() json.RawMessage {
	raw, err := json.Marshal(d)
	if err != nil {
		// Definition contains only strings, bools and ints.
		panic(fmt.Sprintf("platformflags: marshal definition: %v", err))
	}
	return raw
}

// Marshal encodes groups in their stored form.
func (g Groups) Marshal() json.RawMessage {
	if g == nil {
		g = Groups{}
	}
	raw, err := json.Marshal(g)
	if err != nil {
		panic(fmt.Sprintf("platformflags: marshal groups: %v", err))
	}
	return raw
}
