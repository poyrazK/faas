package environmentsync

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

// BuildPlan binds every proposed mutation to the desired revision, observed
// intent, resource identities, ownership, override state, and generation.
// Adoption is an explicit operation even if the current value already equals
// Git. Pruning is opt-in and can affect only the caller's previously owned
// fields; unowned fields and another manager's fields are never removed.
func BuildPlan(desired DesiredState, observed ObservedState, owners []Ownership, options PlanOptions) (Plan, error) {
	plan := Plan{
		Manager: options.Manager, Revision: options.Revision, CommitSHA: options.CommitSHA, Generation: options.Generation,
		DesiredDigest: desired.Digest, ObservedVersion: observed.Version,
		Changes: []Change{}, BlockingReasons: []string{},
	}
	if options.Manager == "" || options.Revision == "" || options.Generation <= 0 || observed.Version < 0 {
		return plan, fmt.Errorf("plan needs an authenticated manager, revision, positive generation, and observed version")
	}
	// Recompile instead of trusting a caller-provided field list or digest.
	compiled, err := Compile(desired.Definition)
	if err != nil || compiled.Digest != desired.Digest {
		return plan, fmt.Errorf("desired state does not match its compiled definition")
	}
	// Source parameters alone do not identify deployed code. A new approved
	// commit must reconcile source workloads even if their manifest is unchanged.
	for _, name := range sortedKeys(compiled.Definition.Workloads) {
		workload := compiled.Definition.Workloads[name]
		if workload.Source == nil || workload.Source.Kind == "image" {
			continue
		}
		if !validCommitSHA(options.CommitSHA) {
			return plan, fmt.Errorf("source workloads require an immutable approved commit SHA")
		}
		value, _ := json.Marshal(options.CommitSHA)
		compiled.Fields = append(compiled.Fields, Field{Resource: "workload/" + name, Path: "source_revision", Value: value})
	}
	wanted, err := indexFields(compiled.Fields)
	if err != nil {
		return plan, err
	}
	current, err := indexFields(observed.Fields)
	if err != nil {
		return plan, err
	}
	ownership, err := indexOwnership(owners)
	if err != nil {
		return plan, err
	}
	overrides := make(map[string]Override)
	for _, override := range options.Overrides {
		key := (Field{Resource: override.Resource, Path: override.Path}).Key()
		if _, exists := overrides[key]; exists {
			return plan, fmt.Errorf("duplicate override for %q", key)
		}
		if override.ExpiresAt.After(options.Now) {
			overrides[key] = override
		}
	}
	plan.BlockingReasons = append(plan.BlockingReasons, observed.Unsupported...)
	for _, key := range sortedKeys(wanted) {
		next := wanted[key]
		old, exists := current[key]
		owner, owned := ownership[key]
		change := Change{Resource: next.Resource, Path: next.Path, After: next.Value}
		if exists {
			change.Before = old.Value
		}
		switch {
		case owned && owner.Manager != options.Manager:
			change.Action, change.Reason = "conflict", "field belongs to another manager"
			plan.BlockingReasons = append(plan.BlockingReasons, key+": ownership conflict")
		case owned && activeOverride(overrides, key):
			change.Action, change.Reason = "overridden", "temporary override is active"
		case !owned && exists && !options.Adopt:
			change.Action, change.Reason = "conflict", "existing field requires reviewed adoption"
			plan.BlockingReasons = append(plan.BlockingReasons, key+": adoption required")
		case !owned && exists:
			change.Action = "adopt"
			if !bytes.Equal(old.Value, next.Value) {
				change.Reason = "adoption preserves the value; reconciliation will apply the approved definition"
			}
		case !owned:
			change.Action = "create"
		case !exists || !bytes.Equal(old.Value, next.Value):
			change.Action = "update"
		default:
			change.Action = "keep"
		}
		plan.Changes = append(plan.Changes, change)
	}
	for _, key := range sortedKeys(ownership) {
		owner := ownership[key]
		if owner.Manager != options.Manager {
			continue
		}
		if _, exists := wanted[key]; exists {
			continue
		}
		change := Change{Resource: owner.Resource, Path: owner.Path}
		if old, exists := current[key]; exists {
			change.Before = old.Value
		}
		switch {
		case activeOverride(overrides, key):
			change.Action, change.Reason = "overridden", "temporary override is active"
		case options.Prune:
			change.Action = "remove"
		default:
			change.Action, change.Reason = "remove_candidate", "pruning requires explicit authorization"
			plan.BlockingReasons = append(plan.BlockingReasons, key+": pruning is disabled")
		}
		plan.Changes = append(plan.Changes, change)
	}
	for _, key := range sortedKeys(current) {
		if _, exists := wanted[key]; exists {
			continue
		}
		if _, exists := ownership[key]; exists {
			continue
		}
		old := current[key]
		plan.Changes = append(plan.Changes, Change{Resource: old.Resource, Path: old.Path, Action: "retain_unmanaged", Before: old.Value})
	}
	slices.SortFunc(plan.Changes, func(a, b Change) int {
		return strings.Compare((Field{Resource: a.Resource, Path: a.Path}).Key(), (Field{Resource: b.Resource, Path: b.Path}).Key())
	})
	slices.Sort(plan.BlockingReasons)
	plan.BlockingReasons = slices.Compact(plan.BlockingReasons)
	canonicalObserved := observed
	canonicalObserved.Fields = make([]Field, 0, len(current))
	for _, key := range sortedKeys(current) {
		canonicalObserved.Fields = append(canonicalObserved.Fields, current[key])
	}
	canonicalObserved.Unsupported = append([]string(nil), observed.Unsupported...)
	slices.Sort(canonicalObserved.Unsupported)
	canonicalOwners := make([]Ownership, 0, len(ownership))
	for _, key := range sortedKeys(ownership) {
		canonicalOwners = append(canonicalOwners, ownership[key])
	}
	canonicalOverrides := make([]Override, 0, len(overrides))
	for _, key := range sortedKeys(overrides) {
		canonicalOverrides = append(canonicalOverrides, overrides[key])
	}
	preimage, err := json.Marshal(struct {
		Plan      Plan          `json:"plan"`
		Observed  ObservedState `json:"observed"`
		Owners    []Ownership   `json:"owners"`
		Overrides []Override    `json:"overrides"`
		Adopt     bool          `json:"adopt"`
		Prune     bool          `json:"prune"`
	}{plan, canonicalObserved, canonicalOwners, canonicalOverrides, options.Adopt, options.Prune})
	if err != nil {
		return plan, err
	}
	plan.Hash = digest(preimage)
	return plan, nil
}

