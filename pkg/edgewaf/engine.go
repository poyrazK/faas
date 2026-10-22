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
	"io/fs"
	"slices"
	"strings"
	"sync/atomic"

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
	// maxCRSDetectionRules caps the rule_id label of
	// gateway_waf_rule_matches_total per app; TestRuleIDLabelIsBounded
	// checks the vendored CRS stays under it.
	maxCRSDetectionRules = 600
)

// Result is the scored outcome of one sample.
type Result struct {
	Score      int
	Detected   bool
	RuleIDs    []int
	Categories []string
}

// CRS assumes a website; platformPolicy and platformOverrides adapt it to a
// general compute platform (ADR-831 amendment 6), where which methods and
// body formats an app accepts is the app's business:
//
//   - REST methods are allowed. CRS allows GET, HEAD, POST and OPTIONS, so
//     911100 scored every PUT, PATCH and DELETE as critical.
//   - The content-type allow-list (920420) is dropped. It scored every
//     text/plain, octet-stream or NDJSON body as critical. So are the two
//     rules that score a body sent without a Content-Type (920340, 920640),
//     as Python's requests.post(url, data=json.dumps(...)) sends it. 920470
//     still flags a malformed Content-Type header.
//   - 921150 (CR/LF in an argument name) looks at query argument names
//     only. CRS parses a body of unknown type as URL-encoded so its rules
//     still see it (901340), which makes any multi-line body without "=" one
//     argument name containing a newline. The rule keeps its ID, so
//     exclude_rule_ids and the rule_id metric label are unchanged.
const (
	platformPolicy    = `SecAction "id:900200,phase:1,pass,t:none,nolog,setvar:'tx.allowed_methods=GET HEAD POST OPTIONS PUT PATCH DELETE'"`
	platformOverrides = `SecRuleRemoveById 920340 920420 920640
SecRuleRemoveById 921150
SecRule ARGS_GET_NAMES "@rx [\n\r]" "id:921150,phase:2,block,capture,t:none,msg:'HTTP Header Injection Attack via payload (CR/LF detected)',tag:'attack-protocol',tag:'paranoia-level/1',tag:'OWASP_CRS',tag:'OWASP_CRS/PROTOCOL-ATTACK',severity:'CRITICAL',setvar:'tx.http_violation_score=+%{tx.critical_anomaly_score}',setvar:'tx.inbound_anomaly_score_pl1=+%{tx.critical_anomaly_score}'"`
)

// engineOptions are engine settings that must not change a verdict, so
// tests can compare an engine with and without them.
type engineOptions struct {
	// rxPrefilter turns on Coraza's @rx pre-filter (SecRxPreFilter): at
	// load time each regex yields a minimum input length and literals any
	// match must contain, and an input without them skips the regex.
	rxPrefilter bool
	// ownRx loads CRS with @rx renamed to the gateway's @grx (rxop.go):
	// a pre-filter without corazawaf/coraza#1790, and results cached per
	// app (ADR-831 amendment 10).
	ownRx bool
}

// engine is the configuration the inspector compiles.
var engine = engineOptions{}

// rootFS is the rule files the engine loads.
func (o engineOptions) rootFS() fs.FS {
	if o.ownRx {
		return rxRenameFS{coreruleset.FS}
	}
	return coreruleset.FS
}

// rules returns directives with @rx renamed when the engine uses @grx.
func (o engineOptions) rules(directives string) string {
	if o.ownRx {
		return rxRenamer.Replace(directives)
	}
	return directives
}

// directive returns the engine's directives, to follow
// coraza.conf-recommended (which sets SecRxPreFilter Off) and precede the
// rules they apply to.
func (o engineOptions) directive() string {
	if o.rxPrefilter {
		return "SecRxPreFilter On"
	}
	return "SecRxPreFilter Off"
}

// compile builds the Coraza WAF for one paranoia level. The directives keep
// CRS in detection-only mode, turn off audit logging and response
// inspection, and cap the request body at the largest sample a rule can ask
// for; the gateway has already cut each sample to its rule's size.
func compile(paranoiaLevel int) (coraza.WAF, error) {
	return compileWith(paranoiaLevel, engine)
}

func compileWith(paranoiaLevel int, opts engineOptions) (coraza.WAF, error) {
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
%s
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=%d"
%s
Include @owasp_crs/*.conf
%s
SecRuleEngine DetectionOnly
SecAuditEngine Off
SecResponseBodyAccess Off
SecRequestBodyLimit %d
SecRequestBodyInMemoryLimit %d
SecRequestBodyLimitAction ProcessPartial`,
		opts.directive(), paranoiaLevel, platformPolicy, opts.rules(platformOverrides), api.MaxEdgeWAFInspectBodyBytes, api.MaxEdgeWAFInspectBodyBytes)
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(opts.rootFS()).WithDirectives(directives))
	if err != nil {
		return nil, fmt.Errorf("compile OWASP CRS at paranoia level %d: %w", paranoiaLevel, err)
	}
	return waf, nil
}

// txSeq numbers transactions so each ID is unique.
var txSeq atomic.Uint64

// evaluate runs one sample through waf and scores it against the sample's
// threshold and exclusions. The transaction ID names the app, which keys
// @grx's result cache (rxop.go).
func evaluate(waf coraza.WAF, s gateway.WAFSample) (Result, error) {
	tx := waf.NewTransactionWithID(rxTransactionID(s.AppID, txSeq.Add(1)))
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
