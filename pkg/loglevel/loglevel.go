// Package loglevel classifies a plain-text log line by the common
// line shapes apps use to mark severity ([ERROR], level=error,
// "level":"error", …). It is the heuristic behind `gregale logs
// --level` (pkg/scheddgrpc.LevelMatcher) and the per-app log line
// counter in vmmd (ADR-746), shared so an alert on error lines and the
// command an operator runs next agree on what an error line is.
//
// It is deliberately conservative: a line that mentions "error" in prose
// is unclassified, because a false positive in a severity filter or an
// alert is worse than a miss.
package loglevel

import "strings"

// Canonical levels, lowest to highest.
const (
	Info  = "info"
	Warn  = "warn"
	Error = "error"
)

// Rank orders the canonical levels; unknown levels rank -1.
func Rank(level string) int {
	switch level {
	case Info:
		return 0
	case Warn:
		return 1
	case Error:
		return 2
	}
	return -1
}

type pattern struct {
	level string
	hit   string // lowercase substring
}

var patterns = []pattern{
	{Error, "[error]"}, {Error, "[err]"}, {Error, "level=error"}, {Error, `"level":"error"`}, {Error, `"level": "error"`}, {Error, `"severity":"error"`},
	{Warn, "[warn]"}, {Warn, "[warning]"}, {Warn, "level=warn"}, {Warn, `"level":"warn"`}, {Warn, `"level": "warn"`}, {Warn, `"severity":"warn"`},
	{Info, "[info]"}, {Info, "[notice]"}, {Info, "level=info"}, {Info, `"level":"info"`}, {Info, `"level": "info"`}, {Info, `"severity":"info"`},
}

// Detect returns the highest canonical level any pattern matches in line,
// case-insensitively, or "" when the line is unclassified.
func Detect(line string) string {
	lower := strings.ToLower(line)
	best := ""
	for _, p := range patterns {
		if Rank(p.level) > Rank(best) && strings.Contains(lower, p.hit) {
			best = p.level
		}
	}
	return best
}
