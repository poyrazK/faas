package edgewaf

import (
	"fmt"
	"strings"
	"time"

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
	return compileInlineWith(engine)
}

func compileInlineWith(opts engineOptions) (coraza.WAF, error) {
	var rules strings.Builder
	for _, n := range inlineRuleFiles {
		fmt.Fprintf(&rules, "Include @owasp_crs/REQUEST-%s-*.conf\n", n)
	}
	directives := fmt.Sprintf(`Include @coraza.conf-recommended
%s
Include @crs-setup.conf.example
SecAction "id:900000,phase:1,pass,t:none,nolog,setvar:tx.blocking_paranoia_level=1"
%sSecRuleEngine DetectionOnly
SecAuditEngine Off
SecRequestBodyAccess Off
SecResponseBodyAccess Off`, opts.directive(), rules.String())
	waf, err := coraza.NewWAF(coraza.NewWAFConfig().WithRootFS(opts.rootFS()).WithDirectives(directives))
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
// over the app's inline budget, or that finds no slot within the slot wait,
// is skipped so the WAF never queues requests for long or takes the gateway
// down; the caller lets the request through and counts the skip. Seconds is
// the latency the check added, including any wait for a slot.
func (i *Inspector) CheckInline(s gateway.WAFSample) gateway.WAFInlineResult {
	if !i.admitInline(s.AppID) {
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineSkipped, SkipReason: gateway.WAFInlineSkipBudget}
	}
	start := time.Now()
	tenant := inlineTenant(s)
	if ok, overShare := i.inlineSlots.acquire(tenant, i.inlineWait); !ok {
		i.settleInline(s.AppID, 0)
		reason := gateway.WAFInlineSkipBusy
		if overShare {
			reason = gateway.WAFInlineSkipAccountShare
		}
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineSkipped, SkipReason: reason}
	}
	defer i.inlineSlots.release(tenant)
	waf, err := i.inlineWAF()
	if err != nil {
		i.settleInline(s.AppID, 0)
		return gateway.WAFInlineResult{Outcome: gateway.WAFInlineError}
	}
	s.Body, s.BodyTruncated = nil, false
	checkStart := time.Now()
	res, err := evaluate(waf, s)
	// The app's budget is charged the check's own time, not the wait.
	i.settleInline(s.AppID, time.Since(checkStart))
	out := gateway.WAFInlineResult{Seconds: time.Since(start).Seconds()}
	switch {
	case err != nil:
		out.Outcome = gateway.WAFInlineError
	case res.Detected:
		out.Outcome, out.RuleIDs, out.Categories, out.Score = gateway.WAFInlineDetected, res.RuleIDs, res.Categories, res.Score
	default:
		out.Outcome = gateway.WAFInlineClean
	}
	return out
}

// inlineTenant is whose share of the check slots a check counts against:
// the app's account, or the app alone when the gateway knows no account.
func inlineTenant(s gateway.WAFSample) string {
	if s.AccountID != "" {
		return s.AccountID
	}
	return "app:" + s.AppID
}

// InlineSlots is how many in-path checks may run at once.
func (i *Inspector) InlineSlots() int { return i.inlineSlots.size }

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
