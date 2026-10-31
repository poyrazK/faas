package edgewaf

// adr: 831 — amendment 10: @grx matches exactly as Coraza's @rx, its
// pre-filter never rejects an input the regex matches, and its result cache
// is exact and kept apart per app.

import (
	"io/fs"
	"os"
	"regexp"
	"regexp/syntax"
	"slices"
	"strconv"
	"strings"
	"testing"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/experimental/plugins/plugintypes"

	"github.com/onebox-faas/faas/pkg/gateway"
)

var ownRxOn = engineOptions{ownRx: true}

// grxFor compiles pattern as @grx does, without registering it.
func grxFor(t testing.TB, pattern string) (*regexp.Regexp, rxRequirement, int) {
	t.Helper()
	data := "(?sm)" + pattern
	re, err := regexp.Compile(data)
	if err != nil {
		t.Fatalf("compile %q: %v", pattern, err)
	}
	parsed, err := syntax.Parse(data, syntax.Perl)
	if err != nil {
		t.Fatal(err)
	}
	parsed = parsed.Simplify()
	return re, rxRequired(parsed).best(), rxMinLen(parsed)
}

// checkSound fails when the pre-filter would skip a value the regex matches.
func checkSound(t testing.TB, pattern string, values ...string) {
	t.Helper()
	re, req, minLen := grxFor(t, pattern)
	for _, v := range values {
		if re.MatchString(v) && (len(v) < minLen || !req.mayMatch(v)) {
			t.Errorf("pattern %q: pre-filter rejects %q, which matches (min length %d, requirement %v)", pattern, v, minLen, req)
		}
	}
}

// TestGrxPrefilterSound covers the shapes that broke Coraza's pre-filter
// (corazawaf/coraza#1790) and the corners of literal matching.
func TestGrxPrefilterSound(t *testing.T) {
	for _, tc := range []struct {
		pattern string
		values  []string
	}{
		{`2(?:x|.2250)`, []string{"2.2250", "2x", "22250"}},
		{`2(?:x|.(?:ab|cd))`, []string{"2.ab", "2-cd", "2x"}},
		{`2(?:x|.p_(?:ab|cd))`, []string{"2.p_ab", "2xp_cd", "2x"}},
		{`(?i)^(?:429496729[56]|2(?:14748364[78]|.22507385850720(?:07|11)e-308)|-(?:214748364[89]|0000023456)|00000(?:12345|23456)|1e309)$`,
			[]string{"2.2250738585072011e-308", "2X2250738585072007E-308", "-2147483649", "1E309", "4294967295"}},
		{`s(?:e(?:lect|t)|leep)`, []string{"select", "set", "sleep"}},
		{`select.*from`, []string{"select x from", "selectfrom"}},
		// Unicode folding matches k with U+212A KELVIN SIGN and s with
		// U+017F LONG S.
		{`(?i)kss`, []string{"Kss", "kſs", "KSS"}},
		// U+FFFD in a pattern matches an invalid byte.
		{"a�b", []string{"a\xffb"}},
		{`(?i)union\s+select`, []string{"UNION  SELECT", "union\nselect"}},
		{`^abc$`, []string{"x\nabc\ny"}},
		// One-character requirements: punctuation and control bytes.
		{`(?i)\b(?:system|exec)[^0-9A-Z_a-z]*\(`, []string{"SYSTEM (", "exec(", "exec\t("}},
		{`(?:^|[^\x5c])\x5c[cdeg]`, []string{`a\c`, `\d`}},
		{`[^&\-]#.*?[\s\x0b]|;?\x00`, []string{"a# x", ";\x00", "\x00"}},
	} {
		checkSound(t, tc.pattern, tc.values...)
	}
}

