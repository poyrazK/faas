package chaos

import (
	"fmt"
	"slices"

	"github.com/onebox-faas/faas/pkg/api"
)

// IsTCP separates stream faults from request-level HTTP faults.
func (r Rule) IsTCP() bool {
	return r.Kind == KindTCPLatency || r.Kind == KindTCPBandwidth ||
		r.Kind == KindTCPTimeout || r.Kind == KindTCPReset ||
		r.Kind == KindTCPConnectTimeout || r.Kind == KindTCPConnectRefused
}

func (r Rule) IsTCPConnectFault() bool {
	return r.Kind == KindTCPConnectTimeout || r.Kind == KindTCPConnectRefused
}

func (r Rule) EffectiveDirection() string {
	if r.Direction == "" {
		return DirectionBoth
	}
	return r.Direction
}

func validateTCPRule(r Rule) error {
	if r.Port < 1 || r.Port > 65535 {
		return fmt.Errorf("TCP port must be between 1 and 65535")
	}
	if slices.Contains(api.ServiceTCPReservedPorts(), r.Port) {
		return fmt.Errorf("TCP port %d is reserved for the HTTP mesh", r.Port)
	}
	if d := r.EffectiveDirection(); d != DirectionUpstream && d != DirectionDownstream && d != DirectionBoth {
		return fmt.Errorf("direction must be upstream, downstream, or both")
	}
	if r.StatusCode != 0 {
		return fmt.Errorf("status_code is only valid for http_status")
	}
	if r.Kind != KindTCPLatency && r.LatencyMS != 0 {
		return fmt.Errorf("latency_ms is only valid for latency faults")
	}
	if r.Kind != KindTCPBandwidth && r.RateKiBPerSecond != 0 {
		return fmt.Errorf("rate_kib_per_second is only valid for tcp_bandwidth")
	}
	if r.Kind != KindTCPReset && r.ResetAfterMS != 0 {
		return fmt.Errorf("reset_after_ms is only valid for tcp_reset")
	}
	switch r.Kind {
	case KindTCPLatency:
		if r.LatencyMS < 1 || r.LatencyMS > MaxLatency.Milliseconds() {
			return fmt.Errorf("latency_ms must be between 1 and %d", MaxLatency.Milliseconds())
		}
	case KindTCPBandwidth:
		// At least 1 KiB/s keeps a bounded stream chunk's wait finite.
		if r.RateKiBPerSecond < 1 || r.RateKiBPerSecond > api.ScenarioTCPChaosMaxRateKiBPerSecond {
			return fmt.Errorf("rate_kib_per_second must be between 1 and 1000000")
		}
	case KindTCPReset:
		if r.EffectiveDirection() != DirectionBoth || r.ResetAfterMS < 0 || r.ResetAfterMS > MaxLatency.Milliseconds() {
			return fmt.Errorf("tcp_reset requires direction both and reset_after_ms between 0 and %d", MaxLatency.Milliseconds())
		}
	case KindTCPConnectTimeout, KindTCPConnectRefused:
		if r.EffectiveDirection() != DirectionBoth {
			return fmt.Errorf("%s requires direction both", r.Kind)
		}
	}
	return nil
}
