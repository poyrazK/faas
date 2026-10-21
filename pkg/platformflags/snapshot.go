package platformflags

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/flags"
)

// bucketSeed is the hash domain for platform-flag rollouts. Every flag shares
// it; the flag key is part of the hashed input, so cohorts are independent per
// flag and stable for the life of the key.
const bucketSeed = "gregale-platform-flags-v1"

// Decision reasons. They are part of the inspector and audit vocabulary.
const (
	ReasonUnknownFlag    = "unknown_flag"
	ReasonFlagDisabled   = "flag_disabled"
	ReasonAccountMissing = "account_missing"
	ReasonRuleMatch      = "rule_match"
	ReasonDefault        = "default"

	// ScheduleNotStarted and ScheduleEnded explain a rule that matched the
	// subject but was outside its window (ADR 20261021080640).
	ScheduleNotStarted = "rule_not_started"
	ScheduleEnded      = "rule_ended"
)

// Subject is the account a decision is made for. Plan may be empty when the
// caller does not know it; a rule that constrains plans then does not match.
// OrgID is the organization the request acts under (ADR 20261021080640), empty
// outside a company context; a rule that constrains orgs then does not match.
// At is the decision time; zero means now. Only the inspector and tests set it.
type Subject struct {
	AccountID string
	Plan      string
	OrgID     string
	At        time.Time
}

// Decision explains one evaluation. ScheduledRuleID and ScheduleState name
// the first rule that matched the subject but was outside its window, when
// that changed the outcome's rule.
type Decision struct {
	Flag            string `json:"flag"`
	Enabled         bool   `json:"enabled"`
	Reason          string `json:"reason"`
	RuleID          string `json:"rule_id,omitempty"`
	Bucket          *int   `json:"bucket,omitempty"`
	Version         int64  `json:"version"`
	ScheduledRuleID string `json:"scheduled_rule_id,omitempty"`
	ScheduleState   string `json:"schedule_state,omitempty"`
}

type compiledRule struct {
	id       string
	accounts map[string]struct{}
	orgs     map[string]struct{}
	group    string
	plans    map[string]struct{}
	rollout  int // -1 when the rule has no rollout constraint
	value    bool
	starts   *time.Time
	ends     *time.Time
}

// windowState is "" inside the window, else why the rule is inactive at now.
func (r compiledRule) windowState(now time.Time) string {
	switch {
	case r.starts != nil && now.Before(*r.starts):
		return ScheduleNotStarted
	case r.ends != nil && !now.Before(*r.ends):
		return ScheduleEnded
	}
	return ""
}

type compiledFlag struct {
	key     string
	def     Definition
	rules   []compiledRule
	version int64
}

// Snapshot is an immutable, compiled view of every platform flag and the
// shared groups. Evaluation performs no I/O and no locking.
type Snapshot struct {
	flags         map[string]compiledFlag
	groups        map[string]map[string]struct{}
	rawGroups     Groups
	groupsVersion int64
}

// Compile builds a snapshot. A rule that names a group missing from groups
// never matches: the evaluator fails closed on a dangling reference instead
// of widening the audience.
func Compile(defs map[string]Definition, versions map[string]int64, groups Groups, groupsVersion int64) *Snapshot {
	s := &Snapshot{
		flags:         make(map[string]compiledFlag, len(defs)),
		groups:        make(map[string]map[string]struct{}, len(groups)),
		rawGroups:     maps.Clone(groups),
		groupsVersion: groupsVersion,
	}
	for name, accounts := range groups {
		s.groups[name] = toSet(accounts)
	}
	for key, def := range defs {
		flag := compiledFlag{key: key, def: def, version: versions[key]}
		for _, rule := range def.Rules {
			compiled := compiledRule{id: rule.ID, accounts: toSet(rule.Accounts), orgs: toSet(rule.Orgs), group: rule.Group, plans: toSet(rule.Plans), rollout: -1}
			if rule.Rollout != nil {
				compiled.rollout = *rule.Rollout
			}
			if rule.Value != nil {
				compiled.value = *rule.Value
			}
			compiled.starts, compiled.ends = rule.StartsAt, rule.EndsAt
			flag.rules = append(flag.rules, compiled)
		}
		s.flags[key] = flag
	}
	return s
}

func toSet(values []string) map[string]struct{} {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(values))
	for _, value := range values {
		out[value] = struct{}{}
	}
	return out
}

// Evaluate decides a flag for one account. An unknown flag is off.
func (s *Snapshot) Evaluate(key string, subject Subject) Decision {
	d := Decision{Flag: key, Reason: ReasonUnknownFlag}
	if s == nil {
		return d
	}
	flag, ok := s.flags[key]
	if !ok {
		return d
	}
	d.Version = flag.version
	if !flag.def.Enabled {
		d.Reason = ReasonFlagDisabled
		return d
	}
	account := canonicalAccount(subject.AccountID)
	if account == "" {
		d.Reason = ReasonAccountMissing
		return d
	}
	org := canonicalAccount(subject.OrgID)
	now := subject.At
	if now.IsZero() {
		now = time.Now()
	}
	for _, rule := range flag.rules {
		if rule.accounts != nil {
			if _, ok := rule.accounts[account]; !ok {
				continue
			}
		}
		if rule.orgs != nil {
			if _, ok := rule.orgs[org]; !ok || org == "" {
				continue
			}
		}
		if rule.group != "" {
			members, ok := s.groups[rule.group]
			if !ok {
				continue
			}
			if _, ok := members[account]; !ok {
				continue
			}
		}
		if rule.plans != nil {
			if _, ok := rule.plans[subject.Plan]; !ok {
				continue
			}
		}
		var bucket *int
		if rule.rollout >= 0 {
			b := flags.Bucket(bucketSeed, key, account)
			if b >= rule.rollout {
				continue
			}
			bucket = &b
		}
		if state := rule.windowState(now); state != "" {
			if d.ScheduledRuleID == "" {
				d.ScheduledRuleID, d.ScheduleState = rule.id, state
			}
			continue
		}
		d.Enabled, d.Reason, d.RuleID, d.Bucket = rule.value, ReasonRuleMatch, rule.id, bucket
		return d
	}
	d.Enabled, d.Reason = flag.def.Default, ReasonDefault
	return d
}

