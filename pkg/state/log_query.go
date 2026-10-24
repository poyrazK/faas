package state

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
)

// LogQueryKeys are the ledger filters a log search, a saved search and a
// log_search_matches alert share (ADR-20261018083536). Windows, cursors and
// page sizes belong to the caller, never to a stored query.
var LogQueryKeys = []string{"source", "level", "status", "route", "deployment_id", "request_id", "trace_id", "q"}

// ParseLogEventQuery reads the shared ledger filters through get, which
// returns a trimmed query value or "". The returned filter carries no tenant
// or window.
func ParseLogEventQuery(get func(string) string) (LogEventFilter, error) {
	f := LogEventFilter{Route: get("route"), RequestID: get("request_id"), TraceID: get("trace_id"), DeploymentID: get("deployment_id")}
	if raw := get("source"); raw != "" {
		if !validLogEventSource(LogEventSource(raw)) {
			return LogEventFilter{}, errors.New("source must be one of runtime, build, deploy, http, network, dns")
		}
		f.Source = LogEventSource(raw)
	}
	if raw := get("level"); raw != "" {
		for _, level := range strings.Split(raw, ",") {
			level = strings.ToLower(strings.TrimSpace(level))
			if !logEventLevels[level] {
				return LogEventFilter{}, errors.New("level must be a comma list of trace, debug, info, warn, error, fatal")
			}
			f.Levels = append(f.Levels, level)
		}
	}
	if raw := get("status"); raw != "" {
		if err := parseLogStatus(raw, &f); err != nil {
			return LogEventFilter{}, err
		}
	}
	if f.DeploymentID != "" {
		if _, err := uuid.Parse(f.DeploymentID); err != nil {
			return LogEventFilter{}, errors.New("deployment_id must be a UUID")
		}
	}
	if len(f.TraceID) > 128 {
		return LogEventFilter{}, errors.New("trace_id must be at most 128 characters")
	}
	if f.Query = get("q"); f.Query != "" {
		if n := utf8.RuneCountInString(f.Query); n < logEventQueryMinRunes || n > logEventQueryMaxRunes {
			return LogEventFilter{}, fmt.Errorf("q must be %d..%d characters", logEventQueryMinRunes, logEventQueryMaxRunes)
		}
	}
	return f, nil
}

// CanonicalLogQuery validates a stored query (a saved search or an alert's
// filter) and returns it in canonical form: only LogQueryKeys, one value
// each, non-empty values, keys sorted.
func CanonicalLogQuery(raw string) (string, LogEventFilter, error) {
	values, err := url.ParseQuery(raw)
	if err != nil {
		return "", LogEventFilter{}, fmt.Errorf("query is not a URL query string: %w", err)
	}
	allowed := map[string]bool{}
	for _, key := range LogQueryKeys {
		allowed[key] = true
	}
	canonical := url.Values{}
	for key, vs := range values {
		if !allowed[key] {
			return "", LogEventFilter{}, fmt.Errorf("query key %q is not a stored filter; use %s", key, strings.Join(LogQueryKeys, ", "))
		}
		if len(vs) != 1 {
			return "", LogEventFilter{}, fmt.Errorf("query key %q must appear once", key)
		}
		if v := strings.TrimSpace(vs[0]); v != "" {
			canonical.Set(key, v)
		}
	}
	f, err := ParseLogEventQuery(canonical.Get)
	if err != nil {
		return "", LogEventFilter{}, err
	}
	return canonical.Encode(), f, nil
}

func parseLogStatus(raw string, f *LogEventFilter) error {
	if class, ok := strings.CutSuffix(strings.ToLower(raw), "xx"); ok {
		n, err := strconv.Atoi(class)
		if err != nil || n < 1 || n > 5 {
			return errors.New("status class must be 1xx..5xx")
		}
		f.StatusClass = n
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 100 || n > 599 {
		return errors.New("status must be 100..599 or a class such as 5xx")
	}
	f.Status = n
	return nil
}
