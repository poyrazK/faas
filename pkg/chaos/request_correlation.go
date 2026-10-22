package chaos

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

const RequestCorrelationHeader = "X-Gregale-Test-Request-ID"
const MaxRequestCorrelationSlots = 1024

var correlationScopePattern = regexp.MustCompile(`^[0-9a-f]{16}$`)

func RequestCorrelationID(runID, scope string, slot int) string {
	return fmt.Sprintf("%s.%s.%d", runID, scope, slot)
}
func RequestCorrelationSlot(runID string, rule Rule, token string) (int, bool) {
	prefix := runID + "." + rule.CorrelationScope + "."
	if rule.CorrelationScope == "" || !strings.HasPrefix(token, prefix) {
		return 0, false
	}
	value := strings.TrimPrefix(token, prefix)
	slot, err := strconv.Atoi(value)
	return slot, err == nil && slot >= 1 && slot <= MaxRequestCorrelationSlots && strconv.Itoa(slot) == value
}
func CorrelationCounterID(rule Rule, slot int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("gregale:request-attempt:v1:%s:%d", RuleID(rule), slot)))
	return hex.EncodeToString(sum[:])
}

// Slot 0 counts uncorrelated calls; -1 identifies support/completeness evidence.
func UncorrelatedAttemptID(rule Rule) string { return CorrelationCounterID(rule, 0) }
func CorrelationSupportID(rule Rule) string  { return CorrelationCounterID(rule, -1) }
