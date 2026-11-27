package preview

// Attribute targeting (ADR 20261114150000 §2): rule conditions on
// gateway-verified gregale.* attributes and application-supplied app.*
// attributes. A condition on a missing attribute never matches.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

const (
	flagAttributeCountry  = "gregale.country"
	flagAttributeUAFamily = "gregale.ua_family"
	// flagAttributeCohort is the gateway's X-Faas-Flag-Cohorts value
	// (ADR 20261119102741 §4).
	flagAttributeCohort  = "gregale.cohort"
	flagAppAttributePref = "app."

	maxFlagConditions      = 8
	maxFlagConditionValues = 100
	maxFlagAppAttributes   = 16
)

var (
	flagCountryPattern       = regexp.MustCompile(`^[A-Z]{2}$`)
	flagAppAttributePattern  = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	flagAttributeNamePattern = regexp.MustCompile(`^(gregale\.(country|ua_family|cohort)|app\.[a-z][a-z0-9_]{0,31})$`)
)

// parseFlagCohorts reads a gregale.cohort value: cohort keys, "-" for none;
// ok is false for a malformed value.
func parseFlagCohorts(value string) (keys []string, ok bool) {
	if value == "-" {
		return nil, true
	}
	if value == "" {
		return nil, false
	}
	keys = strings.Split(value, ",")
	if len(keys) > 20 {
		return nil, false
	}
	for _, k := range keys {
		if !validFlagKey(k) {
			return nil, false
		}
	}
	return keys, true
}

// matchFlagCohorts applies a gregale.cohort condition: in matches a member
// of any listed cohort, not_in a customer in none of them.
func matchFlagCohorts(c flagCondition, value string) bool {
	keys, ok := parseFlagCohorts(value)
	if !ok || (c.op != "in" && c.op != "not_in") {
		return false
	}
	member := false
	for _, v := range c.values {
		for _, k := range keys {
			if v == k {
				member = true
			}
		}
	}
	return member == (c.op == "in")
}

type flagCondition struct {
	attribute string
	op        string
	values    []string
}

type flagAttributesContextKey struct{}

type wireFlagCondition struct {
	Attribute string   `json:"attribute"`
	Op        string   `json:"op"`
	Values    []string `json:"values"`
}

func validFlagAttributeValue(value string) bool {
	if len(value) == 0 || len(value) > 128 {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

func decodeFlagConditions(raw json.RawMessage) ([]flagCondition, error) {
	var wire []wireFlagCondition
	if isJSONNull(raw) || json.Unmarshal(raw, &wire) != nil || len(wire) == 0 || len(wire) > maxFlagConditions {
		return nil, errors.New("invalid Flags rule attributes")
	}
	out := make([]flagCondition, 0, len(wire))
	for _, c := range wire {
		if !flagAttributeNamePattern.MatchString(c.Attribute) || (c.Op != "in" && c.Op != "not_in" && c.Op != "ends_with") ||
			(c.Op == "ends_with" && !strings.HasPrefix(c.Attribute, flagAppAttributePref)) ||
			len(c.Values) == 0 || len(c.Values) > maxFlagConditionValues {
			return nil, errors.New("invalid Flags rule attribute condition")
		}
		seen := make(map[string]struct{}, len(c.Values))
		for _, v := range c.Values {
			if _, dup := seen[v]; dup || !validFlagAttributeValue(v) {
				return nil, errors.New("invalid Flags rule attribute value")
			}
			seen[v] = struct{}{}
		}
		out = append(out, flagCondition{attribute: c.Attribute, op: c.Op, values: c.Values})
	}
	return out, nil
}

func matchFlagConditions(conditions []flagCondition, attributes map[string]string) bool {
	for _, c := range conditions {
		value, ok := attributes[c.attribute]
		if c.attribute == flagAttributeCohort {
			if !ok || !matchFlagCohorts(c, value) {
				return false
			}
			continue
		}
		if !ok || !validFlagAttributeValue(value) {
			return false
		}
		found := false
		for _, v := range c.values {
			if c.op == "ends_with" && strings.HasSuffix(value, v) || c.op != "ends_with" && value == v {
				found = true
				break
			}
		}
		if found == (c.op == "not_in") {
			return false
		}
	}
	return true
}

// requestFlagAttributes merges the gateway attributes of the request with the
// application attributes bound by WithAttributes.
func requestFlagAttributes(ctx context.Context, state *flagRequestState) map[string]string {
	app, _ := ctx.Value(flagAttributesContextKey{}).(map[string]string)
	if len(app) == 0 {
		return state.attributes
	}
	merged := make(map[string]string, len(state.attributes)+len(app))
	for k, v := range state.attributes {
		merged[k] = v
	}
	for k, v := range app {
		merged[k] = v
	}
	return merged
}

// WithAttributes returns a request context whose flag checks see the given
// application attributes as app.<name>, for attribute-targeted rules. Call it
// after the application authenticates its user. Names are lowercase
// identifiers; an invalid value is dropped and reads as missing. Attribute
// values are not included in evidence or the decision propagation header.
func (f *GregaleFlags) WithAttributes(ctx context.Context, attributes map[string]string) (context.Context, error) {
	if _, err := f.requestState(ctx); err != nil {
		return nil, err
	}
	prior, _ := ctx.Value(flagAttributesContextKey{}).(map[string]string)
	next := make(map[string]string, len(prior)+len(attributes))
	for k, v := range prior {
		next[k] = v
	}
	for name, value := range attributes {
		if !flagAppAttributePattern.MatchString(name) {
			return nil, fmt.Errorf("faas: invalid flag attribute name %q", name)
		}
		if validFlagAttributeValue(value) {
			next[flagAppAttributePref+name] = value
		} else {
			delete(next, flagAppAttributePref+name)
		}
	}
	if len(next) > maxFlagAppAttributes {
		return nil, fmt.Errorf("faas: at most %d application attributes per request", maxFlagAppAttributes)
	}
	return context.WithValue(ctx, flagAttributesContextKey{}, next), nil
}