// canonicalAccount matches an account ID against stored rules, which hold
// canonical lowercase hyphenated UUIDs: a UUID in any accepted spelling is
// canonicalized, anything else is used as given (and so matches no rule).
func canonicalAccount(accountID string) string {
	accountID = strings.TrimSpace(accountID)
	if id, err := uuid.Parse(accountID); err == nil {
		return id.String()
	}
	return accountID
}

// Enabled reports the decision value only.
func (s *Snapshot) Enabled(key string, subject Subject) bool {
	return s.Evaluate(key, subject).Enabled
}

// GloballyEnabled reports whether the kill switch is on. Callers use it to
// tell "the feature is off on this platform" from "not enabled for you".
func (s *Snapshot) GloballyEnabled(key string) bool {
	if s == nil {
		return false
	}
	flag, ok := s.flags[key]
	return ok && flag.def.Enabled
}

// Definition returns the definition and version a flag was compiled from.
func (s *Snapshot) Definition(key string) (Definition, int64, bool) {
	if s == nil {
		return Definition{}, 0, false
	}
	flag, ok := s.flags[key]
	return flag.def, flag.version, ok
}

// Groups returns a copy of the shared groups and their version.
func (s *Snapshot) Groups() (Groups, int64) {
	if s == nil {
		return Groups{}, 0
	}
	out := make(Groups, len(s.rawGroups))
	for name, accounts := range s.rawGroups {
		out[name] = append([]string(nil), accounts...)
	}
	return out, s.groupsVersion
}

// Set is the process-local holder daemons share. Apply is called by the
// runtime-config watcher (or apid's manager) for every acknowledged row;
// readers call Snapshot or Enabled on hot paths without locking.
type Set struct {
	current atomic.Pointer[Snapshot]

	mu            sync.Mutex
	defs          map[string]Definition
	versions      map[string]int64
	groups        Groups
	groupsVersion int64
}

// NewSet seeds the holder with boot-time definitions (version 0). Keys absent
// from bootstrap use their catalog default. A durable value of any positive
// version replaces the bootstrap value and is never replaced by it again.
func NewSet(bootstrap map[string]Definition) *Set {
	s := &Set{defs: map[string]Definition{}, versions: map[string]int64{}, groups: Groups{}}
	for _, spec := range Catalog() {
		s.defs[spec.Key] = spec.Default
	}
	for key, def := range bootstrap {
		if _, ok := Lookup(key); ok {
			s.defs[key] = NormalizeDefinition(def)
		}
	}
	s.publishLocked()
	return s
}

func (s *Set) publishLocked() {
	s.current.Store(Compile(maps.Clone(s.defs), maps.Clone(s.versions), s.groups, s.groupsVersion))
}

// Snapshot returns the current compiled view. A nil Set yields an empty
// snapshot in which every flag is off.
func (s *Set) Snapshot() *Snapshot {
	if s == nil {
		return nil
	}
	return s.current.Load()
}

// Enabled evaluates against the current snapshot.
func (s *Set) Enabled(key string, subject Subject) bool {
	return s.Snapshot().Enabled(key, subject)
}

// Evaluate evaluates against the current snapshot.
func (s *Set) Evaluate(key string, subject Subject) Decision {
	return s.Snapshot().Evaluate(key, subject)
}

// ConfigKeys lists the runtime-configuration keys a Set consumes: one per
// catalog flag plus the shared groups row.
func ConfigKeys() []string {
	specs := Catalog()
	keys := make([]string, 0, len(specs)+1)
	for _, spec := range specs {
		keys = append(keys, ConfigKey(spec.Key))
	}
	return append(keys, GroupsConfigKey)
}

// Apply installs one acknowledged runtime-configuration row. It ignores keys
// that are not platform-flag keys, refuses malformed values, and drops a
// version older than the one already installed so a replayed or reordered
// notification cannot roll a flag back. Version 0 is the bootstrap value and
// never replaces a durable one.
func (s *Set) Apply(configKey string, value json.RawMessage, version int64) error {
	if s == nil {
		return nil
	}
	if configKey == GroupsConfigKey {
		groups, err := ParseGroups(value)
		if err != nil {
			return err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if version < s.groupsVersion || version == 0 && s.groupsVersion > 0 {
			return nil
		}
		s.groups, s.groupsVersion = groups, version
		s.publishLocked()
		return nil
	}
	key, ok := FlagKeyFromConfigKey(configKey)
	if !ok {
		return nil
	}
	if _, known := Lookup(key); !known {
		return fmt.Errorf("unknown platform flag %q", key)
	}
	def, err := ParseDefinition(value)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.versions[key]
	if version < current || version == 0 && current > 0 {
		return nil
	}
	s.defs[key], s.versions[key] = def, version
	s.publishLocked()
	return nil
}
