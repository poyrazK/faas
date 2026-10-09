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
}

// PickFirstWAFMatch returns the first priority-ordered WAF rule matching the
// request path, method, and optional header conditions.
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
			if ok, _ := pathGlobMatch(rule.PathGlob, requestPath); !ok {
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
// finished with it. Header is a clone and Body is at most
// api.EdgeWAFInspectBodyBytes; neither aliases live request state.
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

// WithWAFInspector arms observe-only kind=waf inspection.
func (h *Handler) WithWAFInspector(inspector WAFInspector) *Handler {
	h.wafInspector = inspector
	return h
}

// beginEdgeRuleWAF matches a kind=waf rule and, on a hit, records the body
// prefix while downstream code reads r.Body. The returned func submits the
// sample and must run once the request is finished (ServeHTTP defers it).
// Observe-only: this gate never writes a response and never reads the body
// itself, so it adds no latency to the forwarded request.
func (h *Handler) beginEdgeRuleWAF(r *http.Request, app App) func() {
	if h.edgeRules == nil || h.wafInspector == nil {
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
	var recorder *wafBodyRecorder
	if r.Body != nil && r.Body != http.NoBody && !isUpgradeRequest(r) {
		recorder = &wafBodyRecorder{ReadCloser: r.Body, limit: api.EdgeWAFInspectBodyBytes}
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
