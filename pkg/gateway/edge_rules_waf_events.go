package gateway

// ADR-831 amendment 5: kind=waf detections become ADR-964 security events,
// so a customer can see which request was flagged, by which CRS rules, and
// tune exclude_rule_ids. A WAF rule's plain matches are not events: every
// inspected request matches, and sampling them would crowd detections out.

import (
	"context"
	"strings"
	"time"
)

// Event outcomes for kind=waf detections, alongside "matched" and "logged".
const (
	// EdgeRuleEventDetected is a detection in an off-path sample (any mode).
	EdgeRuleEventDetected = "detected"
	// EdgeRuleEventWarned is an in-path detection by a warn rule.
	EdgeRuleEventWarned = "warned"
	// EdgeRuleEventBlocked is an in-path detection a block rule answered
	// with 403.
	EdgeRuleEventBlocked = "blocked"
)

// Bounds on a detection event's detail; edge_rule_events checks the same.
const (
	EdgeRuleEventMaxWAFRuleIDs    = 32
	EdgeRuleEventMaxWAFCategories = 16
	EdgeRuleEventMaxWAFScore      = 10000
)

// WAFEventRecorder keeps sampled detection events. SampleEdgeRuleDetection
// has a per-rule budget of its own, separate from match sampling, and both
// methods must be cheap and non-blocking: the in-path caller is on the
// request path.
type WAFEventRecorder interface {
	SampleEdgeRuleDetection(ruleID string) bool
	RecordEdgeRuleEvent(ev EdgeRuleEvent)
}

// WAFDetection is what an event records about a detection: the CRS rules
// that scored, their attack categories, and the anomaly score. Never the
// matched payload.
type WAFDetection struct {
	RuleIDs    []int
	Categories []string
	Score      int
}

// NewWAFDetectionEvent builds the security event for a detection from the
// gateway's sample of the request. The path keeps no query string, like
// every ADR-964 event.
func NewWAFDetectionEvent(s WAFSample, outcome string, d WAFDetection, at time.Time) EdgeRuleEvent {
	path, _, _ := strings.Cut(s.URI, "?")
	ev := EdgeRuleEvent{
		RuleID: s.RuleID, AppID: s.AppID, Outcome: outcome, At: at.UTC(),
		RequestID: truncateBytes(s.RequestID, 128),
		Method:    truncateBytes(s.Method, 16),
		Host:      truncateBytes(hostname(s.Host), 253),
		Path:      truncateBytes(path, EdgeRuleEventMaxPathBytes),
		ClientIP:  s.ClientIP,
		Country:   s.ResolveCountry(),
		WAFScore:  min(max(d.Score, 0), EdgeRuleEventMaxWAFScore),
	}
	if s.Header != nil {
		ev.UserAgent = truncateBytes(s.Header.Get("User-Agent"), EdgeRuleEventMaxUserAgentBytes)
	}
	ev.WAFRuleIDs = append([]int(nil), d.RuleIDs[:min(len(d.RuleIDs), EdgeRuleEventMaxWAFRuleIDs)]...)
	ev.WAFCategories = append([]string(nil), d.Categories[:min(len(d.Categories), EdgeRuleEventMaxWAFCategories)]...)
	return ev
}

// ResolveCountry returns the client's country for an event, resolving it at
// most once per request and only when a detection asks. Empty when unknown.
func (s WAFSample) ResolveCountry() string {
	if s.country == nil {
		return ""
	}
	return s.country()
}

// recordWAFEvent samples an in-path detection as a security event.
func (h *Handler) recordWAFEvent(sample WAFSample, outcome string, res WAFInlineResult) {
	rec, ok := h.edgeRuleHits.(WAFEventRecorder)
	if !ok || !rec.SampleEdgeRuleDetection(sample.RuleID) {
		return
	}
	rec.RecordEdgeRuleEvent(NewWAFDetectionEvent(sample, outcome,
		WAFDetection{RuleIDs: res.RuleIDs, Categories: res.Categories, Score: res.Score}, time.Now()))
}

// suppressEdgeRuleMatchEvents marks kind=waf: its matches are counted
// (ADR-960) but not sampled as events; detections are (see above).
func (EdgeRuleWAFResolved) suppressEdgeRuleMatchEvents() {}

// edgeRuleMatchEventsSuppressed is implemented by rule kinds whose plain
// matches are not security events on their own.
type edgeRuleMatchEventsSuppressed interface{ suppressEdgeRuleMatchEvents() }

// wafSampleCountry returns a lazy country resolver backed by the request's
// match context, so an off-path detection can name the country without the
// gateway looking it up for every matched request.
func wafSampleCountry(ctx context.Context) func() string {
	if m := edgeRuleMatchContextFrom(ctx); m != nil {
		return m.resolvedCountry
	}
	return nil
}