// crsRxPatterns is every @rx argument in the embedded CRS rule files.
func crsRxPatterns(t testing.TB) []string {
	t.Helper()
	var out []string
	err := fs.WalkDir(coreruleset.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.Contains(path, ".conf") {
			return err
		}
		b, err := fs.ReadFile(coreruleset.FS, path)
		if err != nil {
			return err
		}
		s := string(b)
		for {
			i := strings.Index(s, `@rx `)
			if i < 0 {
				break
			}
			s = s[i+len(`@rx `):]
			end := 0
			for end < len(s) && (s[end] != '"' || strings.HasSuffix(s[:end], `\`) && !strings.HasSuffix(s[:end], `\\`)) {
				end++
			}
			out = append(out, s[:end])
			s = s[end:]
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// grxValues is input the regex test pool draws on: the package's benign
// and attack samples, broken into the values rules see.
func grxValues() []string {
	var out []string
	for _, s := range equivalenceSamples() {
		out = append(out, s.URI, string(s.Body))
		if _, q, ok := strings.Cut(s.URI, "?"); ok {
			for _, kv := range strings.Split(q, "&") {
				_, v, _ := strings.Cut(kv, "=")
				out = append(out, kv, v)
			}
		}
		for _, vs := range s.Header {
			out = append(out, vs...)
		}
	}
	for _, a := range []string{"2.2250738585072011e-308", "<script>alert(1)</script>", "1' or '1'='1", "../../etc/passwd",
		"; cat /etc/passwd", "${jndi:ldap://x/a}", "() { :; }; echo", "union select password from users"} {
		out = append(out, a, strings.ToUpper(a))
	}
	return out
}

// TestGrxPrefilterSoundOnCRS checks every CRS pattern against the sample
// pool, and against the CRS regression corpus when FAAS_WAF_FTW_DIR is set.
func TestGrxPrefilterSoundOnCRS(t *testing.T) {
	values := grxValues()
	if dir := os.Getenv("FAAS_WAF_FTW_DIR"); dir != "" {
		cases, _ := loadFTWCases(t, dir)
		for _, c := range cases {
			values = append(values, c.sample.URI, string(c.sample.Body))
			for _, vs := range c.sample.Header {
				values = append(values, vs...)
			}
		}
	}
	patterns := crsRxPatterns(t)
	if len(patterns) < 250 {
		t.Fatalf("found %d CRS @rx patterns, want the full rule set", len(patterns))
	}
	filtered := 0
	for _, p := range patterns {
		if matchesNonUTF8("(?sm)" + p) {
			continue
		}
		if _, err := regexp.Compile("(?sm)" + p); err != nil {
			continue // a seclang escape this test does not unescape
		}
		if _, req, _ := grxFor(t, p); len(req) > 0 {
			filtered++
		}
		checkSound(t, p, values...)
	}
	t.Logf("%d CRS patterns, %d with a literal pre-filter, %d values", len(patterns), filtered, len(values))
}

func FuzzGrxPrefilterSound(f *testing.F) {
	patterns := []string{`2(?:x|.2250)`, `2(?:x|.(?:ab|cd))`, `(?i)k(?:s|.ss)`, `a(?:bc|.de)f`, `(?i)(?:union|sel.ct)\s+all`}
	for _, v := range []string{"2.2250", "2xab", "Kss", "a.def", "UNION all"} {
		for i := range patterns {
			f.Add(i, v)
		}
	}
	f.Fuzz(func(t *testing.T, i int, v string) {
		if i < 0 {
			i = -i
		}
		checkSound(t, patterns[i%len(patterns)], v)
	})
}

// TestGrxMatchesStock compares the engines with and without @grx on the
// package's samples, twice, so the second pass reads the result cache.
func TestGrxMatchesStock(t *testing.T) {
	for _, cfg := range engineConfigs() {
		base, err := cfg.build(engineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := cfg.build(ownRxOn)
		if err != nil {
			t.Fatal(err)
		}
		var samples []gateway.WAFSample
		for _, s := range equivalenceSamples() {
			s.AppID = "app-equivalence"
			samples = append(samples, cfg.prep(s))
		}
		compareEngines(t, cfg.name, base, candidate, samples)
		compareEngines(t, cfg.name+" cached", base, candidate, samples)
	}
}

// TestGrxKnownFalseNegativeFixed: the input Coraza's pre-filter misses
// (TestRxPrefilterKnownFalseNegative) is detected through @grx.
func TestGrxKnownFalseNegativeFixed(t *testing.T) {
	waf, err := compileInlineWith(ownRxOn)
	if err != nil {
		t.Fatal(err)
	}
	s := sample("GET", "/get?i=2.2250738585072011e-308", nil, "")
	s.AppID = "app-fn"
	for range 2 {
		res, err := evaluateAll(waf, s)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(res.matched, 942220) {
			t.Fatalf("942220 matched = false through @grx; matched %v", res.matched)
		}
	}
}

// TestCRSRegressionCorpusOwnRxEquivalence compares the engines with and
// without @grx on every replayable CRS regression test (opt-in).
func TestCRSRegressionCorpusOwnRxEquivalence(t *testing.T) {
	dir := os.Getenv("FAAS_WAF_FTW_DIR")
	if dir == "" {
		t.Skip("set FAAS_WAF_FTW_DIR to a CRS tests/regression/tests checkout")
	}
	cases, _ := loadFTWCases(t, dir)
	for _, cfg := range engineConfigs() {
		base, err := cfg.build(engineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := cfg.build(ownRxOn)
		if err != nil {
			t.Fatal(err)
		}
		samples := make([]gateway.WAFSample, len(cases))
		for i, c := range cases {
			samples[i] = cfg.prep(c.sample)
			samples[i].AppID = "app-corpus"
		}
		compareEngines(t, cfg.name, base, candidate, samples)
		compareEngines(t, cfg.name+" cached", base, candidate, samples)
		t.Logf("[%s] %d samples compared twice", cfg.name, len(samples))
	}
}

// TestGrxCaptures: a chained rule that reads a capture sees the same
// fields from the regex and from the cache.
func TestGrxCaptures(t *testing.T) {
	// Rule 11 asks for the same value without and then with captures, so
	// the second link completes a cached result with its indexes.
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().WithDirectives(`SecRuleEngine On
SecRule ARGS:a "@grx ^(\w+)-(\d+)$" "id:10,phase:1,pass,capture,chain"
  SecRule TX:2 "@eq 42" "t:none"
SecRule ARGS:b "@grx ^(\w+)-(\d+)$" "id:11,phase:1,pass,chain"
  SecRule ARGS:b "@grx ^(\w+)-(\d+)$" "capture,chain"
    SecRule TX:1 "@streq kept" "t:none"`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query string
		want  []int
	}{
		{"a=build-42&b=kept-1", []int{10, 11}},
		{"a=build-43&b=other-1", nil},
		{"a=build-42&b=kept-1", []int{10, 11}},
		{"a=build-43&b=other-1", nil},
	} {
		tx := waf.NewTransactionWithID(rxTransactionID("app-captures", txSeq.Add(1)))
		tx.ProcessURI("/?"+tc.query, "GET", "HTTP/1.1")
		tx.ProcessRequestHeaders()
		var got []int
		for _, m := range tx.MatchedRules() {
			if id := m.Rule().ID(); (id == 10 || id == 11) && !slices.Contains(got, id) {
				got = append(got, id)
			}
		}
		_ = tx.Close()
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: matched %v, want %v", tc.query, got, tc.want)
		}
	}
}

// TestGrxCachePerApp: a result is cached under the app that asked for it,
// and a non-capturing hit is completed with indexes when a rule captures.
func TestGrxCachePerApp(t *testing.T) {
	op, err := newGregaleRx(plugintypes.OperatorOptions{Arguments: `(\d+)`})
	if err != nil {
		t.Fatal(err)
	}
	p := op.(*grx)
	c := newRxMemo()
	if ok, m := c.lookup("app-a", p, "x42", false); !ok || m != nil {
		t.Fatalf("first lookup = %v %v, want a match without indexes", ok, m)
	}
	if ok, m := c.lookup("app-a", p, "x42", true); !ok || !slices.Equal(m, []int{1, 3, 1, 3}) {
		t.Fatalf("capturing lookup = %v %v, want [1 3 1 3]", ok, m)
	}
	c.lookup("app-b", p, "x42", false)
	scopes := map[string]int{}
	for i := range c.shards {
		for k := range c.shards[i].m {
			scopes[k.scope]++
		}
	}
	if scopes["app-a"] != 1 || scopes["app-b"] != 1 {
		t.Errorf("cached entries by app = %v, want one each", scopes)
	}
	for i := range rxMemoShardEntries * rxMemoShards * 2 {
		c.lookup("app-c", p, "v"+strconv.Itoa(i), false)
	}
	total := 0
	for i := range c.shards {
		total += len(c.shards[i].m)
	}
	if total > rxMemoShards*rxMemoShardEntries {
		t.Errorf("cache holds %d entries, want at most %d", total, rxMemoShards*rxMemoShardEntries)
	}
}

func TestRxTransactionID(t *testing.T) {
	for _, tc := range []struct{ app, scope string }{{"app-1", "app-1"}, {"", ""}, {"a|b", ""}} {
		if got := rxScope(rxTransactionID(tc.app, 7)); got != tc.scope {
			t.Errorf("app %q: scope %q, want %q", tc.app, got, tc.scope)
		}
	}
}

func TestMatchesNonUTF8(t *testing.T) {
	for pattern, want := range map[string]bool{`abc`: false, `\x41\x42`: false, `\xff`: true, `\x{ff}`: true, `\xc3\xa9`: false, `é`: false} {
		if got := matchesNonUTF8(pattern); got != want {
			t.Errorf("%q: %v, want %v", pattern, got, want)
		}
	}
}
