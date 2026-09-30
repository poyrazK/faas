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
	Value   bool `json:"value"`
}
type Flag struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled"`
	Default     bool   `json:"default"`
	Seed        string `json:"seed"`
	Rules       []Rule `json:"rules"`
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
	Flag          string `json:"flag"`
	Value         bool   `json:"value"`
	ConfigVersion int64  `json:"config_version"`
	RuleID        string `json:"rule_id,omitempty"`
	Reason        string `json:"reason"`
	Bucket        *int   `json:"bucket,omitempty"`
	Source        string `json:"source"`
}

var keyPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)

func ValidKey(s string) bool { return keyPattern.MatchString(s) }

// Bucket is specified across SDKs: SHA-256(seed + NUL + key + NUL + customer),
// first four bytes as unsigned big-endian, modulo 10000. Seed must be stable.
func Bucket(seed, key, customer string) int {
	h := sha256.Sum256([]byte(seed + "\x00" + key + "\x00" + customer))
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
		d.Value, d.Source, d.Reason = f.Default, "configuration", "default"
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
				d.Bucket = &bucket
			}
			d.Value, d.RuleID, d.Reason = r.Value, r.ID, "rule_match"
			return d
		}
		return d
	}
	return d
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
			if len(r.Customers) == 0 && r.Group == "" && r.Rollout == nil {
				return fmt.Errorf("rule requires customer, group, or rollout targeting")
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
