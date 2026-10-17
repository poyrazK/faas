// Package configguard judges config changes (ADR-1451): whether the traffic
// served with a new config failed against the config before it, and which
// writes return a scope to an earlier revision.
package configguard

import (
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/canary"
	"github.com/onebox-faas/faas/pkg/state"
)

// Decision rules recorded on a closed guard.
const (
	RuleFirstWake5xx   = "fail_first_wake_5xx"
	Rule5xxRegression  = "fail_5xx_regression"
	RuleClean          = "clean"
	RuleNoTraffic      = "no_traffic"
	RuleChangedOutside = "changed_outside_guard"
)

// Decision is the explained verdict stored on a closed guard, in the shape of
// ADR-1447 rollout decisions: a rule, a one-line summary and both samples.
type Decision = api.ConfigGuardDecision

func pct(errors, requests int64) float64 {
	if requests == 0 {
		return 0
	}
	return 100 * float64(errors) / float64(requests)
}

func sampleDecision(rule, summary string, s state.ConfigGuardSample) Decision {
	return Decision{Rule: rule, Summary: summary, NewRequests: s.NewRequests, NewServerErrors: s.NewServerErrors,
		OldRequests: s.OldRequests, OldServerErrors: s.OldServerErrors}
}

// Judge reports whether the new config failed. It fails on the ADR-625
// first-wake bar (at least RollbackOn5xxMinServerErrors 5xx that are also at
// least RollbackOn5xxMinErrorPct of the new-config requests), unless the old
// config, measured on a full sample, failed as badly: a change meant to fix a
// broken app is never reverted to the broken config. With full samples on
// both sides it also fails on the canary's 5xx regression rule.
func Judge(s state.ConfigGuardSample) (Decision, bool) {
	newPct, oldPct := pct(s.NewServerErrors, s.NewRequests), pct(s.OldServerErrors, s.OldRequests)
	summary := fmt.Sprintf("5xx after the config change (new config %d/%d, %.1f%%; old config %d/%d, %.1f%%)",
		s.NewServerErrors, s.NewRequests, newPct, s.OldServerErrors, s.OldRequests, oldPct)
	firstWake := s.NewServerErrors >= api.RollbackOn5xxMinServerErrors &&
		s.NewServerErrors*100 >= s.NewRequests*api.RollbackOn5xxMinErrorPct
	oldComparable := s.OldRequests >= canary.CircuitBreakerMinRequests
	if firstWake && (!oldComparable || (newPct >= 3*oldPct && newPct >= oldPct+5)) {
		return sampleDecision(RuleFirstWake5xx, summary, s), true
	}
	candidate := canary.HealthWindow{Requests: s.NewRequests, ServerErrors: s.NewServerErrors}
	stable := canary.HealthWindow{Requests: s.OldRequests, ServerErrors: s.OldServerErrors}
	if _, failed := canary.ServerErrorRegression(candidate, stable); failed {
		return sampleDecision(Rule5xxRegression, summary, s), true
	}
	return sampleDecision(RuleClean, fmt.Sprintf("no 5xx regression (new config %d/%d; old config %d/%d)",
		s.NewServerErrors, s.NewRequests, s.OldServerErrors, s.OldRequests), s), false
}

// NoTraffic is the verdict for a guard that never saw new-config traffic.
func NoTraffic() Decision {
	return Decision{Rule: RuleNoTraffic, Summary: "no traffic was served with the new config before the guard expired"}
}

// VariableWrite sets or deletes one variable.
type VariableWrite struct {
	Key    string
	Value  string
	Delete bool
}

// VariablesToward returns the writes that make current equal target, sorted
// by key.
func VariablesToward(target, current map[string]string) []VariableWrite {
	var writes []VariableWrite
	for key, value := range target {
		if have, ok := current[key]; !ok || have != value {
			writes = append(writes, VariableWrite{Key: key, Value: value})
		}
	}
	for key := range current {
		if _, ok := target[key]; !ok {
			writes = append(writes, VariableWrite{Key: key, Delete: true})
		}
	}
	sort.Slice(writes, func(i, j int) bool { return writes[i].Key < writes[j].Key })
	return writes
}

// GuardVariableRestore returns the writes that undo a guard's own variable
// changes (base → head), or ok=false when a key the guard changed no longer
// holds the head value: something outside the guard changed it since.
func GuardVariableRestore(base, head, current map[string]string) ([]VariableWrite, bool) {
	var writes []VariableWrite
	for _, key := range changedKeys(base, head) {
		headValue, inHead := head[key]
		curValue, inCurrent := current[key]
		if inHead != inCurrent || headValue != curValue {
			return nil, false
		}
		if baseValue, inBase := base[key]; inBase {
			writes = append(writes, VariableWrite{Key: key, Value: baseValue})
		} else {
			writes = append(writes, VariableWrite{Key: key, Delete: true})
		}
	}
	return writes, true
}

// GuardSecretsUnchanged reports whether every secret the guard retained still
// holds the version the guard's head recorded.
func GuardSecretsUnchanged(retained []state.ConfigGuardSecret, head, current map[string]state.ConfigSecretFingerprint) bool {
	for _, secret := range retained {
		headFP, inHead := head[secret.Key]
		curFP, inCurrent := current[secret.Key]
		if inHead != inCurrent || headFP != curFP {
			return false
		}
	}
	return true
}

// SecretsDiffering lists the secrets whose version or value differ between
// two states, sorted. A config rollback cannot restore them.
func SecretsDiffering(target, current map[string]state.ConfigSecretFingerprint) []string {
	keys := map[string]bool{}
	for key, fp := range target {
		if current[key] != fp {
			keys[key] = true
		}
	}
	for key := range current {
		if _, ok := target[key]; !ok {
			keys[key] = true
		}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func changedKeys(a, b map[string]string) []string {
	keys := map[string]bool{}
	for key, value := range a {
		if other, ok := b[key]; !ok || other != value {
			keys[key] = true
		}
	}
	for key := range b {
		if _, ok := a[key]; !ok {
			keys[key] = true
		}
	}
	out := make([]string, 0, len(keys))
	for key := range keys {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
