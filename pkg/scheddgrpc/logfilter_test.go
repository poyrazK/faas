package scheddgrpc

import (
	"testing"
)

func TestLogFilter_NoFilterPassesEverything(t *testing.T) {
	f := LogFilter{}
	if !f.NoFilter() {
		t.Errorf("zero-value LogFilter should report NoFilter=true")
	}
	if !f.MatchLine("anything goes here") {
		t.Errorf("zero-value LogFilter must pass every line; got drop")
	}
	if !f.MatchLine("[ERROR] boom") {
		t.Errorf("zero-value LogFilter must pass error lines too")
	}
}

func TestParseLogFilter_BothEmpty(t *testing.T) {
	f, err := ParseLogFilter("", "")
	if err != nil {
		t.Fatalf("ParseLogFilter(empty): %v", err)
	}
	if !f.NoFilter() {
		t.Errorf("ParseLogFilter(\"\", \"\") must produce NoFilter")
	}
}

func TestParseLogFilter_GrepRejectsNewline(t *testing.T) {
	// Newline rejection is the only validation ParseLogFilter
	// does on grep; substring semantics mean any other
	// character class is fine (peer review of PR #728).
	for _, bad := range []string{"foo\nbar", "foo\rbar"} {
		if _, err := ParseLogFilter("", bad); err == nil {
			t.Errorf("ParseLogFilter must reject grep %q (newline)", bad)
		}
	}
}

func TestParseLogFilter_InvalidLevel(t *testing.T) {
	_, err := ParseLogFilter("debug", "")
	if err == nil {
		t.Fatalf("ParseLogFilter must reject level=debug (not in info|warn|error)")
	}
}

func TestLogFilter_GrepSubstringMatch(t *testing.T) {
	// Customer-facing semantics: --grep is a literal substring
	// (case-insensitive). The match is anchored on occurrence,
	// not on regex semantics — ?grep=foo.bar matches the
	// literal "foo.bar", NOT "fooXbar". Peer review of PR #728
	// switched the implementation from Go regexp to substring
	// to (a) match the SDK contract in pkg/api/logs.go and
	// (b) close the regex-DoS surface entirely.
	f, err := ParseLogFilter("", "timeout")
	if err != nil {
		t.Fatalf("ParseLogFilter: %v", err)
	}
	if f.NoFilter() {
		t.Fatalf("expected filter to be active")
	}
	cases := []struct {
		line string
		want bool
	}{
		{"request timeout exceeded", true},
		{"TIMEOUT fired", true}, // case-insensitive
		{"everything is fine", false},
		// Substring literal: a "." is a literal dot, NOT a wildcard.
		// A line containing "timeout" with an unrelated "."
		// character elsewhere still matches on the "timeout"
		// substring; what matters is whether the literal
		// substring "timeout" appears in the line.
		{"timeout at 12.5s", true},
	}
	for _, c := range cases {
		if got := f.MatchLine(c.line); got != c.want {
			t.Errorf("MatchLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestLogFilter_GrepLiteralDot(t *testing.T) {
	// The "." character in the grep pattern must be a literal
	// dot, NOT a regex wildcard. This is the test that pins
	// substring semantics against the pre-#728 regex behaviour:
	// `--grep=foo.bar` would match `fooXbar` under regex, must
	// NOT under substring.
	f, err := ParseLogFilter("", "foo.bar")
	if err != nil {
		t.Fatalf("ParseLogFilter: %v", err)
	}
	if f.NoFilter() {
		t.Fatalf("expected filter to be active")
	}
	cases := []struct {
		line string
		want bool
	}{
		{"the literal foo.bar substring", true},
		{"foo.bar with no other context", true},
		// These would have matched under regex but NOT under
		// substring — the "." is literal.
		{"fooXbar", false},
		{"fooabar", false},
		{"foozbar", false},
	}
	for _, c := range cases {
		if got := f.MatchLine(c.line); got != c.want {
			t.Errorf("MatchLine(%q) = %v, want %v", c.line, got, c.want)
		}
	}
}

func TestLevelMatcher_NewLevelMatcher(t *testing.T) {
	for _, lvl := range []string{"info", "warn", "error"} {
		if _, err := NewLevelMatcher(lvl); err != nil {
			t.Errorf("NewLevelMatcher(%q): %v", lvl, err)
		}
	}
	for _, lvl := range []string{"DEBUG", "trace", ""} {
		if _, err := NewLevelMatcher(lvl); err == nil {
			t.Errorf("NewLevelMatcher(%q) should reject", lvl)
		}
	}
}

func TestLevelMatcher_FloorError(t *testing.T) {
	m, err := NewLevelMatcher("error")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		line string
		want bool
	}{
		// error floor — pass error lines, drop warn/info/blank
		{"[ERROR] db unreachable", true},
		{"level=error fatal", true},
		{`{"level":"error","msg":"x"}`, true},
		{"[WARN] retrying", false},
		{"[INFO] startup ok", false},
		{"plain stdout with no level marker", false},
	}
	for _, c := range cases {
		if got := m.Match(c.line); got != c.want {
			t.Errorf("Match(%q) = %v, want %v (floor=error)", c.line, got, c.want)
		}
	}
}

func TestLevelMatcher_FloorWarn(t *testing.T) {
	m, err := NewLevelMatcher("warn")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		line string
		want bool
	}{
		// warn floor — pass warn AND error, drop info/blank
		{"[ERROR] db unreachable", true},
		{"[WARN] retrying", true},
		{"level=warn slow query", true},
		{"[INFO] startup ok", false},
		{"plain stdout", false},
	}
	for _, c := range cases {
		if got := m.Match(c.line); got != c.want {
			t.Errorf("Match(%q) = %v, want %v (floor=warn)", c.line, got, c.want)
		}
	}
}

func TestLevelMatcher_FloorInfo(t *testing.T) {
	m, err := NewLevelMatcher("info")
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		line string
		want bool
	}{
		{"[INFO] startup ok", true},
		{"[WARN] retrying", true},
		{"[ERROR] db unreachable", true},
		// pino / winston / structlog / loguru shapes
		{`{"level":"info","time":1700000000,"msg":"req"}`, true},
		{`{"level":30,"msg":"req"}`, false}, // pino numeric levels: not covered
		{"plain stdout", false},             // no marker → drop
	}
	for _, c := range cases {
		if got := m.Match(c.line); got != c.want {
			t.Errorf("Match(%q) = %v, want %v (floor=info)", c.line, got, c.want)
		}
	}
}

