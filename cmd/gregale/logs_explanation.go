package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
	"github.com/onebox-faas/faas/pkg/safetext"
)

// explainCollector aggregates log lines for the --explain summary.
// One instance per invocation. Snapshot entry limits bound pattern cardinality;
// message excerpts and pattern prefixes are bounded separately.
type explainCollector struct {
	slug       string
	deployment string
	errorCount int
	warnCount  int
	infoCount  int
	patterns   map[string]int
	lastError  string
	redact     func(string) string
	shortened  bool
}

// explainLogRecord is the production app-log SSE data envelope. Line is a
// pointer so a JSON object without a line field can still fall through to the
// legacy text parser instead of being treated as an empty log line.
type explainLogRecord struct {
	Line     *string         `json:"line"`
	Stream   string          `json:"stream"`
	Level    json.RawMessage `json:"level"`
	Severity json.RawMessage `json:"severity"`
}

// newExplainCollector constructs the per-invocation collector.
// The patterns map is allocated lazily on first observe() — most
// log lines are info/warn, and we only count errors.
func newExplainCollector(slug, deployment string) *explainCollector {
	return &explainCollector{
		slug:       slug,
		deployment: deployment,
		patterns:   map[string]int{},
		redact:     newLogExplanationRedactor(),
	}
}

// observe ingests one SSE data line. Production streams carry a JSON
// envelope with a raw guest line plus optional structured severity; older
// streams use the legacy `{ts} {level} {message}` shape. The latter remains
// byte-compatible while the former gets a conservative stdout/stderr
// fallback when no level was classified server-side.
func (c *explainCollector) observe(line string) {
	trimmed := strings.TrimSpace(line)
	if strings.HasPrefix(trimmed, "{") {
		var record explainLogRecord
		if err := json.Unmarshal([]byte(trimmed), &record); err == nil && record.Line != nil {
			msg := *record.Line
			level := explainStructuredLevel(record.Level, record.Severity)
			if level == "" {
				if nestedLevel, nestedMessage := explainNestedStructuredLine(msg); nestedLevel != "" {
					level, msg = nestedLevel, nestedMessage
				}
			}
			if level == "" {
				level = explainFallbackLevel(record.Stream, msg)
			}
			c.observeLevel(level, msg)
			return
		}
	}
	// Split into at most 3 parts: ts, level, message.
	parts := strings.SplitN(line, " ", 3)
	if len(parts) < 3 {
		return
	}
	c.observeLevel(parts[1], parts[2])
}

// observeLevel updates the summary counters for an already-canonical level.
// Keeping this bookkeeping in one place prevents structured and legacy rows
// from drifting apart.
func (c *explainCollector) observeLevel(level, msg string) {
	msg = c.message(msg)
	switch level {
	case "error":
		c.errorCount++
		c.lastError = msg
		// Pattern bucket: first 64 bytes of the message. Enough
		// to coalesce the same error fired 100x into one bucket,
		// small enough to keep the map bounded. UTF-8 safe: a
		// naive byte-slice on a multi-byte rune (e.g. an emoji
		// or CJK character) would split the rune and produce an
		// invalid prefix that flush() can't render. Back off to
		// the last rune boundary when the 64th byte lands in the
		// middle of a sequence.
		prefix := msg
		if len(prefix) > 64 {
			prefix = prefix[:64]
			for len(prefix) > 0 {
				if r, size := utf8.DecodeLastRuneInString(prefix); r == utf8.RuneError && size == 1 {
					prefix = prefix[:len(prefix)-1]
				} else {
					break
				}
			}
		}
		c.patterns[prefix]++
	case "warn":
		c.warnCount++
	case "info":
		c.infoCount++
	}
}

func newLogExplanationRedactor() func(string) string {
	credentials := errorTextRedactor()
	redactor := redact.New(api.SSEMaxLineBytes)
	return func(message string) string {
		clean, _ := redactor.Apply(credentials(message))
		return clean
	}
}

func (c *explainCollector) message(message string) string {
	if c.redact != nil {
		message = c.redact(message)
	}
	if len(message) > api.CLILogExplanationMessageBytes {
		c.shortened = true
		return safetext.Truncate(message, api.CLILogExplanationMessageBytes)
	}
	return message
}

func (c *explainCollector) summary() logExplanationSummary {
	patterns := topExplanationPatterns(c.patterns, 3)
	if patterns == nil {
		patterns = []explanationPattern{}
	}
	return logExplanationSummary{ErrorCount: c.errorCount, WarnCount: c.warnCount, InfoCount: c.infoCount,
		LastError: c.lastError, TopPatterns: patterns, Shortened: c.shortened}
}

func formatExplanationPatterns(patterns []explanationPattern) []string {
	if len(patterns) == 0 {
		return nil
	}
	out := make([]string, len(patterns))
	for i, pattern := range patterns {
		out[i] = fmt.Sprintf("%s (N%d)", pattern.Pattern, pattern.Count)
	}
	return out
}

