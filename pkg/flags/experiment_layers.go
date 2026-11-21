package flags

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// Layers, the environment holdout and the experiment a rule carries
// (ADR 20261119173702 §2).

// ReasonHoldout marks a decision that served an experiment's control to a
// held-out customer.
const ReasonHoldout = "holdout"

// Experiment units (ADR 20261119173702 §1).
const (
	ExperimentUnitCustomer = "customer"
	ExperimentUnitSubject  = "subject"
)

// Holdout holds BasisPoints of the environment's customers out of every
// experiment: they see each experiment's control.
type Holdout struct {
	Seed        string `json:"seed"`
	BasisPoints int    `json:"basis_points"`
}

// Layer is a set of mutually exclusive experiments: each takes a slice of
// the layer's buckets.
type Layer struct {
	Key  string `json:"key"`
	Seed string `json:"seed"`
}

// RuleExperiment is the running experiment of a weighted rule. apid sets it
// when the experiment starts and removes it when the experiment ends.
type RuleExperiment struct {
	Key     string `json:"key"`
	Control string `json:"control"`
	// Unit is customer or subject; subject needs a subject-allocated rule.
	Unit string `json:"unit"`
	// Layer, From and To restrict the rule to the units whose layer bucket
	// is in [From, To).
	Layer string `json:"layer,omitempty"`
	From  int    `json:"from,omitempty"`
	To    int    `json:"to,omitempty"`
}

func bucketOf(parts ...string) int {
	h := sha256.New()
	for i, p := range parts {
		if i > 0 {
			h.Write([]byte{0})
		}
		h.Write([]byte(p))
	}
	sum := h.Sum(nil)
	return int(binary.BigEndian.Uint32(sum[:4]) % 10000)
}

// LayerBucket is SHA-256(seed NUL "layer" NUL customer [NUL subject]),
// first four bytes big-endian, modulo 10000. Subject is empty for customer
// units.
func LayerBucket(seed, customer, subject string) int {
	if subject == "" {
		return bucketOf(seed, "layer", customer)
	}
	return bucketOf(seed, "layer", customer, subject)
}

// HoldoutBucket is SHA-256(seed NUL "holdout" NUL customer), first four
// bytes big-endian, modulo 10000. The holdout is always per customer.
func HoldoutBucket(seed, customer string) int {
	return bucketOf(seed, "holdout", customer)
}

// Held reports whether the environment holdout holds the customer out.
func (h *Holdout) Held(customer string) bool {
	return h != nil && h.BasisPoints > 0 && HoldoutBucket(h.Seed, customer) < h.BasisPoints
}

// experimentGate applies a rule's experiment: whether the unit is in the
// rule's layer slice, and whether the holdout holds it out.
func experimentGate(c Config, x RuleExperiment, customer, subject string) (inSlice, held bool) {
	if x.Layer != "" {
		i := slices.IndexFunc(c.Layers, func(l Layer) bool { return l.Key == x.Layer })
		if i < 0 {
			return false, false
		}
		unitSubject := ""
		if x.Unit == ExperimentUnitSubject {
			unitSubject = subject
		}
		if b := LayerBucket(c.Layers[i].Seed, customer, unitSubject); b < x.From || b >= x.To {
			return false, false
		}
	}
	return true, c.Holdout.Held(customer)
}

// validateExperiments checks layers, the holdout and every rule experiment,
// including that experiments in one layer never share a bucket.
func validateExperiments(c Config) error {
	if len(c.Layers) > api.FlagLayersMaxPerEnvironment {
		return fmt.Errorf("at most %d experiment layers per environment", api.FlagLayersMaxPerEnvironment)
	}
	seen := map[string]bool{}
	for _, l := range c.Layers {
		if !ValidKey(l.Key) || seen[l.Key] || l.Seed == "" || len(l.Seed) > 128 {
			return fmt.Errorf("experiment layer %q needs a unique key and a seed", l.Key)
		}
		seen[l.Key] = true
	}
	if h := c.Holdout; h != nil && (h.BasisPoints < 0 || h.BasisPoints > api.FlagHoldoutMaxBasisPoints || h.Seed == "" || len(h.Seed) > 128) {
		return fmt.Errorf("the holdout needs a seed and 0..%d basis points", api.FlagHoldoutMaxBasisPoints)
	}
	type slice struct {
		from, to int
		key      string
	}
	slices := map[string][]slice{}
	keys := map[string]bool{}
	for _, f := range c.Flags {
		for _, r := range f.Rules {
			x := r.Experiment
			if x == nil {
				continue
			}
			if err := validateRuleExperiment(f, r, *x, seen); err != nil {
				return err
			}
			if keys[x.Key] {
				return fmt.Errorf("experiment %q is on more than one rule", x.Key)
			}
			keys[x.Key] = true
			if x.Layer == "" {
				continue
			}
			for _, s := range slices[x.Layer] {
				if x.From < s.to && s.from < x.To {
					return fmt.Errorf("experiments %q and %q share buckets of layer %q", s.key, x.Key, x.Layer)
				}
			}
			slices[x.Layer] = append(slices[x.Layer], slice{x.From, x.To, x.Key})
		}
	}
	return nil
}

