// Package flags evaluates immutable tenant-scoped feature configuration.
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
	// Subjects are opaque application subject IDs, matched only after the
	// customer/group constraint has selected the tenant.
	Subjects []string `json:"subjects,omitempty"`
	// Attributes are conditions on gateway-verified (gregale.*) or
	// application-supplied (app.*) request attributes; all must hold
	// (ADR 20261114150000 §2).
	Attributes []Condition `json:"attributes,omitempty"`
	// Rollout is basis points (0..10000) of eligible customers, or subjects
	// when RolloutUnit is "subject". Nil is 100%.
	Rollout *int `json:"rollout,omitempty"`
	// RolloutUnit selects stable allocation per tenant subject instead of per
	// customer. Omitted preserves the historical customer allocation.
	RolloutUnit string `json:"rollout_unit,omitempty"`
	// Progression pins a boolean true rollout to one of a finite set of
	// percentage stages. Only the management API advances CurrentStage.
	Progression *ProgressiveRollout `json:"progression,omitempty"`
	// Value is a boolean for boolean flags, or a variant key for variant flags.
	// Variant rules may omit it to use the configured weighted allocation.
	Value any `json:"value,omitempty"`
	// Experiment is the running experiment of a weighted rule: its layer
	// slice and the environment holdout apply (ADR 20261119173702 §2).
	Experiment *RuleExperiment `json:"experiment,omitempty"`
}

// Condition matches one request attribute. A missing attribute never
// matches, whatever the operator.
type Condition struct {
	Attribute string   `json:"attribute"`
	Op        string   `json:"op"`
	Values    []string `json:"values"`
}

// Attribute names and operators (ADR 20261114150000 §2).
const (
	AttributeCountry  = "gregale.country"
	AttributeUAFamily = "gregale.ua_family"
	// AttributeCohort is the gateway-stamped cohorts of the request's
	// customer (ADR 20261119102741 §4).
	AttributeCohort    = "gregale.cohort"
	AppAttributePrefix = "app."
	// CohortsNone is the gregale.cohort value of a customer in no cohort.
	CohortsNone = "-"

	OpIn       = "in"
	OpNotIn    = "not_in"
	OpEndsWith = "ends_with"
)

var (
	appAttributeNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	countryPattern          = regexp.MustCompile(`^[A-Z]{2}$`)
)

// ValidAttributeName accepts the gateway attributes and app.<name>.
func ValidAttributeName(name string) bool {
	if name == AttributeCountry || name == AttributeUAFamily || name == AttributeCohort {
		return true
	}
	rest, ok := strings.CutPrefix(name, AppAttributePrefix)
	return ok && appAttributeNamePattern.MatchString(rest)
}

// ValidAppAttributeName accepts the <name> an application supplies.
func ValidAppAttributeName(name string) bool { return appAttributeNamePattern.MatchString(name) }

// ValidAttributeValue accepts printable ASCII up to FlagsMaxAttributeValueBytes.
// SDKs drop an invalid application value, so it reads as missing.
func ValidAttributeValue(value string) bool {
	if len(value) == 0 || len(value) > api.FlagsMaxAttributeValueBytes {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x20 || b > 0x7e {
			return false
		}
	}
	return true
}

// ParseCohorts reads a gregale.cohort attribute: the sorted, comma-separated
// cohort keys the gateway stamped, or CohortsNone for a customer in none.
// ok is false for a malformed value, which reads as missing.
func ParseCohorts(value string) (keys []string, ok bool) {
	if value == CohortsNone {
		return nil, true
	}
	if value == "" {
		return nil, false
	}
	keys = strings.Split(value, ",")
	if len(keys) > api.AnalyticsCohortsMaxPerEnvironment {
		return nil, false
	}
	for _, k := range keys {
		if !ValidKey(k) {
			return nil, false
		}
	}
	return keys, true
}

