package edgewaf

// ADR-831 preview gate, detection: replay the OWASP CRS regression corpus
// (go-ftw format) against both engine configurations the gateway uses
// (false positives on legitimate traffic are TestBenignCorpus):
//
//   - sampled: the full rule set at paranoia level 1 and 2, headers and body
//     (off-path inspection, every mode);
//   - in-path: the trimmed rule set at paranoia level 1, headers and URI only
//     (warn and block rules).
//
// Opt-in, because it needs the CRS test corpus checked out:
//
//	git clone --depth 1 --branch v4.25.0 --filter=blob:none --sparse \
//	    https://github.com/coreruleset/coreruleset.git crs
//	git -C crs sparse-checkout set tests/regression/tests
//	FAAS_WAF_FTW_DIR=$PWD/crs/tests/regression/tests go test ./pkg/edgewaf -run TestCRSRegressionCorpus -v

import (
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
	"gopkg.in/yaml.v3"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

type ftwFile struct {
	RuleID int       `yaml:"rule_id"`
	Tests  []ftwTest `yaml:"tests"`
}

type ftwTest struct {
	TestID int        `yaml:"test_id"`
	Stages []ftwStage `yaml:"stages"`
}

type ftwStage struct {
	Input struct {
		Method         string            `yaml:"method"`
		URI            string            `yaml:"uri"`
		Version        string            `yaml:"version"`
		Headers        map[string]string `yaml:"headers"`
		Data           string            `yaml:"data"`
		EncodedRequest string            `yaml:"encoded_request"`
		RawRequest     string            `yaml:"raw_request"`
		// AutocompleteHeaders defaults to true: go-ftw then adds a
		// Content-Length for the data.
		AutocompleteHeaders *bool `yaml:"autocomplete_headers"`
	} `yaml:"input"`
	Output struct {
		Log struct {
			ExpectIDs   []int `yaml:"expect_ids"`
			NoExpectIDs []int `yaml:"no_expect_ids"`
		} `yaml:"log"`
	} `yaml:"output"`
}

// ftwCase is one single-stage test the harness can replay.
type ftwCase struct {
	file     string
	category string
	ruleID   int
	testID   int
	positive bool // expect_ids names the rule; otherwise a negative test
	sample   gateway.WAFSample
}

// crsRuleParanoia maps each CRS rule ID to its paranoia level, read from the
// vendored rule files (rules without a paranoia tag run at every level).
func crsRuleParanoia(t *testing.T) map[int]int {
	t.Helper()
	out := map[int]int{}
	idRe := regexp.MustCompile(`id:(\d{6})`)
	plRe := regexp.MustCompile(`paranoia-level/(\d)`)
	err := fs.WalkDir(coreruleset.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".conf") {
			return err
		}
		b, err := fs.ReadFile(coreruleset.FS, path)
		if err != nil {
			return err
		}
		// A rule is a SecRule line plus its continuation lines; split on
		// "SecRule" so each chunk carries one rule's actions.
		for _, chunk := range strings.Split(string(b), "\nSecRule ") {
			id := idRe.FindStringSubmatch(chunk)
			if id == nil {
				continue
			}
			n, _ := strconv.Atoi(id[1])
			pl := 1
			if m := plRe.FindStringSubmatch(chunk); m != nil {
				pl, _ = strconv.Atoi(m[1])
			}
			out[n] = pl
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read CRS rules: %v", err)
	}
	return out
}

func loadFTWCases(t *testing.T, dir string) (cases []ftwCase, skipped int) {
	t.Helper()
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".yaml") {
			return err
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var f ftwFile
		if err := yaml.Unmarshal(b, &f); err != nil {
			return fmt.Errorf("parse %s: %w", path, err)
		}
		category := filepath.Base(filepath.Dir(path))
		for _, tc := range f.Tests {
			// Multi-stage tests depend on state between requests, and raw
			// or encoded requests carry malformed HTTP the gateway would
			// reject before any edge rule runs.
			if len(tc.Stages) != 1 || tc.Stages[0].Input.EncodedRequest != "" || tc.Stages[0].Input.RawRequest != "" {
				skipped++
				continue
			}
			st := tc.Stages[0]
			positive := slices.Contains(st.Output.Log.ExpectIDs, f.RuleID)
			negative := slices.Contains(st.Output.Log.NoExpectIDs, f.RuleID)
			if !positive && !negative {
				skipped++
				continue
			}
			cases = append(cases, ftwCase{
				file: filepath.Base(path), category: category, ruleID: f.RuleID, testID: tc.TestID,
				positive: positive, sample: ftwSample(st),
			})
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	return cases, skipped
}

func ftwSample(st ftwStage) gateway.WAFSample {
	in := st.Input
	header := http.Header{}
	host := "localhost"
	for k, v := range in.Headers {
		if strings.EqualFold(k, "Host") {
			host = v
			continue
		}
		header.Set(k, v)
	}
	method := in.Method
	if method == "" {
		method = http.MethodGet
	}
	uri := in.URI
	if uri == "" {
		uri = "/"
	}
	proto := in.Version
	if proto == "" {
		proto = "HTTP/1.1"
	}
	autocomplete := in.AutocompleteHeaders == nil || *in.AutocompleteHeaders
	if in.Data != "" && autocomplete && header.Get("Content-Length") == "" {
		header.Set("Content-Length", strconv.Itoa(len(in.Data)))
	}
	return gateway.WAFSample{
		AppID: "ftw", ParanoiaLevel: 1, AnomalyThreshold: api.EdgeWAFDefaultAnomalyThreshold,
		ClientIP: "203.0.113.9", Method: method, Host: host, URI: uri, Proto: proto,
		Header: header, Body: []byte(in.Data),
	}
}

// ftwResult tallies one configuration's run over the corpus.
type ftwResult struct {
	name                         string
	positives, ruleHit, detected int
	negatives                    int
	negFlagged                   int
	negRuleFired                 []string // negative tests whose own rule fired
	ruleMissed                   []string // positive tests whose own rule did not fire
	missedByCategory             map[string]int
	positivesByCategory          map[string]int
}

func runFTW(waf coraza.WAF, name string, cases []ftwCase, active func(ftwCase) bool, prep func(gateway.WAFSample) gateway.WAFSample) (ftwResult, error) {
	r := ftwResult{name: name, missedByCategory: map[string]int{}, positivesByCategory: map[string]int{}}
	for _, c := range cases {
		if !active(c) {
			continue
		}
		res, err := evaluateAll(waf, prep(c.sample))
		if err != nil {
			return r, fmt.Errorf("%s test %d: %w", c.file, c.testID, err)
		}
		if c.positive {
			r.positives++
			r.positivesByCategory[c.category]++
			if slices.Contains(res.matched, c.ruleID) {
				r.ruleHit++
			} else {
				r.ruleMissed = append(r.ruleMissed, fmt.Sprintf("%d-%d", c.ruleID, c.testID))
			}
			if res.Detected {
				r.detected++
			} else {
				r.missedByCategory[c.category]++
			}
			continue
		}
		r.negatives++
		if slices.Contains(res.matched, c.ruleID) {
			r.negRuleFired = append(r.negRuleFired, fmt.Sprintf("%d-%d", c.ruleID, c.testID))
		}
		if res.Detected {
			r.negFlagged++
		}
	}
	return r, nil
}

// fullResult is evaluate's Result plus every CRS rule that matched, scored
// or not, so a positive test can check its own rule fired.
type fullResult struct {
	Result
	matched []int
}

func evaluateAll(waf coraza.WAF, s gateway.WAFSample) (fullResult, error) {
	tx := waf.NewTransaction()
	defer func() { _ = tx.Close() }()
	tx.ProcessConnection(s.ClientIP, 0, "", 0)
	tx.SetServerName(s.Host)
	tx.ProcessURI(s.URI, s.Method, s.Proto)
	tx.AddRequestHeader("Host", s.Host)
	for name, values := range s.Header {
		for _, v := range values {
			tx.AddRequestHeader(name, v)
		}
	}
	tx.ProcessRequestHeaders()
	if len(s.Body) > 0 {
		if _, _, err := tx.WriteRequestBody(s.Body); err != nil {
			return fullResult{}, err
		}
	}
	if _, err := tx.ProcessRequestBody(); err != nil {
		return fullResult{}, err
	}
	out := fullResult{Result: score(tx.MatchedRules(), s.ExcludeRuleIDs)}
	out.Detected = out.Score >= s.AnomalyThreshold
	for _, m := range tx.MatchedRules() {
		out.matched = append(out.matched, m.Rule().ID())
	}
	tx.ProcessLogging()
	return out, nil
}

func (r ftwResult) report(t *testing.T) {
	t.Helper()
	pct := func(n, d int) float64 { return 100 * float64(n) / float64(max(d, 1)) }
	t.Logf("[%s] positive tests %d: expected rule fired %d (%.1f%%), detected at threshold 5: %d (%.1f%%)",
		r.name, r.positives, r.ruleHit, pct(r.ruleHit, r.positives), r.detected, pct(r.detected, r.positives))
	t.Logf("[%s]   expected rule did not fire: %v", r.name, r.ruleMissed)
	// Negative tests are near-miss payloads for one rule; other rules may
	// flag them by design, so only the rule's own firing is a regression.
	t.Logf("[%s] negative tests %d: rule fired anyway %d (%.1f%%) %v, scored at threshold by other rules %d",
		r.name, r.negatives, len(r.negRuleFired), pct(len(r.negRuleFired), r.negatives), r.negRuleFired, r.negFlagged)
	cats := make([]string, 0, len(r.positivesByCategory))
	for c := range r.positivesByCategory {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	for _, c := range cats {
		t.Logf("[%s]   %-42s detected %3d/%3d", r.name, c, r.positivesByCategory[c]-r.missedByCategory[c], r.positivesByCategory[c])
	}
}

func TestCRSRegressionCorpus(t *testing.T) {
	dir := os.Getenv("FAAS_WAF_FTW_DIR")
	if dir == "" {
		t.Skip("set FAAS_WAF_FTW_DIR to a CRS tests/regression/tests checkout")
	}
	paranoia := crsRuleParanoia(t)
	cases, skipped := loadFTWCases(t, dir)
	t.Logf("corpus: %d replayable single-stage tests, %d skipped (multi-stage, raw/encoded requests, or no rule expectation)", len(cases), skipped)

	// Tests of CRS policy the platform deliberately relaxes (platformPolicy,
	// platformOverrides) are out of scope for the sampled engine.
	relaxed := func(c ftwCase) bool {
		switch c.ruleID {
		case 920340, 920420, 920640:
			return true
		case 911100:
			return slices.Contains([]string{http.MethodPut, http.MethodPatch, http.MethodDelete}, c.sample.Method)
		case 921150:
			return len(c.sample.Body) > 0
		}
		return false
	}
	inDetection := func(c ftwCase) bool {
		return c.ruleID >= crsDetectionMin && c.ruleID <= crsDetectionMax && !relaxed(c)
	}
	atPL := func(pl int) func(ftwCase) bool {
		return func(c ftwCase) bool { return inDetection(c) && paranoia[c.ruleID] <= pl }
	}
	inlineFamilies := map[string]bool{}
	for _, n := range inlineRuleFiles {
		inlineFamilies[n] = true
	}
	inlineActive := func(c ftwCase) bool {
		family := strings.TrimPrefix(c.category, "REQUEST-")
		family, _, _ = strings.Cut(family, "-")
		// The in-path check never reads the body, so only attacks carried
		// in the URI or headers are in scope for it.
		return atPL(1)(c) && inlineFamilies[family] && len(c.sample.Body) == 0
	}
	asSampled := func(pl int) func(gateway.WAFSample) gateway.WAFSample {
		return func(s gateway.WAFSample) gateway.WAFSample { s.ParanoiaLevel = pl; return s }
	}
	noBody := func(s gateway.WAFSample) gateway.WAFSample { s.Body = nil; return s }

	for _, cfg := range []struct {
		name   string
		build  func() (coraza.WAF, error)
		active func(ftwCase) bool
		prep   func(gateway.WAFSample) gateway.WAFSample
	}{
		{"sampled PL1", func() (coraza.WAF, error) { return compile(1) }, atPL(1), asSampled(1)},
		{"sampled PL2", func() (coraza.WAF, error) { return compile(2) }, atPL(2), asSampled(2)},
		{"in-path PL1", compileInline, inlineActive, noBody},
	} {
		waf, err := cfg.build()
		if err != nil {
			t.Fatalf("%s: %v", cfg.name, err)
		}
		r, err := runFTW(waf, cfg.name, cases, cfg.active, cfg.prep)
		if err != nil {
			t.Fatal(err)
		}
		r.report(t)
	}
}
