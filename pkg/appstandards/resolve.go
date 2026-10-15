package appstandards

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"time"
)

type constraint struct {
	rule   Rule
	source Source
}

// Resolve is deterministic for a set of inherited layers. Explicit local
// replacements are validated against every requirement, including ancestors.
// A caller must reject writes when Violations is nonempty.
func Resolve(base Settings, layers []Layer, local Settings, exceptions []Exception, now time.Time, limits Limits) (Effective, error) {
	result := Effective{Values: Settings{}, Sources: map[Field][]Source{}, Violations: []Violation{}}
	if len(layers) > limits.Layers {
		return result, fmt.Errorf("too many inherited standard layers")
	}
	for field, value := range base {
		normal, err := normalizeValue(field, value, limits.SetEntries)
		if err != nil {
			return result, err
		}
		result.Values[field] = normal
	}
	layers = append([]Layer(nil), layers...)
	sort.Slice(layers, func(i, j int) bool {
		if scopeRank(layers[i].Scope) != scopeRank(layers[j].Scope) {
			return scopeRank(layers[i].Scope) < scopeRank(layers[j].Scope)
		}
		return layers[i].StandardID < layers[j].StandardID
	})
	constraints := map[Field][]constraint{}
	for _, layer := range layers {
		if err := resolveLayer(&result, constraints, layer, exceptions, now, limits); err != nil {
			return result, err
		}
	}
	for field, value := range local {
		normal, err := normalizeValue(field, value, limits.SetEntries)
		if err != nil {
			return result, err
		}
		result.Values[field] = normal
	}
	checkConstraints(&result, constraints)
	sort.Slice(result.Violations, func(i, j int) bool {
		a, b := result.Violations[i], result.Violations[j]
		if a.Field != b.Field {
			return a.Field < b.Field
		}
		if a.Source.StandardID != b.Source.StandardID {
			return a.Source.StandardID < b.Source.StandardID
		}
		return a.Code < b.Code
	})
	return result, nil
}

func resolveLayer(result *Effective, constraints map[Field][]constraint, layer Layer, exceptions []Exception, now time.Time, limits Limits) error {
	if scopeRank(layer.Scope) < 0 || layer.StandardID == "" || layer.Version < 1 {
		return fmt.Errorf("invalid inherited standard identity or scope")
	}
	definition, _, err := Normalize(layer.Definition, limits)
	if err != nil {
		return err
	}
	for field, rule := range definition {
		source := Source{AssignmentID: layer.AssignmentID, ScopeID: layer.ScopeID, StandardID: layer.StandardID, Version: layer.Version, Scope: layer.Scope, Mode: rule.Mode, Override: rule.Override}
		if err := applyException(field, layer, &rule, &source, exceptions, now, limits); err != nil {
			return err
		}
		result.Sources[field] = append(result.Sources[field], source)
		current := constraints[field]
		if rule.Mode == Default {
			if len(current) == 0 {
				result.Values[field] = bytes.Clone(rule.Value)
			}
			continue
		}
		candidate := bytes.Clone(rule.Value)
		for _, inherited := range current {
			candidate, err = combine(field, candidate, inherited.rule)
			if err != nil {
				return err
			}
		}
		result.Values[field] = candidate
		constraints[field] = append(current, constraint{rule: rule, source: source})
	}
	return nil
}

func applyException(field Field, layer Layer, rule *Rule, source *Source, exceptions []Exception, now time.Time, limits Limits) error {
	for _, exception := range exceptions {
		if exception.Field != field || exception.StandardID != layer.StandardID || exception.Version != layer.Version || !now.Before(exception.ExpiresAt) {
			continue
		}
		if source.ExceptionID != "" {
			return fmt.Errorf("multiple active exceptions for %s in one standard version", field)
		}
		if exception.ID == "" || exception.Reason == "" {
			return fmt.Errorf("exception identity and reason are required")
		}
		normal, err := normalizeValue(field, exception.Value, limits.SetEntries)
		if err != nil {
			return err
		}
		rule.Value, source.ExceptionID = normal, exception.ID
	}
	return nil
}

func combine(field Field, candidate json.RawMessage, inherited Rule) (json.RawMessage, error) {
	if inherited.Mode == Mandatory && inherited.Override == NoOverride {
		return bytes.Clone(inherited.Value), nil
	}
	if field == LogDestinations && inherited.Override == Extend {
		a, b := stringsValue(candidate), stringsValue(inherited.Value)
		a = append(a, b...)
		slices.Sort(a)
		return json.Marshal(slices.Compact(a))
	}
	if inherited.Mode == Restricted || inherited.Override == Narrow {
		return intersect(field, candidate, inherited.Value)
	}
	return bytes.Clone(inherited.Value), nil
}

func checkConstraints(result *Effective, constraints map[Field][]constraint) {
	for field, rules := range constraints {
		for _, rule := range rules {
			if !permitted(field, result.Values[field], rule.rule) {
				result.Violations = append(result.Violations, Violation{Field: field, Code: "standard_requirement_conflict", Source: rule.source})
			}
		}
	}
}

func permitted(field Field, candidate json.RawMessage, rule Rule) bool {
	if bytes.Equal(candidate, rule.Value) {
		return true
	}
	if rule.Mode == Default {
		return true
	}
	if rule.Override == Extend {
		return stringsContainAll(stringsValue(candidate), stringsValue(rule.Value))
	}
	if rule.Mode == Restricted || rule.Override == Narrow {
		return narrower(field, candidate, rule.Value)
	}
	return false
}

func stringsValue(raw json.RawMessage) []string {
	var values []string
	_ = json.Unmarshal(raw, &values)
	return values
}
func portsValue(raw json.RawMessage) []int {
	var values []int
	_ = json.Unmarshal(raw, &values)
	return values
}
func boolValue(raw json.RawMessage) bool {
	var value bool
	_ = json.Unmarshal(raw, &value)
	return value
}
func policyValue(raw json.RawMessage) string {
	var value string
	_ = json.Unmarshal(raw, &value)
	return value
}
func stringsContainAll(values, required []string) bool {
	for _, value := range required {
		if !slices.Contains(values, value) {
			return false
		}
	}
	return true
}
func scopeRank(scope string) int {
	switch scope {
	case "organization":
		return 0
	case "project":
		return 1
	case "application":
		return 2
	default:
		return -1
	}
}
