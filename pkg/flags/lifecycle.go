package flags

// Flag lifecycle (ADR 20261114150000 §4): who owns a flag, when it should go,
// and whether it still does anything. Pure functions over configuration
// history; the evidence counts come from the caller.

import (
	"encoding/json"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// Lifecycle attention reasons.
const (
	AttentionExpired        = "expired"
	AttentionExpiresSoon    = "expires_soon"
	AttentionStale          = "stale"
	AttentionNeverEvaluated = "never_evaluated"
	AttentionNoOwner        = "no_owner"
)

// Lifecycle thresholds.
const (
	LifecycleExpiresSoon    = 7 * 24 * time.Hour
	LifecycleStaleAfter     = 14 * 24 * time.Hour
	LifecycleEvidenceWindow = 7 * 24 * time.Hour
	LifecycleHistoryLimit   = 200
	expiresAtLayout         = "2006-01-02"
)

// HistoryVersion is one published configuration, newest first in a slice.
type HistoryVersion struct {
	Version   int64
	CreatedAt time.Time
	Config    Config
}

// FlagLifecycle is the lifecycle report for one flag.
type FlagLifecycle struct {
	Key                string   `json:"key"`
	Owner              string   `json:"owner,omitempty"`
	ExpiresAt          string   `json:"expires_at,omitempty"`
	LastChangedVersion int64    `json:"last_changed_version"`
	LastChangedAt      string   `json:"last_changed_at"`
	ServesOneValue     bool     `json:"serves_one_value"`
	EvaluatedRequests  *int64   `json:"evaluated_requests,omitempty"`
	Evidence           string   `json:"evidence"`
	Attention          []string `json:"attention"`
}

// ValidOwner accepts up to 128 bytes of printable UTF-8 without control
// characters: a team, a person or an email.
func ValidOwner(owner string) bool {
	if len(owner) > api.FlagsMaxOwnerBytes || !utf8.ValidString(owner) {
		return false
	}
	for _, r := range owner {
		if r < 0x20 || r == 0x7f {
			return false
		}
	}
	return true
}

// ParseExpiresAt reads a YYYY-MM-DD expiry date.
func ParseExpiresAt(value string) (time.Time, error) {
	t, err := time.Parse(expiresAtLayout, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("expires_at must be a date like 2027-01-31, not %q", value)
	}
	return t, nil
}

// ServesOneValue reports a flag that gives every request the same value: it
// is disabled or killed, has no rules, or its first rule matches everybody.
func ServesOneValue(f Flag) bool {
	if !f.Enabled || f.Killed || len(f.Rules) == 0 {
		return true
	}
	r := f.Rules[0]
	untargeted := len(r.Customers) == 0 && r.Group == "" && len(r.Subjects) == 0 && len(r.Attributes) == 0
	return untargeted && (r.Rollout == nil || *r.Rollout >= 10000) && (flagType(f) == "boolean" || r.Value != nil)
}

// Lifecycle reports every flag of history[0]. history is newest first and
// at most LifecycleHistoryLimit long; evaluated maps a flag key to its
// request count in LifecycleEvidenceWindow, or is nil when evidence is
// unavailable.
func Lifecycle(history []HistoryVersion, evaluated map[string]int64, now time.Time) []FlagLifecycle {
	if len(history) == 0 {
		return []FlagLifecycle{}
	}
	out := make([]FlagLifecycle, 0, len(history[0].Config.Flags))
	for _, f := range history[0].Config.Flags {
		changed := lastChanged(history, f.Key)
		row := FlagLifecycle{
			Key: f.Key, Owner: f.Owner, ExpiresAt: f.ExpiresAt,
			LastChangedVersion: changed.Version, LastChangedAt: changed.CreatedAt.UTC().Format(time.RFC3339),
			ServesOneValue: ServesOneValue(f), Evidence: "unavailable", Attention: []string{},
		}
		if f.ExpiresAt != "" {
			if expires, err := ParseExpiresAt(f.ExpiresAt); err == nil {
				switch {
				case !now.Before(expires.Add(24 * time.Hour)):
					row.Attention = append(row.Attention, AttentionExpired)
				case expires.Sub(now) <= LifecycleExpiresSoon:
					row.Attention = append(row.Attention, AttentionExpiresSoon)
				}
			}
		}
		if row.ServesOneValue && now.Sub(changed.CreatedAt) >= LifecycleStaleAfter {
			row.Attention = append(row.Attention, AttentionStale)
		}
		if evaluated != nil {
			count := evaluated[f.Key]
			row.EvaluatedRequests, row.Evidence = &count, "available"
			if f.Enabled && !f.Killed && count == 0 && now.Sub(changed.CreatedAt) >= LifecycleEvidenceWindow {
				row.Attention = append(row.Attention, AttentionNeverEvaluated)
			}
		}
		if f.Owner == "" {
			row.Attention = append(row.Attention, AttentionNoOwner)
		}
		out = append(out, row)
	}
	return out
}

// lastChanged is the newest version whose definition of key differs from the
// version before it, or the oldest version available.
func lastChanged(history []HistoryVersion, key string) HistoryVersion {
	current, _ := flagJSON(history[0].Config, key)
	for i := 0; i+1 < len(history); i++ {
		older, found := flagJSON(history[i+1].Config, key)
		if !found || older != current {
			return history[i]
		}
	}
	return history[len(history)-1]
}

func flagJSON(c Config, key string) (string, bool) {
	for _, f := range c.Flags {
		if f.Key == key {
			raw, _ := json.Marshal(f)
			return string(raw), true
		}
	}
	return "", false
}
