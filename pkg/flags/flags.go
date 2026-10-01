// Package flags evaluates immutable customer-scoped feature configuration.
// Flags control deployed application behavior, never authorization or entitlement.
package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

type Rule struct {
	ID        string   `json:"id"`
	Customers []string `json:"customers,omitempty"`
	Group     string   `json:"group,omitempty"`
	// Rollout is basis points (0..10000) of eligible customers. Nil is 100%.
	Rollout *int `json:"rollout,omitempty"`
	// Progression pins a boolean true rollout to one of a finite set of
	// percentage stages. Only the management API advances CurrentStage.
	Progression *ProgressiveRollout `json:"progression,omitempty"`
	// Value is a boolean for boolean flags, or a variant key for variant flags.
	// Variant rules may omit it to use the configured weighted allocation.
	Value any `json:"value,omitempty"`
}
type ProgressiveRollout struct {
	Stages       []int `json:"stages"`
	CurrentStage int   `json:"current_stage"`
	// AutoAdvance opts this plan into server-managed, one-stage-at-a-time
	// promotion after a full healthy observation window. The default is manual.
	AutoAdvance                   bool  `json:"auto_advance,omitempty"`
	MinimumUsedRequests           int64 `json:"minimum_used_requests"`
	MaximumHTTP5xxRateBasisPoints int   `json:"maximum_http_5xx_rate_basis_points"`
	MaximumP95LatencyMS           int   `json:"maximum_p95_latency_ms"`
	WindowSeconds                 int   `json:"window_seconds"`
}
type Flag struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	// Type is omitted for legacy boolean flags. Variant flags use "variant".
	Type     string        `json:"type,omitempty"`
	Enabled  bool          `json:"enabled"`
	Default  any           `json:"default"`
	Seed     string        `json:"seed"`
	Rules    []Rule        `json:"rules"`
	Variants []FlagVariant `json:"variants,omitempty"`
}
type FlagVariant struct {
	Key    string `json:"key"`
	Weight int    `json:"weight"`
}
type Config struct {
	Flags  []Flag              `json:"flags"`
	Groups map[string][]string `json:"groups"`
}
type Bundle struct {
	EnvironmentID string `json:"environment_id"`
	Version       int64  `json:"version"`
	Config
}
type Decision struct {
	Flag          string          `json:"flag"`
	Value         any             `json:"value"`
	Type          string          `json:"type,omitempty"`
	ConfigVersion int64           `json:"config_version"`
	RuleID        string          `json:"rule_id,omitempty"`
	Reason        string          `json:"reason"`
	Bucket        *int            `json:"bucket,omitempty"`
	RolloutBucket *int            `json:"rollout_bucket,omitempty"`
	Source        string          `json:"source"`
	InheritedFrom *EvidenceOrigin `json:"inherited_from,omitempty"`
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func ValidKey(s string) bool { return keyPattern.MatchString(s) }

// Bucket is specified across SDKs: SHA-256(seed + NUL + key + NUL + customer),
// first four bytes as unsigned big-endian, modulo 10000. Seed must be stable.
func Bucket(seed, key, customer string) int {
	h := sha256.Sum256([]byte(seed + "\x00" + key + "\x00" + customer))
	return int(binary.BigEndian.Uint32(h[:4]) % 10000)
}

// VariantBucket uses a separate hash domain from rollout eligibility so
// changing a percentage does not bias variant assignment toward the first
// allocation bucket. The wire algorithm is SHA-256(seed + NUL + key + NUL +
// "variant" + NUL + customer), first four bytes as unsigned big-endian modulo
// 10000.
func VariantBucket(seed, key, customer string) int {
	h := sha256.Sum256([]byte(seed + "\x00" + key + "\x00variant\x00" + customer))
	return int(binary.BigEndian.Uint32(h[:4]) % 10000)
}

// Evaluate performs no I/O and requires customer identity supplied by trusted
// application middleware. Anonymous contexts never match targeting rules.
func Evaluate(b Bundle, key, customer string, fallback bool) Decision {
	d := Decision{Flag: key, Value: fallback, ConfigVersion: b.Version, Reason: "flag_missing", Source: "fallback"}
	for _, f := range b.Flags {
		if f.Key != key {
			continue
		}
		if flagType(f) != "boolean" {
			d.Reason = "type_mismatch"
			return d
		}
		return evaluateFlag(b, f, customer, fallback)
	}
	return d
}

// EvaluateVariant evaluates a named variant, using fallback when the flag is
// missing or has a different type. Variant values are deterministic
// for each customer and use the same immutable server-owned flag seed.
func EvaluateVariant(b Bundle, key, customer, fallback string) Decision {
	d := Decision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: b.Version, Reason: "flag_missing", Source: "fallback"}
	for _, f := range b.Flags {
		if f.Key != key {
			continue
		}
		if flagType(f) != "variant" {
			d.Reason = "type_mismatch"
			return d
		}
		return evaluateFlag(b, f, customer, fallback)
	}
	return d
}

