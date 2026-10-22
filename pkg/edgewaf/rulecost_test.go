package edgewaf

// TestInlineRuleSetCost measures what rule families and single rules cost
// the in-path check, and what each uniquely detects: the in-path rule set
// is rebuilt without them, timed on benign header and URI traffic, and
// replayed against the CRS regression tests it covers (ADR-831 amendment
// 6). Opt-in, because it needs the CRS corpus:
//
//	FAAS_WAF_FTW_DIR=... go test ./pkg/edgewaf/ -run TestInlineRuleSetCost -v
//
// FAAS_WAF_RULE_COST_REMOVE replaces the default variants with
// semicolon-separated SecRuleRemoveById arguments, such as
// "932230 932250 932260;942000-942999".

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"

	"github.com/onebox-faas/faas/pkg/gateway"
)

func compileInlineRemoving(remove string) (coraza.WAF, error) {
	var rules strings.Builder
	for _, n := range inlineRuleFiles {
		fmt.Fprintf(&rules, "Include @owasp_crs/REQUEST-%s-*.conf\n", n)
	}
	if remove != "" {
		fmt.Fprintf(&rules, "SecRuleRemoveById %s\n", remove)
	}
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
%s
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=1"
%sSecRuleEngine DetectionOnly
SecAuditEngine Off
SecRequestBodyAccess Off
SecResponseBodyAccess Off`, engine.directive(), rules.String())
	return coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(coreruleset.FS).WithDirectives(directives))
}

// inPathCorpusAttacks are the replayable PL1 attack tests of the in-path
// rule files that carry the attack in the URI or headers.
func inPathCorpusAttacks(t *testing.T, dir string) []gateway.WAFSample {
	t.Helper()
	cases, _ := loadFTWCases(t, dir)
	paranoia := crsRuleParanoia(t)
	inline := map[string]bool{}
	for _, n := range inlineRuleFiles {
		inline[n] = true
	}
	var out []gateway.WAFSample
	for _, c := range cases {
		family, _, _ := strings.Cut(strings.TrimPrefix(c.category, "REQUEST-"), "-")
		if c.positive && c.ruleID >= crsDetectionMin && c.ruleID <= crsDetectionMax &&
			paranoia[c.ruleID] <= 1 && inline[family] && len(c.sample.Body) == 0 {
			out = append(out, c.sample)
		}
	}
	return out
}

func TestInlineRuleSetCost(t *testing.T) {
	dir := os.Getenv("FAAS_WAF_FTW_DIR")
	if dir == "" {
		t.Skip("set FAAS_WAF_FTW_DIR to a CRS tests/regression/tests checkout")
	}
	removals := []string{"", "932000-932999", "942000-942999", "941000-941999", "930000-931999",
		"913000-913999 921000-921999", "932230 932250 932260", "932230 932235 932250 932260 932340", ""}
	if v := os.Getenv("FAAS_WAF_RULE_COST_REMOVE"); v != "" {
		removals = append([]string{""}, strings.Split(v, ";")...)
	}
	attacks := inPathCorpusAttacks(t, dir)
	samples := inPathBenchSamples()
	wafs := make([]coraza.WAF, len(removals))
	detected := make([]int, len(removals))
	for i, remove := range removals {
		waf, err := compileInlineRemoving(remove)
		if err != nil {
			t.Fatalf("remove %q: %v", remove, err)
		}
		wafs[i] = waf
		for _, a := range attacks {
			res, err := evaluate(waf, a)
			if err != nil {
				t.Fatal(err)
			}
			if res.Detected {
				detected[i]++
			}
		}
	}
	// Interleave the variants so drift on a busy host hits them alike.
	const rounds, reps = 16, 40
	cost := make([]time.Duration, len(removals))
	for range rounds {
		for i, waf := range wafs {
			start := time.Now()
			for range reps {
				for _, s := range samples {
					if _, err := evaluate(waf, s); err != nil {
						t.Fatal(err)
					}
				}
			}
			cost[i] += time.Since(start)
		}
	}
	per := func(d time.Duration) float64 { return float64(d.Microseconds()) / float64(rounds*reps*len(samples)) }
	base := per(cost[0])
	t.Logf("%d benign in-path samples, %d in-scope corpus attacks", len(samples), len(attacks))
	for i, remove := range removals {
		name := "full in-path set"
		if remove != "" {
			name = "without " + remove
		}
		t.Logf("%-48s %6.0f µs/check (%+5.1f%%), attacks detected %d (%+d)",
			name, per(cost[i]), 100*(per(cost[i])-base)/base, detected[i], detected[i]-detected[0])
	}
}
