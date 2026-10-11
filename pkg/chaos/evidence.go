package chaos

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// RuleID is a stable, opaque identifier for a rule within an installed plan.
// Plan validation prevents duplicate rules with the same match scope, while
// hashing the full rule also distinguishes different fault parameters.
func RuleID(rule Rule) string {
	raw, _ := json.Marshal(rule)
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// InjectionBatch records aggregated rule matches from one gateway process.
// Generation prevents events queued by a replaced plan from being counted by
// its successor.
type InjectionBatch struct {
	RunID      string
	CallerApp  string
	Generation string
	RuleID     string
	Count      int64
}

// RuleMatchCount is the number of requests or TCP connections that matched a
// rule in the current plan generation.
type RuleMatchCount struct {
	RuleID string `json:"rule_id"`
	Count  int64  `json:"count"`
}

// MatchEvidence identifies the plan generation that produced its counts.
type MatchEvidence struct {
	Generation string           `json:"generation,omitempty"`
	Matches    []RuleMatchCount `json:"matches"`
}