func validateRuleExperiment(f Flag, r Rule, x RuleExperiment, layers map[string]bool) error {
	switch {
	case flagType(f) != "variant" || r.Value != nil:
		return fmt.Errorf("rule %q of %q: an experiment needs a weighted rule of a variant flag", r.ID, f.Key)
	case !ValidExperimentKey(x.Key):
		return fmt.Errorf("rule %q of %q: invalid experiment key", r.ID, f.Key)
	case !slices.ContainsFunc(f.Variants, func(v FlagVariant) bool { return v.Key == x.Control }):
		return fmt.Errorf("rule %q of %q: experiment control %q is not a variant", r.ID, f.Key, x.Control)
	case x.Unit != ExperimentUnitCustomer && x.Unit != ExperimentUnitSubject:
		return fmt.Errorf("rule %q of %q: experiment unit is customer or subject", r.ID, f.Key)
	case (x.Unit == ExperimentUnitSubject) != (r.RolloutUnit == "subject"):
		return fmt.Errorf("rule %q of %q: a subject experiment needs rollout_unit subject, a customer experiment a customer rule", r.ID, f.Key)
	case x.Layer == "" && (x.From != 0 || x.To != 0):
		return fmt.Errorf("rule %q of %q: a layer slice needs a layer", r.ID, f.Key)
	case x.Layer != "" && (!layers[x.Layer] || x.From < 0 || x.From >= x.To || x.To > 10000):
		return fmt.Errorf("rule %q of %q: experiment layer %q needs an existing layer and a slice 0 <= from < to <= 10000", r.ID, f.Key, x.Layer)
	}
	return nil
}

// SetRuleExperiment returns cfg with the rule's experiment set (or removed
// when x is nil); false when the flag or rule does not exist.
func SetRuleExperiment(cfg Config, flagKey, ruleID string, x *RuleExperiment) (Config, bool) {
	out := cloneConfigRules(cfg)
	for i, f := range out.Flags {
		if f.Key != flagKey {
			continue
		}
		for j, r := range f.Rules {
			if r.ID == ruleID {
				out.Flags[i].Rules[j].Experiment = x
				return out, true
			}
		}
	}
	return cfg, false
}

// SetVariantWeights returns cfg with a flag's variant weights replaced;
// false when the flag does not exist or the weights do not cover its
// variants or sum to 10000.
func SetVariantWeights(cfg Config, flagKey string, weights map[string]int) (Config, bool) {
	out := cloneConfigRules(cfg)
	for i, f := range out.Flags {
		if f.Key != flagKey {
			continue
		}
		total := 0
		variants := slices.Clone(f.Variants)
		for k, v := range variants {
			w, ok := weights[v.Key]
			if !ok || w < 0 {
				return cfg, false
			}
			variants[k].Weight = w
			total += w
		}
		if total != 10000 || len(weights) != len(variants) {
			return cfg, false
		}
		out.Flags[i].Variants = variants
		return out, true
	}
	return cfg, false
}

// WeightedRules counts a flag's rules that allocate by variant weight.
func WeightedRules(f Flag) int {
	n := 0
	for _, r := range f.Rules {
		if r.Value == nil {
			n++
		}
	}
	return n
}

// LayerSlice finds the lowest free contiguous slice of traffic basis points
// in a layer whose taken slices are given as [from, to) pairs.
func LayerSlice(taken [][2]int, traffic int) (from, to int, ok bool) {
	if traffic <= 0 || traffic > 10000 {
		return 0, 0, false
	}
	sorted := slices.Clone(taken)
	slices.SortFunc(sorted, func(a, b [2]int) int { return a[0] - b[0] })
	at := 0
	for _, s := range sorted {
		if s[0]-at >= traffic {
			return at, at + traffic, true
		}
		at = max(at, s[1])
	}
	if 10000-at >= traffic {
		return at, at + traffic, true
	}
	return 0, 0, false
}

// LayerSlices lists the slices taken in a layer by rule experiments.
func LayerSlices(cfg Config, layer string) [][2]int {
	var out [][2]int
	for _, f := range cfg.Flags {
		for _, r := range f.Rules {
			if x := r.Experiment; x != nil && x.Layer == layer {
				out = append(out, [2]int{x.From, x.To})
			}
		}
	}
	return out
}

// HasRuleExperiments reports whether any rule carries an experiment.
func HasRuleExperiments(cfg Config) bool {
	for _, f := range cfg.Flags {
		for _, r := range f.Rules {
			if r.Experiment != nil {
				return true
			}
		}
	}
	return false
}

// cloneConfigRules copies the flags and their rules and variants, so the
// caller can change one rule without touching cfg.
func cloneConfigRules(cfg Config) Config {
	out := cfg
	out.Flags = slices.Clone(cfg.Flags)
	for i := range out.Flags {
		out.Flags[i].Rules = slices.Clone(cfg.Flags[i].Rules)
		out.Flags[i].Variants = slices.Clone(cfg.Flags[i].Variants)
	}
	out.Layers = slices.Clone(cfg.Layers)
	return out
}
