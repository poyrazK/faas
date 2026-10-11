package api

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// WorkflowRetryAfter parses the downstream delay without allowing an unbounded
// sleep or duration overflow. An invalid or expired value adds no delay.
func WorkflowRetryAfter(raw string, now time.Time) time.Time {
	raw = strings.TrimSpace(raw)
	ceiling := now.Add(WorkflowRetryAfterMaxDelay)
	if seconds, err := strconv.ParseUint(raw, 10, 64); err == nil {
		if seconds >= uint64(WorkflowRetryAfterMaxDelay/time.Second) {
			return ceiling
		}
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if at, err := http.ParseTime(raw); err == nil && at.After(now) {
		if at.After(ceiling) {
			return ceiling
		}
		return at
	}
	return time.Time{}
}
