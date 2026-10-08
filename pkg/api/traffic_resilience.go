package api

import (
	"fmt"
	"strconv"
	"strings"
)

// EgressCircuitBreakerEnabled is shared by schedd and vmmd. An unset flag
// defaults off; false/0 stay off, and malformed values fail startup rather
// than silently enabling or disabling protection.
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