func flagType(f Flag) string {
	if f.Type == "" || f.Type == "boolean" {
		return "boolean"
	}
	return f.Type
}

func evaluateFlag(b Bundle, f Flag, customer string, fallback any) Decision {
	typ := flagType(f)
	d := Decision{Flag: f.Key, Value: fallback, ConfigVersion: b.Version, Reason: "type_mismatch", Source: "fallback"}
	if typ == "variant" {
		d.Type = "variant"
	}
	switch typ {
	case "boolean":
		if _, ok := fallback.(bool); !ok {
			return d
		}
	case "variant":
		if _, ok := fallback.(string); !ok {
			return d
		}
	default:
		return d
	}
	d.Value = fallback
	defaultValue := f.Default
	if typ == "boolean" && defaultValue == nil {
		// Preserve the Go zero-value behavior of the original boolean field.
		defaultValue = false
	}
	if typ == "boolean" {
		if _, ok := defaultValue.(bool); !ok {
			return d
		}
	} else {
		if _, ok := defaultValue.(string); !ok {
			return d
		}
	}
	d.Value, d.Source, d.Reason = defaultValue, "configuration", "default"
	if !f.Enabled {
		d.Reason = "disabled"
		return d
	}
	if customer == "" {
		d.Reason = "customer_missing"
		return d
	}
	for _, r := range f.Rules {
		if len(r.Customers) > 0 && !slices.Contains(r.Customers, customer) {
			continue
		}
		if r.Group != "" && !slices.Contains(b.Groups[r.Group], customer) {
			continue
		}
		if r.Rollout != nil {
			bucket := Bucket(f.Seed, f.Key, customer)
			if bucket >= *r.Rollout {
				continue
			}
			if typ == "boolean" {
				d.Bucket = &bucket
			} else {
				d.RolloutBucket = &bucket
			}
		}
		if typ == "boolean" {
			value, ok := r.Value.(bool)
			if r.Value == nil {
				value, ok = false, true
			}
			if !ok {
				return d
			}
			d.Value = value
		} else if r.Value != nil {
			value, ok := r.Value.(string)
			if !ok {
				return d
			}
			d.Value = value
		} else {
			bucket := VariantBucket(f.Seed, f.Key, customer)
			d.Bucket = &bucket
			d.Value = chooseVariant(f.Variants, bucket)
		}
		d.RuleID, d.Reason = r.ID, "rule_match"
		return d
	}
	return d
}

func chooseVariant(variants []FlagVariant, bucket int) string {
	for _, variant := range variants {
		if bucket < variant.Weight {
			return variant.Key
		}
		bucket -= variant.Weight
	}
	return ""
}

