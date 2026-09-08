package scheddgrpc

import (
	"fmt"
	"strings"
)

// LogFilter narrows the per-instance log stream before the per-instance
// fan-out coalesces into the single stream the gateway renders.
//
// Grep is a literal substring (case-insensitive) applied per line; Level
// is a heuristic floor matcher. Both nil = no filter (pass every line).
//
// Issue #309 / tier-2 DX gap: pre-#309 the gateway parsed `level` and
// `grep` off the request but discarded them with `_ = level; _ = grep`.
// This type is what the schedd server now builds from the proto
// request and applies inside the per-instance sink callback so the
// counter (apid_logs_dropped_total{reason="..."}) increments at the
// single fan-out point.
//
// The LogFilter value is safe for concurrent use after construction —
// the two fields are immutable post-ParseLogFilter.
type LogFilter struct {
	// Grep is the literal substring the customer passed (the gateway
	// validated the value at parse time). Empty = no grep filter.
	// A non-empty Grep is a substring match against each log line,
	// case-insensitive. Substring semantics were chosen over Go
	// regexp for two reasons: (1) the SDK contract in
	// pkg/api/logs.go documents --grep as a substring, and
	// substring matches better match customer mental models
	// (`--grep=ERROR` should NOT match `ERR0R`); (2) the regex
	// path exposed a DoS surface (long Compile cost +
	// expensive MatchString) that the peer review of PR #728
	// flagged, which is closed by avoiding regex entirely.
	Grep string
	// Level is the heuristic level floor. Nil = no level filter.
	Level *LevelMatcher
}

// NoFilter is the zero-value identity — returns "no filter, pass
// every line". Used as the default when both proto fields are empty.
func (f LogFilter) NoFilter() bool {
	return f.Grep == "" && f.Level == nil
}

// ParseLogFilter builds a LogFilter from the proto's level and grep
// strings. Both empty = NoFilter.
//
// An invalid level value (one not in {info, warn, error}) returns an
// error. The gateway already enum-checks via api.IsValidLogLevel, so
// this path is hit only when the gateway skipped validation (tests,
// future direct RPC clients). Defence in depth.
//
// Grep validation is intentionally a no-op here: a substring is any
// non-empty string without an embedded newline, both of which the
// gateway has already enforced. Doing substring search here (vs.
// compiling a regex) closes the regex-DoS surface the peer review
// of PR #728 flagged.
func ParseLogFilter(level, grep string) (LogFilter, error) {
	var out LogFilter
	if grep != "" {
		if strings.ContainsAny(grep, "\n\r") {
			return LogFilter{}, fmt.Errorf("grep must not contain newline or carriage return")
		}
		out.Grep = grep
	}
	if level != "" {
		m, err := NewLevelMatcher(level)
		if err != nil {
			return LogFilter{}, err
		}
		out.Level = m
	}
	return out, nil
}

// MatchLine applies the filter to a single log line. Returns true if
// the line should be passed through; false if it should be dropped.
// It retains the pre-structured-logging API and falls back to the
// heuristic level detector when no parsed level is available.
func (f LogFilter) MatchLine(line string) bool {
	return f.MatchLineWithLevel(line, "")
}

// MatchLineWithLevel applies the filter using the parsed structured-log level
// when one is available. This keeps JSON severity aliases (for example,
// Cloud Logging's WARNING) consistent with the level exposed on the SSE wire,
// while preserving heuristic matching for legacy plain-text lines.
func (f LogFilter) MatchLineWithLevel(line, level string) bool {
	if f.Grep != "" && !strings.Contains(strings.ToLower(line), strings.ToLower(f.Grep)) {
		return false
	}
	if f.Level != nil && !f.Level.MatchLevelOrLine(level, line) {
		return false
	}
	return true
}