// FormatCohorts is the X-Faas-Flag-Cohorts value for keys: sorted and
// comma-separated, or CohortsNone.
func FormatCohorts(keys []string) string {
	if len(keys) == 0 {
		return CohortsNone
	}
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// matchCohorts applies a gregale.cohort condition: in matches a member of
// any listed cohort, not_in a customer in none of them.
func matchCohorts(c Condition, value string) bool {
	keys, ok := ParseCohorts(value)
	if !ok {
		return false
	}
	member := slices.ContainsFunc(c.Values, func(v string) bool { return slices.Contains(keys, v) })
	switch c.Op {
	case OpIn:
		return member
	case OpNotIn:
		return !member
	default:
		return false
	}
}

// MatchConditions reports whether every condition holds for attrs.
func MatchConditions(conditions []Condition, attrs map[string]string) bool {
	for _, c := range conditions {
		if c.Attribute == AttributeCohort {
			value, ok := attrs[c.Attribute]
			if !ok || !matchCohorts(c, value) {
				return false
			}
			continue
		}
		value, ok := attrs[c.Attribute]
		if !ok || !ValidAttributeValue(value) {
			return false
		}
		switch c.Op {
		case OpIn:
			if !slices.Contains(c.Values, value) {
				return false
			}
		case OpNotIn:
			if slices.Contains(c.Values, value) {
				return false
			}
		case OpEndsWith:
			if !slices.ContainsFunc(c.Values, func(suffix string) bool { return strings.HasSuffix(value, suffix) }) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// EvaluationContext is one request's identity and attributes. Customer is
// the platform-verified tenant; Subject and app.* attributes come from the
// application after it authenticated the user; gregale.* attributes come
// from the gateway.
type EvaluationContext struct {
	Customer   string
	Subject    string
	Attributes map[string]string
	// Kills are the flag keys the gateway reported killed for this request
	// (X-Faas-Flag-Kills). They apply even before the bundle has refreshed.
	Kills []string
}

// ReasonKilled marks a decision forced to the application fallback by a kill.
const ReasonKilled = "killed"

// ParseKills reads the gateway's comma-separated kill header, keeping valid
// flag keys only and bounding the list to FlagsMaxPerEnvironment.
func ParseKills(header string) []string {
	if header == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(header, ",") {
		if key := strings.TrimSpace(part); ValidKey(key) && !slices.Contains(out, key) {
			out = append(out, key)
			if len(out) == api.FlagsMaxPerEnvironment {
				break
			}
		}
	}
	return out
}

// FormatKills renders sorted keys for the X-Faas-Flag-Kills header.
func FormatKills(keys []string) string {
	sorted := slices.Clone(keys)
	slices.Sort(sorted)
	return strings.Join(slices.Compact(sorted), ",")
}

// KilledKeys lists the killed flags of a configuration, sorted.
func KilledKeys(c Config) []string {
	var out []string
	for _, f := range c.Flags {
		if f.Killed {
			out = append(out, f.Key)
		}
	}
	slices.Sort(out)
	return out
}

func killedDecision(b Bundle, key, typ string, fallback any) Decision {
	d := Decision{Flag: key, Value: fallback, ConfigVersion: b.Version, Reason: ReasonKilled, Source: "fallback"}
	if typ == "variant" {
		d.Type = "variant"
	}
	return d
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
	// Metric gates stages on a product metric (ADR 20261119173702 §5).
	Metric *RolloutMetric `json:"metric,omitempty"`
}
type Flag struct {
	Key         string `json:"key"`
	Description string `json:"description,omitempty"`
	// Type is omitted for legacy boolean flags. Variant flags use "variant".
	Type    string `json:"type,omitempty"`
	Enabled bool   `json:"enabled"`
	// Killed forces every evaluation to the application's fallback, whatever
	// the default, rules or inherited decisions say (ADR 20261114150000 §3).
	Killed bool `json:"killed,omitempty"`
	// Owner and ExpiresAt (YYYY-MM-DD) drive the lifecycle report
	// (ADR 20261114150000 §4); they never change evaluation.
	Owner     string        `json:"owner,omitempty"`
	ExpiresAt string        `json:"expires_at,omitempty"`
	Default   any           `json:"default"`
	Seed      string        `json:"seed"`
	Rules     []Rule        `json:"rules"`
	Variants  []FlagVariant `json:"variants,omitempty"`
	// Edge routes the flag's variants at the gateway, without an SDK
	// (ADR-20261124212701). Runtime bundles leave it out.
	Edge *Edge `json:"edge,omitempty"`
}
type FlagVariant struct {
	Key    string `json:"key"`
	Weight int    `json:"weight"`
}
type Config struct {
	Flags  []Flag              `json:"flags"`
	Groups map[string][]string `json:"groups"`
	// Holdout and Layers isolate experiments (ADR 20261119173702 §2).
	Holdout *Holdout `json:"holdout,omitempty"`
	Layers  []Layer  `json:"layers,omitempty"`
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

// SubjectBucket is a stable percentage-allocation bucket scoped to both the
// verified customer and an application-authenticated subject.
func SubjectBucket(seed, key, customer, subject string) int {
	h := sha256.Sum256([]byte(seed + "\x00" + key + "\x00subject\x00" + customer + "\x00" + subject))
	return int(binary.BigEndian.Uint32(h[:4]) % 10000)
}

// SubjectVariantBucket uses a separate domain for per-subject variant weights.
func SubjectVariantBucket(seed, key, customer, subject string) int {
	h := sha256.Sum256([]byte(seed + "\x00" + key + "\x00variant\x00subject\x00" + customer + "\x00" + subject))
	return int(binary.BigEndian.Uint32(h[:4]) % 10000)
}

// ValidSubjectID accepts bounded printable ASCII opaque IDs. Applications
// should pass an internal stable ID, never an email address or display name.
func ValidSubjectID(value string) bool {
	if len(value) == 0 || len(value) > api.FlagsMaxSubjectIDBytes {
		return false
	}
	for _, b := range []byte(value) {
		if b < 0x21 || b > 0x7e {
			return false
		}
	}
	return true
}

// Evaluate performs no I/O and requires customer identity supplied by trusted
// application middleware. Anonymous contexts never match targeting rules.
func Evaluate(b Bundle, key, customer string, fallback bool) Decision {
	return EvaluateForSubject(b, key, customer, "", fallback)
}

// EvaluateForSubject evaluates using an application-authenticated subject ID.
// Customer identity remains the platform-verified tenant boundary.
func EvaluateForSubject(b Bundle, key, customer, subject string, fallback bool) Decision {
	return EvaluateWithContext(b, key, EvaluationContext{Customer: customer, Subject: subject}, fallback)
}

// EvaluateWithContext evaluates a boolean flag for one request context.
func EvaluateWithContext(b Bundle, key string, c EvaluationContext, fallback bool) Decision {
	if slices.Contains(c.Kills, key) {
		return killedDecision(b, key, "boolean", fallback)
	}
	d := Decision{Flag: key, Value: fallback, ConfigVersion: b.Version, Reason: "flag_missing", Source: "fallback"}
	for _, f := range b.Flags {
		if f.Key != key {
			continue
		}
		if f.Killed {
			return killedDecision(b, key, "boolean", fallback)
		}
		if flagType(f) != "boolean" {
			d.Reason = "type_mismatch"
			return d
		}
		return evaluateFlag(b, f, c, fallback)
	}
	return d
}

// EvaluateVariant evaluates a named variant by customer, using fallback when
// the flag is missing or has a different type. Use EvaluateVariantForSubject
// for application-authenticated per-subject evaluation.
func EvaluateVariant(b Bundle, key, customer, fallback string) Decision {
	return EvaluateVariantForSubject(b, key, customer, "", fallback)
}

// EvaluateVariantForSubject evaluates a named variant for an authenticated
// application subject within the platform-verified customer.
func EvaluateVariantForSubject(b Bundle, key, customer, subject, fallback string) Decision {
	return EvaluateVariantWithContext(b, key, EvaluationContext{Customer: customer, Subject: subject}, fallback)
}

// EvaluateVariantWithContext evaluates a named variant for one request context.
func EvaluateVariantWithContext(b Bundle, key string, c EvaluationContext, fallback string) Decision {
	if slices.Contains(c.Kills, key) {
		return killedDecision(b, key, "variant", fallback)
	}
	d := Decision{Flag: key, Value: fallback, Type: "variant", ConfigVersion: b.Version, Reason: "flag_missing", Source: "fallback"}
	for _, f := range b.Flags {
		if f.Key != key {
			continue
		}
		if f.Killed {
			return killedDecision(b, key, "variant", fallback)
		}
		if flagType(f) != "variant" {
			d.Reason = "type_mismatch"
			return d
		}
		return evaluateFlag(b, f, c, fallback)
	}
	return d
}

func flagType(f Flag) string {
	if f.Type == "" || f.Type == "boolean" {
		return "boolean"
	}
	return f.Type
}

func evaluateFlag(b Bundle, f Flag, c EvaluationContext, fallback any) Decision {
	customer, subject := c.Customer, c.Subject
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
	if !ValidSubjectID(subject) {
		subject = ""
	}
	subjectMissing := false
	for _, r := range f.Rules {
		if len(r.Customers) > 0 && !slices.Contains(r.Customers, customer) {
			continue
		}
		if r.Group != "" && !slices.Contains(b.Groups[r.Group], customer) {
			continue
		}
		if len(r.Attributes) > 0 && !MatchConditions(r.Attributes, c.Attributes) {
			continue
		}
		subjectScoped := len(r.Subjects) > 0 || r.RolloutUnit == "subject"
		if subjectScoped && subject == "" {
			subjectMissing = true
			continue
		}
		if len(r.Subjects) > 0 && !slices.Contains(r.Subjects, subject) {
			continue
		}
		if r.Experiment != nil {
			inSlice, held := experimentGate(b.Config, *r.Experiment, customer, subject)
			if !inSlice {
				continue
			}
			if held {
				d.Value, d.RuleID, d.Reason = r.Experiment.Control, r.ID, ReasonHoldout
				return d
			}
		}
		if r.Rollout != nil {
			bucket := Bucket(f.Seed, f.Key, customer)
			if r.RolloutUnit == "subject" {
				bucket = SubjectBucket(f.Seed, f.Key, customer, subject)
			}
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
			if subjectScoped {
				bucket = SubjectVariantBucket(f.Seed, f.Key, customer, subject)
			}
			d.Bucket = &bucket
			d.Value = chooseVariant(f.Variants, bucket)
		}
		d.RuleID, d.Reason = r.ID, "rule_match"
		return d
	}
	if subjectMissing {
		d.Reason = "subject_missing"
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
		if !ValidOwner(f.Owner) {
			return fmt.Errorf("flag owner must be at most %d bytes of printable text", api.FlagsMaxOwnerBytes)
		}
		if f.ExpiresAt != "" {
			if _, err := ParseExpiresAt(f.ExpiresAt); err != nil {
				return err
			}
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
			if r.Subjects != nil && len(r.Subjects) == 0 {
				return fmt.Errorf("subject targeting requires at least one subject")
			}
			if err := validateCustomers(r.Customers); err != nil {
				return err
			}
			if err := validateSubjects(r.Subjects); err != nil {
				return err
			}
			if r.Attributes != nil && len(r.Attributes) == 0 {
				return fmt.Errorf("attribute targeting requires at least one condition")
			}
			if err := validateConditions(r.Attributes); err != nil {
				return err
			}
			if r.RolloutUnit != "" && r.RolloutUnit != "customer" && r.RolloutUnit != "subject" {
				return fmt.Errorf("invalid rollout_unit")
			}
			if r.RolloutUnit != "" && r.Rollout == nil {
				return fmt.Errorf("rollout_unit requires a rollout percentage")
			}
			if (len(r.Subjects) > 0 || r.RolloutUnit == "subject") && len(r.Customers) == 0 && r.Group == "" {
				return fmt.Errorf("subject targeting requires a customer or group constraint")
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
				if err := validateRolloutMetric(r); err != nil {
					return err
				}
			}
			if len(r.Customers) == 0 && r.Group == "" && len(r.Subjects) == 0 && len(r.Attributes) == 0 && r.Rollout == nil {
				return fmt.Errorf("rule requires customer, group, attribute, or rollout targeting")
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
	if err := validateEdge(c); err != nil {
		return err
	}
	return validateExperiments(c)
}

func validateConditions(conditions []Condition) error {
	if len(conditions) > api.FlagsMaxConditionsPerRule {
		return fmt.Errorf("a rule takes at most %d attribute conditions", api.FlagsMaxConditionsPerRule)
	}
	for _, c := range conditions {
		if !ValidAttributeName(c.Attribute) {
			return fmt.Errorf("invalid attribute %q: use gregale.country, gregale.ua_family, gregale.cohort or app.<name>", c.Attribute)
		}
		if c.Attribute == AttributeCohort && c.Op == OpEndsWith {
			return fmt.Errorf("gregale.cohort takes in or not_in")
		}
		switch c.Op {
		case OpIn, OpNotIn:
		case OpEndsWith:
			if !strings.HasPrefix(c.Attribute, AppAttributePrefix) {
				return fmt.Errorf("ends_with applies only to app.* attributes")
			}
		default:
			return fmt.Errorf("invalid attribute operator %q: use in, not_in or ends_with", c.Op)
		}
		if len(c.Values) == 0 || len(c.Values) > api.FlagsMaxConditionValues {
			return fmt.Errorf("an attribute condition takes 1..%d values", api.FlagsMaxConditionValues)
		}
		seen := map[string]bool{}
		for _, v := range c.Values {
			if !ValidAttributeValue(v) || seen[v] {
				return fmt.Errorf("invalid or duplicate attribute value %q", v)
			}
			if c.Attribute == AttributeCountry && !countryPattern.MatchString(v) {
				return fmt.Errorf("gregale.country values are two uppercase letters, not %q", v)
			}
			if c.Attribute == AttributeCohort && !ValidKey(v) {
				return fmt.Errorf("gregale.cohort values are cohort keys, not %q", v)
			}
			seen[v] = true
		}
	}
	return nil
}

func validateSubjects(ids []string) error {
	if len(ids) > api.FlagsMaxSubjects {
		return fmt.Errorf("subject limit exceeded")
	}
	seen := map[string]bool{}
	for _, id := range ids {
		if !ValidSubjectID(id) || seen[id] {
			return fmt.Errorf("invalid or duplicate subject identity")
		}
		seen[id] = true
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
