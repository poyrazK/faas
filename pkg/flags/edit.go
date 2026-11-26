package flags

// Configuration edits shared by the CLI's short flag verbs (ADR
// 20261114150000 §1) and the management MCP flag plans
// (ADR-20261117215858). Each edit changes a Config in place and returns a
// one-line summary; the caller publishes the result with the version it read.

import (
	"fmt"
	"math"
	"slices"
	"strconv"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// EveryonePercentRule is the catch-all percentage rollout rule.
	EveryonePercentRule = "everyone-percent"
	// TypeVariant marks a variant flag.
	TypeVariant = "variant"
)

// Edit changes cfg in place and returns a one-line summary.
type Edit func(cfg *Config) (string, error)

// FindFlag returns the flag named key.
func FindFlag(cfg *Config, key string) (*Flag, error) {
	for i := range cfg.Flags {
		if cfg.Flags[i].Key == key {
			return &cfg.Flags[i], nil
		}
	}
	return nil, fmt.Errorf("flag %q does not exist; create it with gregale flag create %s", key, key)
}

// IsVariant reports whether f is a variant flag.
func IsVariant(f *Flag) bool { return f.Type == TypeVariant }

// ParseValue turns a textual value into the JSON value for flag f: a
// variant key for a variant flag, true or false otherwise.
func ParseValue(f *Flag, raw string) (any, error) {
	if IsVariant(f) {
		for _, v := range f.Variants {
			if v.Key == raw {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("%q is not a variant of %s", raw, f.Key)
	}
	switch raw {
	case "true":
		return true, nil
	case "false":
		return false, nil
	}
	return nil, fmt.Errorf("boolean flag %s takes true or false, not %q", f.Key, raw)
}

// EditCreate adds a new flag. variants, when given, makes a variant flag with
// evenly split weights; defaultValue, when non-nil, sets its default.
func EditCreate(key string, variants []string, defaultValue *string, description string) Edit {
	return func(cfg *Config) (string, error) {
		if _, err := FindFlag(cfg, key); err == nil {
			return "", fmt.Errorf("flag %q already exists", key)
		}
		f := Flag{Key: key, Description: description, Enabled: true, Default: false, Rules: []Rule{}}
		if len(variants) > 0 {
			if len(variants) < 2 {
				return "", fmt.Errorf("--variants needs at least two keys")
			}
			f.Type = TypeVariant
			share := 10000 / len(variants)
			for i, k := range variants {
				weight := share
				if i == 0 {
					weight += 10000 - share*len(variants)
				}
				f.Variants = append(f.Variants, FlagVariant{Key: k, Weight: weight})
			}
			f.Default = variants[0]
		}
		if defaultValue != nil {
			v, err := ParseValue(&f, *defaultValue)
			if err != nil {
				return "", err
			}
			f.Default = v
		}
		cfg.Flags = append(cfg.Flags, f)
		return fmt.Sprintf("Created %s (default %v)", key, f.Default), nil
	}
}

// EditSet changes a flag's default and description; nil leaves one as is.
func EditSet(key string, defaultValue, description *string) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		var changed []string
		if defaultValue != nil {
			v, err := ParseValue(f, *defaultValue)
			if err != nil {
				return "", err
			}
			f.Default = v
			changed = append(changed, fmt.Sprintf("default %v", v))
		}
		if description != nil {
			f.Description = *description
			changed = append(changed, "description")
		}
		return fmt.Sprintf("Updated %s: %s", key, strings.Join(changed, ", ")), nil
	}
}

// EditOnOff enables or disables a flag.
func EditOnOff(key string, on bool) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		f.Enabled = on
		if on {
			return fmt.Sprintf("Enabled %s", key), nil
		}
		// A disabled flag serves its default; for a boolean that must be
		// false, or "off" would leave the behavior on (ADR-377).
		if !IsVariant(f) && f.Default == true {
			f.Default = false
			return fmt.Sprintf("Disabled %s and set its default to false", key), nil
		}
		return fmt.Sprintf("Disabled %s; it serves its default %v", key, f.Default), nil
	}
}

