package edgewaf

import (
	"io/fs"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

var (
	pl1Once sync.Once
	pl1     *Inspector
)

// sharedInspector compiles CRS once for the whole package (~80 ms).
func sharedInspector(t *testing.T) *Inspector {
	t.Helper()
	pl1Once.Do(func() { pl1 = New(&recordingObserver{}, nil) })
	if _, err := pl1.engine(1); err != nil {
		t.Fatalf("compile CRS: %v", err)
	}
	return pl1
}

func sample(method, uri string, header http.Header, body string) gateway.WAFSample {
	if header == nil {
		header = http.Header{}
	}
	header.Set("User-Agent", "Mozilla/5.0")
	header.Set("Accept", "*/*")
	if body != "" {
		header.Set("Content-Type", "application/json")
		header.Set("Content-Length", strconv.Itoa(len(body)))
	}
	return gateway.WAFSample{
		AppID: "app-1", ParanoiaLevel: 1, AnomalyThreshold: api.EdgeWAFDefaultAnomalyThreshold,
		ClientIP: "203.0.113.9", Method: method, Host: "app.example.test", URI: uri, Proto: "HTTP/1.1",
		Header: header, Body: []byte(body),
	}
}

func TestEvaluate(t *testing.T) {
	waf, _ := sharedInspector(t).engine(1)
	for _, tc := range []struct {
		name         string
		s            gateway.WAFSample
		wantDetected bool
		wantCategory string
		wantRule     int
	}{
		{name: "benign GET", s: sample(http.MethodGet, "/api/items?page=2", nil, "")},
		{name: "benign JSON body", s: sample(http.MethodPost, "/api/items", nil, `{"name":"widget","qty":3}`)},
		{name: "SQLi in query", s: sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, ""),
			wantDetected: true, wantCategory: "sqli", wantRule: 942100},
		{name: "XSS in JSON body", s: sample(http.MethodPost, "/api/comments", nil, `{"text":"<script>alert(document.cookie)</script>"}`),
			wantDetected: true, wantCategory: "xss"},
		{name: "path traversal", s: sample(http.MethodGet, "/download?file=../../../../etc/passwd", nil, ""),
			wantDetected: true, wantCategory: "lfi"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res, err := evaluate(waf, tc.s)
			if err != nil {
				t.Fatalf("evaluate: %v", err)
			}
			if res.Detected != tc.wantDetected {
				t.Fatalf("detected = %v (score %d, rules %v), want %v", res.Detected, res.Score, res.RuleIDs, tc.wantDetected)
			}
			if tc.wantCategory != "" && !slices.Contains(res.Categories, tc.wantCategory) {
				t.Errorf("categories = %v, want %s", res.Categories, tc.wantCategory)
			}
			if tc.wantRule != 0 && !slices.Contains(res.RuleIDs, tc.wantRule) {
				t.Errorf("rules = %v, want %d", res.RuleIDs, tc.wantRule)
			}
		})
	}
}

func TestEvaluateExclusionsAndThreshold(t *testing.T) {
	waf, _ := sharedInspector(t).engine(1)
	attack := sample(http.MethodGet, "/api/items?q=1%27%20OR%201%3D1--", nil, "")
	base, err := evaluate(waf, attack)
	if err != nil || !base.Detected {
		t.Fatalf("baseline: detected=%v err=%v", base.Detected, err)
	}

	excluded := attack
	excluded.ExcludeRuleIDs = slices.Clone(base.RuleIDs)
	res, err := evaluate(waf, excluded)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res.Detected || res.Score != 0 {
		t.Errorf("all matched rules excluded: detected=%v score=%d rules=%v", res.Detected, res.Score, res.RuleIDs)
	}

	raised := attack
	raised.AnomalyThreshold = base.Score + 1
	res, err = evaluate(waf, raised)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if res.Detected || res.Score != base.Score {
		t.Errorf("threshold above score: detected=%v score=%d, want not detected at score %d", res.Detected, res.Score, base.Score)
	}
}

// TestRuleIDLabelIsBounded pins how many distinct values the rule_id label of
// gateway_waf_rule_matches_total can take: the CRS detection rules in the
// vendored rule set. A CRS upgrade that changes the count should be a
// reviewed change to the metric's cardinality, not a surprise.
func TestRuleIDLabelIsBounded(t *testing.T) {
	ids := map[int]bool{}
	re := regexp.MustCompile(`id:(9[0-9]{5})`)
	err := fs.WalkDir(coreruleset.FS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".conf") {
			return err
		}
		b, err := fs.ReadFile(coreruleset.FS, path)
		if err != nil {
			return err
		}
		for _, m := range re.FindAllSubmatch(b, -1) {
			if id, _ := strconv.Atoi(string(m[1])); id >= crsDetectionMin && id <= crsDetectionMax {
				ids[id] = true
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk CRS: %v", err)
	}
	if len(ids) == 0 || len(ids) > maxCRSDetectionRules {
		t.Fatalf("CRS detection rules = %d, want 1..%d", len(ids), maxCRSDetectionRules)
	}
	t.Logf("CRS detection rules: %d", len(ids))
}

// TestEvaluateTruncatedJSONBody covers bodies longer than the inspection cap:
// the WAF sees a prefix that is no longer valid JSON, and an attack inside
// that prefix must still be scored.
func TestEvaluateTruncatedJSONBody(t *testing.T) {
	waf, _ := sharedInspector(t).engine(1)
	attack := `{"q":"1' OR 1=1--","pad":"` + strings.Repeat("a", 20*1024) + `"}`
	s := sample(http.MethodPost, "/api/search", nil, attack[:api.EdgeWAFDefaultInspectBodyBytes])
	s.BodyTruncated = true
	res, err := evaluate(waf, s)
	if err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if !res.Detected {
		t.Errorf("attack in the inspected prefix of a truncated JSON body was not detected: score=%d rules=%v", res.Score, res.RuleIDs)
	}
}