func validCommitSHA(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, char := range value {
		if !(char >= 'a' && char <= 'f' || char >= '0' && char <= '9') {
			return false
		}
	}
	return true
}

func indexFields(fields []Field) (map[string]Field, error) {
	out := make(map[string]Field, len(fields))
	for _, field := range fields {
		if field.Resource == "" || field.Path == "" || strings.Contains(field.Resource+field.Path, "#") {
			return nil, fmt.Errorf("invalid managed field identity")
		}
		if _, exists := out[field.Key()]; exists {
			return nil, fmt.Errorf("duplicate managed field %q", field.Key())
		}
		canonical, err := canonicalJSON(field.Value)
		if err != nil {
			return nil, fmt.Errorf("invalid managed field value for %q", field.Key())
		}
		field.Value = canonical
		out[field.Key()] = field
	}
	return out, nil
}

func indexOwnership(owners []Ownership) (map[string]Ownership, error) {
	out := make(map[string]Ownership, len(owners))
	for _, owner := range owners {
		if owner.Manager == "" {
			return nil, fmt.Errorf("ownership record needs a manager")
		}
		indexed, err := indexFields([]Field{owner.Field})
		if err != nil {
			return nil, err
		}
		owner.Field = indexed[owner.Key()]
		if _, exists := out[owner.Key()]; exists {
			return nil, fmt.Errorf("duplicate ownership record for %q", owner.Key())
		}
		out[owner.Key()] = owner
	}
	return out, nil
}

func activeOverride(overrides map[string]Override, key string) bool {
	_, exists := overrides[key]
	return exists
}
