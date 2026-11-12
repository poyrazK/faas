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
	// Fatal and critical are errors in the three-level vocabulary, as the
	// structured parse at ring intake maps them (ADR-20261109153000).
	{Error, "[fatal]"}, {Error, "[critical]"}, {Error, "[crit]"}, {Error, "[panic]"}, {Error, "[emerg]"},
	{Error, "level=fatal"}, {Error, "level=critical"}, {Error, "level=crit"}, {Error, "level=panic"},
	{Warn, "[warn]"}, {Warn, "[warning]"}, {Warn, "level=warn"}, {Warn, `"level":"warn"`}, {Warn, `"level": "warn"`}, {Warn, `"severity":"warn"`},
	{Info, "[info]"}, {Info, "[notice]"}, {Info, "level=info"}, {Info, `"level":"info"`}, {Info, `"level": "info"`}, {Info, `"severity":"info"`},
}

// prefixes are case-sensitive markers a line starts with (after leading
// whitespace): the default formats of Python's logging module and uvicorn
// ("ERROR:    Exception in ASGI application"), Python tracebacks, Go panics
// and runtime fatal errors, and the first line of a Node uncaught-exception
// report ("TypeError: …"). Uncaught crashes reach the log as plain stderr in
// these shapes, so without them a crash was no error line at all
// (ADR-20261109153000). They are anchored to the start of the line and
// case-sensitive, so prose that mentions an error stays unclassified.
var prefixes = []pattern{
	{Error, "ERROR:"}, {Error, "CRITICAL:"}, {Error, "FATAL:"}, {Error, "FATAL "},
	{Error, "Traceback (most recent call last):"},
	{Error, "panic: "}, {Error, "fatal error: "},
	{Error, "Uncaught "}, {Error, "UnhandledPromiseRejection"},
	{Warn, "WARNING:"}, {Warn, "WARN:"},
	{Info, "INFO:"},
}

// Detect returns the highest canonical level any pattern matches in line,
// case-insensitively, or any prefix starts it with, or "" when the line is
// unclassified.
func Detect(line string) string {
	lower := strings.ToLower(line)
	best := ""
	for _, p := range patterns {
		if Rank(p.level) > Rank(best) && strings.Contains(lower, p.hit) {
			best = p.level
		}
	}
	start := strings.TrimLeft(line, " \t")
	for _, p := range prefixes {
		if Rank(p.level) > Rank(best) && strings.HasPrefix(start, p.hit) {
			best = p.level
		}
	}
	if Rank(Error) > Rank(best) && jsErrorLine(start) {
		best = Error
	}
	return best
}

// jsErrorLine reports whether line opens a JavaScript error report: an
// error class name (Error, TypeError, MyAppError…) followed by ": ", as Node
// prints an uncaught exception before its stack.
func jsErrorLine(line string) bool {
	name, _, ok := strings.Cut(line, ": ")
	if !ok || !strings.HasSuffix(name, "Error") || name[0] < 'A' || name[0] > 'Z' {
		return false
	}
	for _, r := range name {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}