// explainStructuredLevel maps the string and numeric severity encodings used
// by common guest loggers (Pino/Zap/Cloud Logging) to the CLI's three-level
// vocabulary. If both fields are present, the most severe recognized value
// wins.
func explainStructuredLevel(fields ...json.RawMessage) string {
	best := ""
	for _, raw := range fields {
		value := strings.TrimSpace(string(raw))
		if value == "" || value == "null" {
			continue
		}
		level := ""
		if strings.HasPrefix(value, "\"") {
			var text string
			if json.Unmarshal([]byte(value), &text) == nil {
				level = explainNormalizeLevel(text)
			}
		} else if number, err := strconv.Atoi(value); err == nil {
			level = explainNormalizeNumericLevel(number)
		}
		if explainLevelRank(level) > explainLevelRank(best) {
			best = level
		}
	}
	return best
}

func explainNormalizeLevel(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "trace", "debug", "info", "notice":
		return "info"
	case "warn", "warning":
		return "warn"
	case "error", "err", "fatal", "critical", "alert", "emergency":
		return "error"
	default:
		return ""
	}
}

func explainNormalizeNumericLevel(value int) string {
	switch {
	case value >= 10 && value <= 30:
		return "info"
	case value == 40:
		return "warn"
	case value >= 50:
		return "error"
	default:
		return ""
	}
}

func explainLevelRank(level string) int {
	switch level {
	case "error":
		return 2
	case "warn":
		return 1
	case "info":
		return 0
	default:
		return -1
	}
}

// explainNestedStructuredLine handles a guest line that is itself a JSON log
// object when the server did not attach its additive outer level field.
func explainNestedStructuredLine(line string) (string, string) {
	if !strings.HasPrefix(strings.TrimSpace(line), "{") {
		return "", line
	}
	var record struct {
		Level    json.RawMessage `json:"level"`
		Severity json.RawMessage `json:"severity"`
		Msg      string          `json:"msg"`
		Message  string          `json:"message"`
		Error    string          `json:"error"`
	}
	if err := json.Unmarshal([]byte(line), &record); err != nil {
		return "", line
	}
	level := explainStructuredLevel(record.Level, record.Severity)
	if level == "" {
		return "", line
	}
	for _, message := range []string{record.Msg, record.Message, record.Error} {
		if message != "" {
			return level, message
		}
	}
	return level, line
}

// explainFallbackLevel gives raw guest output a useful, deterministic level
// when no structured field was emitted. Explicit error/warning words win;
// stderr is otherwise a warning and stdout (or an unknown stream) is info.
func explainFallbackLevel(stream, msg string) string {
	lower := strings.ToLower(msg)
	for _, keyword := range []string{"fatal", "panic", "error", "failed", "failure"} {
		if strings.Contains(lower, keyword) {
			return "error"
		}
	}
	for _, keyword := range []string{"warn", "warning"} {
		if strings.Contains(lower, keyword) {
			return "warn"
		}
	}
	if strings.EqualFold(strings.TrimSpace(stream), "stderr") {
		return "warn"
	}
	return "info"
}

// flush emits the 3-line summary on stream end. Output shape:
//
//	── explain: <slug> (deployment <id>) ──
//	error:  <lastError or "none">
//	levels: error=N warn=N info=N
//	top:    pattern1 (Nx) | pattern2 (Nx) | pattern3 (Nx)
//
// The summary is printed on os.Stdout (not os.Stderr) so a
// `gregale logs <slug> --explain > out.txt` captures both the
// stream AND the summary. Error path (when there's no failure)
// prints "(none)" so the script-friendly shape is preserved.
func (c *explainCollector) flush(w io.Writer) {
	_, _ = fmt.Fprintf(w, "\n── explain: %s", c.slug)
	if c.deployment != "" {
		_, _ = fmt.Fprintf(w, " (deployment %s)", c.deployment)
	}
	_, _ = fmt.Fprintln(w, " ──")
	summary := c.summary()
	if summary.LastError == "" {
		_, _ = fmt.Fprintln(w, "error:  (none)")
	} else {
		_, _ = fmt.Fprintf(w, "error:  %s\n", terminalSafeLine(summary.LastError, api.CLILogExplanationMessageBytes))
	}
	_, _ = fmt.Fprintf(w, "levels: error=%d warn=%d info=%d\n", summary.ErrorCount, summary.WarnCount, summary.InfoCount)
	top := formatExplanationPatterns(summary.TopPatterns)
	for i := range top {
		top[i] = terminalSafeLine(top[i], api.CLILogExplanationMessageBytes)
	}
	if len(top) == 0 {
		_, _ = fmt.Fprintln(w, "top:    (no error patterns)")
	} else {
		_, _ = fmt.Fprintf(w, "top:    %s\n", strings.Join(top, " | "))
	}
}

// topPatterns returns the top-N error patterns by count, formatted
// as "pattern (Nx)". Stable sort: ties resolve to alphabetical
// order so the output is deterministic. Empty input → empty slice.
func topPatterns(patterns map[string]int, n int) []string {
	return formatExplanationPatterns(topExplanationPatterns(patterns, n))
}

func topExplanationPatterns(patterns map[string]int, n int) []explanationPattern {
	if len(patterns) == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	pairs := make([]kv, 0, len(patterns))
	for k, v := range patterns {
		pairs = append(pairs, kv{k, v})
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].v != pairs[j].v {
			return pairs[i].v > pairs[j].v
		}
		return pairs[i].k < pairs[j].k
	})
	if len(pairs) > n {
		pairs = pairs[:n]
	}
	out := make([]explanationPattern, len(pairs))
	for i, p := range pairs {
		out[i] = explanationPattern{Pattern: p.k, Count: p.v}
	}
	return out
}
