package edgewaf

import (
	"fmt"
	"strings"
	"time"

	coreruleset "github.com/corazawaf/coraza-coreruleset/v4"
	"github.com/corazawaf/coraza/v3"

	"github.com/onebox-faas/faas/pkg/gateway"
)

// inlineRuleFiles is the CRS rule set warn and block rules run in-path
// (ADR-831 amendment 3 addendum): initialization, scanner detection,
// protocol attack, and the LFI, RFI, RCE, XSS and SQLi families. On headers
// and URI it catches what the full set catches at about 1.1 ms p95 instead of
// 1.6 ms.
var inlineRuleFiles = []string{"901", "913", "921", "930", "931", "932", "941", "942"}

// compileInline builds the in-path WAF: paranoia level 1, request body access
// off, so phase 2 evaluates headers, URI and query arguments without reading
// the body, which is still streaming to the proxy.
func compileInline() (coraza.WAF, error) {
	var rules strings.Builder
	for _, n := range inlineRuleFiles {
		fmt.Fprintf(&rules, "Include @owasp_crs/REQUEST-%s-*.conf\n", n)
	}
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=1"
%sSecRuleEngine DetectionOnly
SecAuditEngine Off
SecRequestBodyAccess Off
SecResponseBodyAccess Off`, rules.String())
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(coreruleset.FS).WithDirectives(directives))
	if err != nil {
		return nil, fmt.Errorf("compile in-path OWASP CRS rule set: %w", err)
	}
	return waf, nil
}

func (i *Inspector) inlineWAF() (coraza.WAF, error) {
	e := &i.inlineEngine
	e.once.Do(func() {
		e.waf, e.err = compileInline()
		if e.err != nil {
			i.log.Error("edge waf in-path rule set failed to compile", "err", e.err)
		}
	})
	return e.waf, e.err
}

// CheckInline implements gateway.WAFInlineChecker: it scores the sample's
// headers and URI on the caller's goroutine. The body is ignored. A check
// over the app's inline budget or with no free slot is skipped so the WAF
// never queues requests or takes the gateway down; the caller lets the
// request through and counts the skip.
func (i *Inspector) CheckInline(s gateway.WAFSample) gateway.WAFInlineResult {
	if !i.admitInline(s.AppID) {
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineSkipped}
	}
	select {
	case i.inlineSlots <- struct{}{}:
	default:
		i.settleInline(s.AppID, 0)
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineSkipped}
	}
	defer func() { <-i.inlineSlots }()
	waf, err := i.inlineWAF()
	if err != nil {
		i.settleInline(s.AppID, 0)
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineError}
	}
	s.Body, s.BodyTruncated = nil, false
	start := time.Now()
	res, err := evaluate(waf, s)
	took := time.Since(start)
	i.settleInline(s.AppID, took)
	out := gateway.WAFInlineResult{Seconds: took.Seconds()}
	switch {
	case err != nil:
		out.Outcome = gateway.WAFInlineError
	case res.Detected:
		out.Outcome, out.RuleIDs, out.Categories = gateway.WAFInlineDetected, res.RuleIDs, res.Categories
	default:
		out.Outcome = gateway.WAFInlineClean
	}
	return out
}

func (i *Inspector) admitInline(appID string) bool {
	i.inlineMu.Lock()
	defer i.inlineMu.Unlock()
	return i.inlineBudgets.admit(appID, i.now(), estimateInlineMs)
}

func (i *Inspector) settleInline(appID string, took time.Duration) {
	i.inlineMu.Lock()
	defer i.inlineMu.Unlock()
	i.inlineBudgets.settle(appID, i.now(), estimateInlineMs, took)
}
