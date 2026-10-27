package api

import (
	"fmt"
	"strconv"
	"strings"
)

// TrafficAuthoritativeHostPolicyEnabled parses gatewayd-internal's
// FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY (ADR-570). Authoritative mode reads
// host ownership and the compiled edge policy in a repeatable-read Postgres
// snapshot on every public request instead of the route/edge-rule caches, so
// it defaults off until fleet load evidence exists. Malformed values fail
// startup rather than silently picking a mode.
func TrafficAuthoritativeHostPolicyEnabled(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("FAAS_TRAFFIC_AUTHORITATIVE_HOST_POLICY must be a boolean")
	}
	return enabled, nil
}

// EgressCircuitBreakerEnabled is shared by schedd and vmmd. An unset flag
// defaults off; false/0 stay off, and malformed values fail startup rather
// than silently enabling or disabling protection.
// HTTPAdmissionEnabled parses vmmd's FAAS_HTTP_ADMISSION kill switch for the
// ADR-570 per-VM HTTP admission cap. Empty keeps the cap on; "off"/"false"
// lifts it while permits stay tracked for park and retirement cancellation.
func HTTPAdmissionEnabled(value string) (bool, error) {
	value = strings.TrimSpace(value)
	switch strings.ToLower(value) {
	case "":
		return true, nil
	case "on":
		return true, nil
	case "off":
		return false, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return true, fmt.Errorf("FAAS_HTTP_ADMISSION must be on, off or a boolean")
	}
	return enabled, nil
}

func EgressCircuitBreakerEnabled(value string) (bool, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return false, nil
	}
	enabled, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("FAAS_EGRESS_CIRCUIT_BREAKER must be a boolean")
	}
	return enabled, nil
}