// LevelMatcher is the heuristic floor matcher for the issue #309
// `faas logs --level` flag. It does not understand structured logging
// — it pattern-matches common line shapes ([ERROR], level=error,
// JSON {"level":"error"}, etc.) and treats a non-matching line as
// "below the floor" → drop.
//
// The matcher is best-effort by design: the customer contract is
// "filter out lower-severity noise", and a strict level match would
// require the runner to emit structured `level` fields, which is
// itself an ADR-shaped change. Customers needing strict level
// matching can use --grep with an explicit substring instead.
//
// The floor is inclusive: a --level=warn filter passes warn AND error
// lines. A --level=info filter passes info, warn, AND error lines.
// A --level=error filter passes error lines only.
//
// Construct via NewLevelMatcher (validates the level string against
// api.IsValidLogLevel).
type LevelMatcher struct {
	// floor is the requested level (info | warn | error).
	floor string
	// ranks assigns each level a number; the matcher passes a line
	// when its detected level rank >= floor rank. The mapping is
	// inclusive above the floor (warn filter passes warn + error).
	//
	// The "detected" level is the highest level any of the
	// heuristic patterns match. A line matching both an error
	// pattern and an info pattern is classified as error (we
	// err on the side of "loud").
	ranks map[string]int
	// patterns is the list of (level, substring) heuristic hits
	// applied case-insensitively to each line. Pre-lowercased at
	// construction so the per-line hot path stays cheap.
	patterns []levelPattern
}

type levelPattern struct {
	level string
	hit   string // pre-lowercased substring
}

// NewLevelMatcher returns a LevelMatcher for the given floor level.
// Returns an error for any level not in {info, warn, error} — the
// gateway already rejects those via api.IsValidLogLevel, so this is
// defence in depth.
//
// The level string is case-folded at parse time; "WARN", "Warn", and
// "warn" all behave identically.
func NewLevelMatcher(level string) (*LevelMatcher, error) {
	floor := strings.ToLower(level)
	switch floor {
	case "info", "warn", "error":
	default:
		return nil, fmt.Errorf("invalid level %q (must be one of: info, warn, error)", level)
	}
	m := &LevelMatcher{
		floor: floor,
		ranks: map[string]int{"info": 0, "warn": 1, "error": 2},
	}
	// Heuristic patterns per level. Pre-lowercased. Order does
	// not matter — Match() picks the highest detected level.
	//
	// Patterns are deliberately conservative: a single false
	// positive (a `[INFO]` substring inside an error message)
	// is more annoying than a single false negative (a
	// customer-emitted level=warn that's missing the prefix).
	for _, hit := range []string{"[error]", "[err]", "level=error", `"level":"error"`, `"level": "error"`, `"severity":"error"`} {
		m.patterns = append(m.patterns, levelPattern{level: "error", hit: hit})
	}
	for _, hit := range []string{"[warn]", "[warning]", "level=warn", `"level":"warn"`, `"level": "warn"`, `"severity":"warn"`} {
		m.patterns = append(m.patterns, levelPattern{level: "warn", hit: hit})
	}
	for _, hit := range []string{"[info]", "[notice]", "level=info", `"level":"info"`, `"level": "info"`, `"severity":"info"`} {
		m.patterns = append(m.patterns, levelPattern{level: "info", hit: hit})
	}
	return m, nil
}

// Match returns true when the line is at the floor level or higher.
// A line with no recognised pattern is treated as "below the floor"
// → drop. This is intentional: a customer who asks for --level=warn
// wants noise gone, not "everything plus a few hits".
func (m *LevelMatcher) Match(line string) bool {
	if m == nil {
		return true // no filter
	}
	lower := strings.ToLower(line)
	// detectedRank starts at -1 so any match (info = rank 0)
	// strictly exceeds it. Without the -1 seed, m.ranks[""]
	// returns the zero value 0 which equals the info rank, and
	// a strict-greater compare would never fire for info-level
	// patterns.
	detected := ""
	detectedRank := -1
	for _, p := range m.patterns {
		if strings.Contains(lower, p.hit) {
			if r := m.ranks[p.level]; r > detectedRank {
				detected = p.level
				detectedRank = r
			}
		}
	}
	if detected == "" {
		return false
	}
	return detectedRank >= m.ranks[m.floor]
}

// MatchLevelOrLine prefers a canonical level parsed at ring intake and falls
// back to the legacy line-shape heuristic when the line is unclassified.
func (m *LevelMatcher) MatchLevelOrLine(level, line string) bool {
	if m == nil {
		return true
	}
	if level != "" {
		rank, ok := m.ranks[level]
		return ok && rank >= m.ranks[m.floor]
	}
	return m.Match(line)
}