func TestLevelMatcher_NilReceiver(t *testing.T) {
	var m *LevelMatcher
	if !m.Match("anything") {
		t.Error("nil *LevelMatcher.Match must return true (no filter)")
	}
}

func TestLevelMatcher_HighestDetectedWins(t *testing.T) {
	// A line that matches BOTH info and error patterns should be
	// classified as error — we err loud.
	m, _ := NewLevelMatcher("info")
	if !m.Match("[ERROR] [INFO] both markers") {
		t.Error("line matching both error + info should classify as error (loud wins)")
	}
}

func TestLogFilter_GrepAndLevelCombine(t *testing.T) {
	// --grep=timeout --level=warn: pass lines that hit BOTH.
	f, err := ParseLogFilter("warn", "timeout")
	if err != nil {
		t.Fatal(err)
	}
	if f.NoFilter() {
		t.Fatal("filter should be active")
	}
	cases := []struct {
		line string
		want bool
	}{
		{"[ERROR] timeout exceeded", true},  // grep + level=error → ok
		{"[WARN] timeout near limit", true}, // grep + level=warn → ok
		{"[INFO] timeout scheduled", false}, // grep hit but info below warn floor
		{"[ERROR] something else", false},   // level ok but no grep hit
		{"plain timeout", false},            // grep hit but no level marker
	}
	for _, c := range cases {
		if got := f.MatchLine(c.line); got != c.want {
			t.Errorf("MatchLine(%q) = %v, want %v (level=warn grep=timeout)", c.line, got, c.want)
		}
	}
}

func TestLogFilter_UsesParsedStructuredLevel(t *testing.T) {
	f, err := ParseLogFilter("warn", "")
	if err != nil {
		t.Fatal(err)
	}
	if !f.MatchLineWithLevel(`{"severity":"WARNING","message":"slow"}`, "warn") {
		t.Error("parsed warn level should pass warn floor")
	}
	if f.MatchLineWithLevel(`{"severity":"INFO","message":"ok"}`, "info") {
		t.Error("parsed info level should fail warn floor")
	}
}
