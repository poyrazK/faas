// Package chaos contains the bounded fault plans used by isolated Gregale
// scenario tests. Faults apply at managed HTTP and TCP proxies, never to a
// host network or an unrelated workload.
package chaos

import (
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"regexp"
	"time"
)

const (
	MaxRules              = 16
	MinDuration           = time.Second
	MaxDuration           = 5 * time.Minute
	MaxLatency            = 30 * time.Second
	MinHTTPStatus         = 500
	MaxHTTPStatus         = 599
	KindLatency           = "latency"
	KindHTTPStatus        = "http_status"
	KindTCPLatency        = "tcp_latency"
	KindTCPBandwidth      = "tcp_bandwidth"
	KindTCPTimeout        = "tcp_timeout"
	KindTCPReset          = "tcp_reset"
	KindTCPConnectTimeout = "tcp_connect_timeout"
	KindTCPConnectRefused = "tcp_connect_refused"
	DirectionUpstream     = "upstream"
	DirectionDownstream   = "downstream"
	DirectionBoth         = "both"
)

var workloadNamePattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{1,38})[a-z0-9]$`)

// Rule targets one declared service call in an isolated scenario run.
// From is empty to match calls from any workload in that run.
type Rule struct {
	From             string `json:"from,omitempty"`
	To               string `json:"to"`
	Kind             string `json:"kind"`
	Percent          int    `json:"percent"`
	LatencyMS        int64  `json:"latency_ms,omitempty"`
	StatusCode       int    `json:"status_code,omitempty"`
	Seed             uint64 `json:"seed"`
	Port             int    `json:"port,omitempty"`
	Direction        string `json:"direction,omitempty"`
	RateKiBPerSecond int64  `json:"rate_kib_per_second,omitempty"`
	ResetAfterMS     int64  `json:"reset_after_ms,omitempty"`
}

// Plan is installed as a short-lived lease on one scenario test run.
type Plan struct {
	DurationMS int64  `json:"duration_ms"`
	Rules      []Rule `json:"rules"`
}

// Lease is the active, run-scoped form read by the service proxy.
type Lease struct {
	CallerWorkload string    `json:"-"`
	Generation     string    `json:"-"`
	Rules          []Rule    `json:"rules"`
	ExpiresAt      time.Time `json:"expires_at"`
}

// Validate checks the resource and duration bounds shared by the CLI and API.
func (p Plan) Validate() error {
	if p.DurationMS < MinDuration.Milliseconds() || p.DurationMS > MaxDuration.Milliseconds() {
		return fmt.Errorf("duration_ms must be between %d and %d", MinDuration.Milliseconds(), MaxDuration.Milliseconds())
	}
	if len(p.Rules) == 0 || len(p.Rules) > MaxRules {
		return fmt.Errorf("rules must contain between 1 and %d entries", MaxRules)
	}
	seen := make(map[string]struct{}, len(p.Rules))
	validatedTCP := make([]Rule, 0, len(p.Rules))
	for i, rule := range p.Rules {
		if !validWorkloadName(rule.To) {
			return fmt.Errorf("rule %d has an invalid target workload", i+1)
		}
		if rule.From != "" && (!validWorkloadName(rule.From) || rule.From == rule.To) {
			return fmt.Errorf("rule %d has an invalid source workload", i+1)
		}
		if rule.Percent < 1 || rule.Percent > 100 {
			return fmt.Errorf("rule %d percent must be between 1 and 100", i+1)
		}
		key := fmt.Sprintf("%s\x00%s\x00%s\x00%d\x00%s", rule.From, rule.To, rule.Kind, rule.Port, rule.EffectiveDirection())
		if _, ok := seen[key]; ok {
			return fmt.Errorf("rule %d duplicates a source, target, and kind", i+1)
		}
		seen[key] = struct{}{}
		if rule.IsTCP() {
			if err := validateTCPRule(rule); err != nil {
				return fmt.Errorf("rule %d: %w", i+1, err)
			}
			for _, previous := range validatedTCP {
				sameTargetPort := rule.To == previous.To && rule.Port == previous.Port
				sameCaller := rule.From == "" || previous.From == "" || rule.From == previous.From
				if sameTargetPort && sameCaller && (rule.IsTCPConnectFault() || previous.IsTCPConnectFault()) {
					return fmt.Errorf("rule %d conflicts with %s for an overlapping TCP connection", i+1, previous.Kind)
				}
			}
			validatedTCP = append(validatedTCP, rule)
			continue
		}
		if rule.Port != 0 || rule.Direction != "" || rule.RateKiBPerSecond != 0 || rule.ResetAfterMS != 0 {
			return fmt.Errorf("rule %d TCP fields require a TCP fault kind", i+1)
		}
		switch rule.Kind {
		case KindLatency:
			if rule.LatencyMS < 1 || rule.LatencyMS > MaxLatency.Milliseconds() || rule.StatusCode != 0 {
				return fmt.Errorf("rule %d latency_ms must be between 1 and %d and status_code must be omitted", i+1, MaxLatency.Milliseconds())
			}
		case KindHTTPStatus:
			if rule.StatusCode < MinHTTPStatus || rule.StatusCode > MaxHTTPStatus || rule.LatencyMS != 0 {
				return fmt.Errorf("rule %d status_code must be between %d and %d and latency_ms must be omitted", i+1, MinHTTPStatus, MaxHTTPStatus)
			}
		default:
			return fmt.Errorf("rule %d has unsupported fault kind %q", i+1, rule.Kind)
		}
	}
	return nil
}

// ValidateWorkloads also verifies every rule points only at members of the
// registered scenario namespace.
func (p Plan) ValidateWorkloads(workloads map[string]struct{}) error {
	if err := p.Validate(); err != nil {
		return err
	}
	for i, rule := range p.Rules {
		if _, ok := workloads[rule.To]; !ok {
			return fmt.Errorf("rule %d target %q is not a workload in this run", i+1, rule.To)
		}
		if rule.From != "" {
			if _, ok := workloads[rule.From]; !ok {
				return fmt.Errorf("rule %d source %q is not a workload in this run", i+1, rule.From)
			}
		}
	}
	return nil
}

// Select uses the user seed and request ordinal. A trace ID, when present,
// keeps the decision stable when a proxy retries or crosses nodes.
func Select(rule Rule, ordinal uint64, traceID string) bool {
	if rule.Percent >= 100 {
		return true
	}
	if rule.Percent <= 0 {
		return false
	}
	key := fmt.Sprintf("%d\x00%d", rule.Seed, ordinal)
	if traceID != "" {
		key = fmt.Sprintf("%d\x00trace\x00%s", rule.Seed, traceID)
	}
	sum := sha256.Sum256([]byte(key))
	return binary.BigEndian.Uint64(sum[:8])%100 < uint64(rule.Percent)
}

func validWorkloadName(name string) bool {
	return workloadNamePattern.MatchString(name)
}

// ValidateRule is convenient for API handlers that need a single stable
// problem response while retaining the detailed validation text.
func ValidateRule(rule Rule) error {
	return (Plan{DurationMS: MinDuration.Milliseconds(), Rules: []Rule{rule}}).Validate()
}