// EditMeta applies owner and expires after edit (ADR 20261114150000 §4); nil
// leaves one as is. expires "never" or "" clears the date.
func EditMeta(edit Edit, key string, owner, expires *string) Edit {
	return func(cfg *Config) (string, error) {
		summary, err := edit(cfg)
		if err != nil {
			return "", err
		}
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		var changed []string
		if owner != nil {
			if !ValidOwner(*owner) {
				return "", fmt.Errorf("--owner must be at most %d bytes of printable text", api.FlagsMaxOwnerBytes)
			}
			f.Owner = *owner
			changed = append(changed, "owner "+*owner)
		}
		if expires != nil {
			if *expires == "never" || *expires == "" {
				f.ExpiresAt = ""
				changed = append(changed, "no expiry")
			} else {
				if _, err := ParseExpiresAt(*expires); err != nil {
					return "", err
				}
				f.ExpiresAt = *expires
				changed = append(changed, "expires "+*expires)
			}
		}
		if len(changed) == 0 {
			return summary, nil
		}
		if strings.HasPrefix(summary, "Updated "+key+": ") {
			return summary + ", " + strings.Join(changed, ", "), nil
		}
		if strings.HasSuffix(summary, ": ") {
			return summary + strings.Join(changed, ", "), nil
		}
		return summary + "; " + strings.Join(changed, ", "), nil
	}
}

// EditKill sets or clears a flag's kill (ADR 20261114150000 §3). A killed
// flag serves the application's fallback on the next request through the
// gateway, whatever its default or rules say.
func EditKill(key string, killed bool) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if f.Killed == killed {
			if killed {
				return fmt.Sprintf("%s is already killed", key), nil
			}
			return fmt.Sprintf("%s is not killed", key), nil
		}
		f.Killed = killed
		if killed {
			return fmt.Sprintf("Killed %s: requests now get the fallback value in your code", key), nil
		}
		return fmt.Sprintf("Revived %s: it serves its configuration again", key), nil
	}
}

// ParsePercent accepts "12", "12.5" or "12.5%" and returns basis points.
func ParsePercent(raw string) (int, error) {
	s := strings.TrimSuffix(strings.TrimSpace(raw), "%")
	p, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(p) || p < 0 || p > 100 {
		return 0, fmt.Errorf("percentage must be a number from 0 to 100, not %q", raw)
	}
	if i := strings.IndexByte(s, '.'); i >= 0 && len(s)-i-1 > 2 {
		return 0, fmt.Errorf("percentage takes at most two decimals, not %q", raw)
	}
	return int(math.Round(p * 100)), nil
}

// EditPercent sets the everyone-percent rollout in basis points; zero removes it.
func EditPercent(key string, bp int) Edit {
	return func(cfg *Config) (string, error) {
		if bp < 0 || bp > 10000 {
			return "", fmt.Errorf("percentage must be from 0 to 100")
		}
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		at := slices.IndexFunc(f.Rules, func(r Rule) bool { return r.ID == EveryonePercentRule })
		if at >= 0 && f.Rules[at].Progression != nil {
			return "", fmt.Errorf("rule %s has a progression plan; advance it with gregale flag promote", EveryonePercentRule)
		}
		if bp == 0 {
			if at < 0 {
				return fmt.Sprintf("%s has no percentage rollout", key), nil
			}
			f.Rules = slices.Delete(f.Rules, at, at+1)
			return fmt.Sprintf("Removed the percentage rollout of %s", key), nil
		}
		rollout := bp
		rule := Rule{ID: EveryonePercentRule, Rollout: &rollout}
		if !IsVariant(f) {
			rule.Value = true
		}
		if at >= 0 {
			f.Rules = slices.Delete(f.Rules, at, at+1)
		}
		f.Rules = append(f.Rules, rule)
		return fmt.Sprintf("%s now serves %s to %s%% of customers", key, ruleValueText(f, rule), FormatBasisPoints(bp)), nil
	}
}

func ruleValueText(f *Flag, r Rule) string {
	if r.Value == nil && IsVariant(f) {
		return "weighted variants"
	}
	return fmt.Sprint(r.Value)
}

// FormatBasisPoints renders basis points as a percentage without trailing zeros.
func FormatBasisPoints(bp int) string {
	return strings.TrimSuffix(strings.TrimRight(strconv.FormatFloat(float64(bp)/100, 'f', 2, 64), "0"), ".")
}

