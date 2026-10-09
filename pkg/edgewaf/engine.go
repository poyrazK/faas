// Package edgewaf evaluates kind=waf samples with the OWASP Core Rule Set
// through Coraza (ADR-831 step 1). Inspection is observe-only and runs off
// the request path: the gateway hands over a finished request's headers and
// body prefix, and a bounded worker pool scores it.
//
// Scoring is done here, not by CRS's blocking-evaluation rules, so per-rule
// exclusions and thresholds can vary per edge rule while one compiled rule
// set per paranoia level is shared by every app on the node.
package edgewaf

import (
	"fmt"
	"slices"
	"strings"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"
	"github.com/corazawaf/coraza/v3/types"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/gateway"
)

// CRS request-detection rules live in 911000–948999 (method enforcement,
// scanner detection, protocol enforcement/attack, and the attack families).
// IDs outside the range are initialization, paranoia skips, blocking
// evaluation, and correlation rules, which carry no detection of their own.
const (
	crsDetectionMin = 911000
	crsDetectionMax = 948999
)

// Result is the scored outcome of one sample.
type Result struct {
	Score      int
	Detected   bool
	RuleIDs    []int
	Categories []string
}

// compile builds the Coraza WAF for one paranoia level. The directives keep
// CRS in detection-only mode, turn off audit logging and response
// inspection, and cap the request body at the gateway's sample size.
func compile(paranoiaLevel int) (coraza.WAF, error) {
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=%d"
Include @owasp_crs/*.conf
SecRuleEngine DetectionOnly
SecAuditEngine Off
SecResponseBodyAccess Off
SecRequestBodyLimit %d
SecRequestBodyInMemoryLimit %d
SecRequestBodyLimitAction ProcessPartial`,
		paranoiaLevel, api.EdgeWAFInspectBodyBytes, api.EdgeWAFInspectBodyBytes)
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(coreruleset.FS).WithDirectives(directives))
	if err != nil {
		return nil, fmt.Errorf("compile OWASP CRS at paranoia level %d: %w", paranoiaLevel, err)
	}
	return waf, nil
}

// evaluate runs one sample through waf and scores it against the sample's
// threshold and exclusions.
func evaluate(waf coraza.WAF, s gateway.WAFSample) (Result, error) {
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
			return Result{}, fmt.Errorf("write request body: %w", err)
		}
	}
	if _, err := tx.ProcessRequestBody(); err != nil {
		return Result{}, fmt.Errorf("process request body: %w", err)
	}
	res := score(tx.MatchedRules(), s.ExcludeRuleIDs)
	res.Detected = res.Score >= s.AnomalyThreshold
	tx.ProcessLogging()
	return res, nil
}

// score sums CRS anomaly points for detection rules that are not excluded.
// Points follow CRS defaults: critical 5, error 4, warning 3, notice 2.
func score(matched []types.MatchedRule, exclude []int) Result {
	var res Result
	for _, m := range matched {
		rule := m.Rule()
		id := rule.ID()
		if id < crsDetectionMin || id > crsDetectionMax {
			continue
		}
		if _, excluded := slices.BinarySearch(exclude, id); excluded {
			continue
		}
		points := severityPoints(rule.Severity())
		if points == 0 {
			continue
		}
		res.Score += points
		res.RuleIDs = append(res.RuleIDs, id)
		for _, c := range categories(rule.Tags()) {
			if !slices.Contains(res.Categories, c) {
				res.Categories = append(res.Categories, c)
			}
		}
	}
	slices.Sort(res.RuleIDs)
	res.RuleIDs = slices.Compact(res.RuleIDs)
	slices.Sort(res.Categories)
	return res
}

func severityPoints(s types.RuleSeverity) int {
	switch s {
	case types.RuleSeverityCritical:
		return 5
	case types.RuleSeverityError:
		return 4
	case types.RuleSeverityWarning:
		return 3
	case types.RuleSeverityNotice:
		return 2
	}
	return 0
}

// crsCategories maps CRS "attack-*" tags to the bounded metric label set.
// The keys are the attack tags used by CRS v4.25.
var crsCategories = map[string]string{
	"attack-sqli":               "sqli",
	"attack-xss":                "xss",
	"attack-rce":                "rce",
	"attack-lfi":                "lfi",
	"attack-rfi":                "rfi",
	"attack-ssrf":               "ssrf",
	"attack-ssti":               "ssti",
	"attack-injection-php":      "php",
	"attack-injection-java":     "java",
	"attack-injection-generic":  "generic",
	"attack-generic":            "generic",
	"attack-protocol":           "protocol",
	"attack-deprecated-header":  "protocol",
	"attack-multipart-header":   "multipart",
	"attack-reputation-scanner": "scanner",
	"attack-fixation":           "session_fixation",
}

func categories(tags []string) []string {
	var out []string
	for _, tag := range tags {
		if !strings.HasPrefix(tag, "attack-") {
			continue
		}
		c, ok := crsCategories[tag]
		if !ok {
			c = "other"
		}
		out = append(out, c)
	}
	return out
}
