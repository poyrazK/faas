package gateway

import (
	"context"
	"io"
	"net/http"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

// EdgeRuleWAFResolved is the compiled kind=waf matcher payload (ADR-831
// step 1). Scoring values are the effective ones stored by apid.
type EdgeRuleWAFResolved struct {
	EdgeRuleCondition
	ID               string
	AccountID        string
	AppID            string
	Priority         int
	PathGlob         string
	Methods          map[string]bool
	MatchHeaders     map[string]string
	ParanoiaLevel    int
	AnomalyThreshold int
	ExcludeRuleIDs   []int
	InspectBodyBytes int
	// Mode is observe, warn or block (api.EdgeWAFMode*).
	Mode string
}

// PickFirstWAFMatch returns the first priority-ordered WAF rule matching the
// request path, method, and optional header conditions. Paths match
// protectively, like the other gates that can deny a request: a block rule
// on /admin/* also covers /ADMIN/x and /public/../admin/x.
func PickFirstWAFMatch(rules []EdgeRuleWAFResolved, requestPath, method string, requestHeaders ...http.Header) *EdgeRuleWAFResolved {
	for i := range rules {
		rule := &rules[i]
		if len(requestHeaders) > 0 && !RequestHeaderConditionsMatch(rule.MatchHeaders, requestHeaders[0]) {
			continue
		}
		if rule.Methods != nil && !rule.Methods[method] {
			continue
		}
		if rule.PathGlob != "" {
			if ok, _ := protectivePathMatch(rule.PathGlob, requestPath); !ok {
				continue
			}
		}
		return rule
	}
	return nil
}

// WAFEdgeRuleMatcher is optional so existing matchers do not have to grow
// when the WAF kind is not wired.
type WAFEdgeRuleMatcher interface {
	MatchWAF(ctx context.Context, host, path, method string) *EdgeRuleWAFResolved
}

// WAFSample is one request handed to the inspector after the gateway has
// finished with it. Header is a clone and Body is at most the rule's
// InspectBodyBytes; neither aliases live request state.
type WAFSample struct {
	AppID            string
	AccountID        string
	RuleID           string
	RequestID        string
	ParanoiaLevel    int
	AnomalyThreshold int
	ExcludeRuleIDs   []int
	ClientIP         string
	Method           string
	Host             string
	URI              string
	Proto            string
	Header           http.Header
	Body             []byte
	BodyTruncated    bool
}

// WAFInspector evaluates samples off the request path. Submit must never
// block; an inspector that cannot take the sample drops and counts it.
type WAFInspector interface {
	Submit(WAFSample)
}

// WAF in-path check outcomes (ADR-831 amendment 4).
const (
	WAFInlineClean    = "clean"
	WAFInlineDetected = "detected"
	WAFInlineSkipped  = "skipped"
	WAFInlineError    = "error"
)

// WAFInlineResult is the verdict of an in-path header/URI check.
type WAFInlineResult struct {
	Outcome    string
	RuleIDs    []int
	Categories []string
	Seconds    float64
}

// WAFInlineChecker scores a sample's headers and URI on the request
// goroutine for warn and block rules. It must return promptly: a check it
// cannot afford is reported as WAFInlineSkipped and the request passes.
type WAFInlineChecker interface {
	CheckInline(WAFSample) WAFInlineResult
}

// WithWAFInspector arms off-path kind=waf inspection (every mode).
func (h *Handler) WithWAFInspector(inspector WAFInspector) *Handler {
	h.wafInspector = inspector
	return h
}

// WithWAFInlineChecker arms in-path header/URI checks for warn and block
// rules. Without it those rules behave like observe.
func (h *Handler) WithWAFInlineChecker(checker WAFInlineChecker) *Handler {
	h.wafInline = checker
	return h
}

// applyEdgeRuleWAF is the kind=waf gate (ADR-831). Warn and block rules first
// check headers and URI in-path: a detection under block is answered with
// 403 here (handled=true); under warn the response is tagged and the request
// passes. Otherwise, and for observe rules, the body prefix is recorded as
// later stages read it, and the returned func submits the sample off-path
// once the request is finished (ServeHTTP defers it). Bodies are never
// blocked.
func (h *Handler) applyEdgeRuleWAF(w http.ResponseWriter, r *http.Request, app App, rec *statusRecorder) (handled bool, submit func()) {
	rule := h.matchEdgeRuleWAF(r, app)
	if rule == nil {
		return false, nil
	}
	sample := newWAFSample(r, app, rule)
	if rule.Mode == api.EdgeWAFModeWarn || rule.Mode == api.EdgeWAFModeBlock {
		if detected := h.checkEdgeRuleWAFInline(rule, sample); detected {
			if rule.Mode == api.EdgeWAFModeBlock {
				api.WriteProblem(w, api.NewProblem(http.StatusForbidden, api.CodeForbidden, "Request blocked",
					"blocked by edge rule "+rule.ID+" (kind=waf)"))
				return true, nil
			}
			// The header names the edge rule, not the CRS rule, so the
			// response does not teach a caller which signature fired.
			rec.installHeaderOps([]EdgeRuleHeaderOp{{Action: "set", Name: "X-WAF-Warning", Value: rule.ID}})
			return false, nil
		}
	}
	return false, h.sampleEdgeRuleWAF(r, rule, sample)
}

func (h *Handler) matchEdgeRuleWAF(r *http.Request, app App) *EdgeRuleWAFResolved {
	if h.edgeRules == nil || (h.wafInspector == nil && h.wafInline == nil) {
		return nil
	}
	matcher, ok := h.edgeRules.(WAFEdgeRuleMatcher)
	if !ok {
		return nil
	}
	rule := matcher.MatchWAF(r.Context(), hostname(r.Host), r.URL.Path, r.Method)
	if rule == nil {
		h.metrics.ObserveEdgeRuleMatch("waf", "miss")
		return nil
	}
	if rule.AccountID != app.AccountID {
		h.metrics.ObserveEdgeRuleMatch("waf", "blocked")
		return nil
	}
	h.metrics.ObserveEdgeRuleMatch("waf", "match")
	return rule
}

func newWAFSample(r *http.Request, app App, rule *EdgeRuleWAFResolved) WAFSample {
	sample := WAFSample{
		AppID: app.ID, AccountID: app.AccountID, RuleID: rule.ID,
		RequestID:        requestIDFrom(r),
		ParanoiaLevel:    rule.ParanoiaLevel,
		AnomalyThreshold: rule.AnomalyThreshold,
		ExcludeRuleIDs:   rule.ExcludeRuleIDs,
		Method:           r.Method,
		Host:             r.Host,
		URI:              r.URL.RequestURI(),
		Proto:            r.Proto,
		Header:           r.Header.Clone(),
	}
	if ip, ok := clientIPFromTrustedXFF(r); ok {
		sample.ClientIP = ip.String()
	}
	return sample
}

// checkEdgeRuleWAFInline runs the in-path check and reports a detection. A
// skipped or failed check reports none: the WAF fails open.
func (h *Handler) checkEdgeRuleWAFInline(rule *EdgeRuleWAFResolved, sample WAFSample) bool {
	if h.wafInline == nil {
		return false
	}
	res := h.wafInline.CheckInline(sample)
	outcome := res.Outcome
	if outcome == WAFInlineDetected {
		outcome = "warned"
		if rule.Mode == api.EdgeWAFModeBlock {
			outcome = "blocked"
		}
		for _, c := range res.Categories {
			h.metrics.ObserveWAFDetection(sample.AppID, c)
		}
		for _, id := range res.RuleIDs {
			h.metrics.ObserveWAFRuleMatch(sample.AppID, id)
		}
	}
	h.metrics.ObserveWAFInline(sample.AppID, outcome, res.Seconds)
	return res.Outcome == WAFInlineDetected
}

// sampleEdgeRuleWAF wraps r.Body to record the inspected prefix and returns
// the func that submits the sample off-path.
func (h *Handler) sampleEdgeRuleWAF(r *http.Request, rule *EdgeRuleWAFResolved, sample WAFSample) func() {
	if h.wafInspector == nil {
		return nil
	}
	var recorder *wafBodyRecorder
	if r.Body != nil && r.Body != http.NoBody && !isUpgradeRequest(r) {
		limit := rule.InspectBodyBytes
		if limit < 1 || limit > api.MaxEdgeWAFInspectBodyBytes {
			limit = api.EdgeWAFDefaultInspectBodyBytes
		}
		recorder = &wafBodyRecorder{ReadCloser: r.Body, limit: limit}
		r.Body = recorder
	}
	inspector := h.wafInspector
	return func() {
		if recorder != nil {
			sample.Body, sample.BodyTruncated = recorder.snapshot()
		}
		inspector.Submit(sample)
	}
}

// wafBodyRecorder copies the first `limit` bytes that pass through Read. The
// proxy transport may read the body on its own goroutine, so the prefix is
// guarded by a mutex and copied out by snapshot.
type wafBodyRecorder struct {
	io.ReadCloser
	mu        sync.Mutex
	limit     int
	prefix    []byte
	truncated bool
}

func (b *wafBodyRecorder) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.mu.Lock()
		room := b.limit - len(b.prefix)
		switch {
		case room >= n:
			b.prefix = append(b.prefix, p[:n]...)
		case room > 0:
			b.prefix = append(b.prefix, p[:room]...)
			b.truncated = true
		default:
			b.truncated = true
		}
		b.mu.Unlock()
	}
	return n, err
}

func (b *wafBodyRecorder) snapshot() ([]byte, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]byte(nil), b.prefix...), b.truncated
}