// EditRuleAdd inserts rule before the everyone-percent rule, so a targeted
// rule wins over the catch-all rollout, or appends it. A string rule value
// is parsed against the flag; no value serves true on a boolean flag.
func EditRuleAdd(key string, rule Rule) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if slices.ContainsFunc(f.Rules, func(r Rule) bool { return r.ID == rule.ID }) {
			return "", fmt.Errorf("flag %s already has a rule %q; remove it first with gregale flag rule delete", key, rule.ID)
		}
		if raw, ok := rule.Value.(string); ok {
			v, err := ParseValue(f, raw)
			if err != nil {
				return "", err
			}
			rule.Value = v
		} else if !IsVariant(f) {
			rule.Value = true
		}
		at := slices.IndexFunc(f.Rules, func(r Rule) bool { return r.ID == EveryonePercentRule })
		if at < 0 {
			at = len(f.Rules)
		}
		f.Rules = slices.Insert(f.Rules, at, rule)
		return fmt.Sprintf("Added rule %s to %s at position %d", rule.ID, key, at+1), nil
	}
}

// EditRuleRemove removes the rule with id from the flag.
func EditRuleRemove(key, id string) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		at := slices.IndexFunc(f.Rules, func(r Rule) bool { return r.ID == id })
		if at < 0 {
			return "", fmt.Errorf("flag %s has no rule %q", key, id)
		}
		f.Rules = slices.Delete(f.Rules, at, at+1)
		return fmt.Sprintf("Removed rule %s from %s", id, key), nil
	}
}

// EditRemove deletes the flag.
func EditRemove(key string) Edit {
	return func(cfg *Config) (string, error) {
		at := slices.IndexFunc(cfg.Flags, func(f Flag) bool { return f.Key == key })
		if at < 0 {
			return "", fmt.Errorf("flag %q does not exist", key)
		}
		cfg.Flags = slices.Delete(cfg.Flags, at, at+1)
		return fmt.Sprintf("Removed %s", key), nil
	}
}

// EditEdgeSet routes path at the edge: requests the flag decides as variant
// are rewritten to rewrite (ADR-20261124212701). It adds the route, or the
// variant's action to an existing route of the flag.
func EditEdgeSet(key, path, variant, rewrite string) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if !IsVariant(f) {
			return "", fmt.Errorf("flag %q is boolean; only variant flags route at the edge", key)
		}
		if !slices.ContainsFunc(f.Variants, func(v FlagVariant) bool { return v.Key == variant }) {
			return "", fmt.Errorf("flag %q has no variant %q", key, variant)
		}
		if f.Edge == nil {
			f.Edge = &Edge{}
		}
		at := slices.IndexFunc(f.Edge.Routes, func(r EdgeRoute) bool { return r.Path == path })
		if at < 0 {
			f.Edge.Routes = append(f.Edge.Routes, EdgeRoute{Path: path, Variants: map[string]EdgeAction{}})
			at = len(f.Edge.Routes) - 1
		}
		if f.Edge.Routes[at].Variants == nil {
			f.Edge.Routes[at].Variants = map[string]EdgeAction{}
		}
		f.Edge.Routes[at].Variants[variant] = EdgeAction{Rewrite: rewrite}
		return fmt.Sprintf("%s routes %s: variant %s serves %s at the edge", key, path, variant, rewrite), nil
	}
}

// EditEdgeRemove stops routing path at the edge, or every path of the flag
// when path is empty.
func EditEdgeRemove(key, path string) Edit {
	return func(cfg *Config) (string, error) {
		f, err := FindFlag(cfg, key)
		if err != nil {
			return "", err
		}
		if f.Edge == nil {
			return fmt.Sprintf("%s routes nothing at the edge", key), nil
		}
		if path == "" {
			f.Edge = nil
			return fmt.Sprintf("%s no longer routes at the edge", key), nil
		}
		at := slices.IndexFunc(f.Edge.Routes, func(r EdgeRoute) bool { return r.Path == path })
		if at < 0 {
			return "", fmt.Errorf("flag %q does not route %s at the edge", key, path)
		}
		f.Edge.Routes = slices.Delete(f.Edge.Routes, at, at+1)
		if len(f.Edge.Routes) == 0 {
			f.Edge = nil
		}
		return fmt.Sprintf("%s no longer routes %s at the edge", key, path), nil
	}
}