// Validate bounds every collection and rejects ambiguous or dangling rules.
// Customer ownership is checked by the state writer before publication.
func Validate(c Config) error {
	if len(c.Flags) > api.FlagsMaxPerEnvironment || len(c.Groups) > api.FlagsMaxGroups {
		return fmt.Errorf("flag or group limit exceeded")
	}
	for key, customers := range c.Groups {
		if !ValidKey(key) {
			return fmt.Errorf("invalid group key %q", key)
		}
		if err := validateCustomers(customers); err != nil {
			return err
		}
	}
	keys := map[string]bool{}
	for _, f := range c.Flags {
		if !ValidKey(f.Key) || keys[f.Key] {
			return fmt.Errorf("invalid or duplicate flag key %q", f.Key)
		}
		keys[f.Key] = true
		if len(f.Description) > api.FlagsMaxDescriptionBytes || len(f.Seed) > api.FlagsMaxSeedBytes || strings.ContainsRune(f.Seed, 0) {
			return fmt.Errorf("invalid flag description or seed")
		}
		typ := flagType(f)
		if typ != "boolean" && typ != "variant" {
			return fmt.Errorf("invalid flag type %q", f.Type)
		}
		variantKeys := map[string]bool{}
		if typ == "boolean" {
			if f.Default != nil {
				if _, ok := f.Default.(bool); !ok {
					return fmt.Errorf("boolean flag requires a boolean default")
				}
			}
			if len(f.Variants) != 0 {
				return fmt.Errorf("boolean flag requires a boolean default and no variants")
			}
		} else {
			defaultValue, ok := f.Default.(string)
			if !ok || len(f.Variants) < 2 || len(f.Variants) > api.FlagsMaxVariants {
				return fmt.Errorf("variant flag requires a string default and 2..%d variants", api.FlagsMaxVariants)
			}
			weightTotal := 0
			for _, variant := range f.Variants {
				if !ValidKey(variant.Key) || variantKeys[variant.Key] || variant.Weight < 0 || variant.Weight > 10000 {
					return fmt.Errorf("invalid or duplicate flag variant")
				}
				variantKeys[variant.Key] = true
				weightTotal += variant.Weight
			}
			if weightTotal != 10000 || !variantKeys[defaultValue] {
				return fmt.Errorf("variant weights must total 10000 and default must name a variant")
			}
		}
		if len(f.Rules) > api.FlagsMaxRules {
			return fmt.Errorf("rule limit exceeded")
		}
		ids := map[string]bool{}
		for _, r := range f.Rules {
			if !ValidKey(r.ID) || ids[r.ID] {
				return fmt.Errorf("invalid or duplicate rule ID")
			}
			ids[r.ID] = true
			if err := validateCustomers(r.Customers); err != nil {
				return err
			}
			if r.Group != "" {
				if _, ok := c.Groups[r.Group]; !ok {
					return fmt.Errorf("unknown group %q", r.Group)
				}
			}
			if r.Rollout != nil && (*r.Rollout < 0 || *r.Rollout > 10000) {
				return fmt.Errorf("rollout must be 0..10000 basis points")
			}
			if r.Progression != nil {
				value, isBoolean := r.Value.(bool)
				if typ != "boolean" || !isBoolean || !value || r.Rollout == nil {
					return fmt.Errorf("progressive rollout requires a boolean true rule with an explicit rollout")
				}
				p := r.Progression
				if len(p.Stages) < 2 || len(p.Stages) > api.FlagsMaxProgressiveStages || p.CurrentStage < 0 || p.CurrentStage >= len(p.Stages) {
					return fmt.Errorf("progressive rollout requires 2..%d stages and a valid current_stage", api.FlagsMaxProgressiveStages)
				}
				if p.Stages[len(p.Stages)-1] != 10000 || *r.Rollout != p.Stages[p.CurrentStage] {
					return fmt.Errorf("progressive rollout must end at 10000 basis points and match the active stage")
				}
				for i, stage := range p.Stages {
					if stage < 1 || stage > 10000 || i > 0 && stage <= p.Stages[i-1] {
						return fmt.Errorf("progressive rollout stages must increase from 1 to 10000 basis points")
					}
				}
				if p.MinimumUsedRequests < 1 || p.MinimumUsedRequests > api.FlagsMaxProgressiveMinimumRequests || p.MaximumHTTP5xxRateBasisPoints < 0 || p.MaximumHTTP5xxRateBasisPoints > 10000 || p.MaximumP95LatencyMS < 1 || p.MaximumP95LatencyMS > api.FlagsMaxProgressiveLatencyMS || p.WindowSeconds < api.FlagsMinProgressiveWindowSeconds || p.WindowSeconds > api.FlagsMaxProgressiveWindowSeconds {
					return fmt.Errorf("invalid progressive rollout evidence thresholds")
				}
			}
			if len(r.Customers) == 0 && r.Group == "" && r.Rollout == nil {
				return fmt.Errorf("rule requires customer, group, or rollout targeting")
			}
			if typ == "boolean" {
				if r.Value != nil {
					if _, ok := r.Value.(bool); !ok {
						return fmt.Errorf("boolean rule requires a boolean value")
					}
				}
			} else if r.Value != nil {
				value, ok := r.Value.(string)
				if !ok || !variantKeys[value] {
					return fmt.Errorf("variant rule value must name a configured variant")
				}
			}
		}
	}
	return nil
}
func validateCustomers(ids []string) error {
	if len(ids) > api.FlagsMaxCustomers {
		return fmt.Errorf("customer limit exceeded")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if id == "" || len(id) > api.FlagsMaxCustomerIDBytes || strings.ContainsRune(id, 0) || seen[id] {
			return fmt.Errorf("invalid or duplicate customer identity")
		}
		seen[id] = true
	}
	return nil
}
