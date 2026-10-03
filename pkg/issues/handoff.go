package issues

import (
	"encoding/json"
	"fmt"
	"sort"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/redact"
)

var (
	handoffText    = redact.New(api.IssueMaxTypeBytes)
	handoffFrame   = redact.New(api.IssueMaxFrameBytes)
	handoffMessage = redact.New(api.IssueMessageMaxBytes)
	handoffStack   = redact.New(api.IssueHandoffStackMaxBytes)
)

func handoffGap(p *api.IssueHandoff, gap string) {
	for _, existing := range p.Gaps {
		if existing == gap {
			return
		}
	}
	p.Gaps = append(p.Gaps, gap)
}

// MarshalHandoff applies the same redactor as issue ingestion to every exported
// evidence string. It also bounds older persisted data and escaped JSON size.
func MarshalHandoff(p api.IssueHandoff) ([]byte, error) {
	p.Gaps = append([]string{}, p.Gaps...)
	clean := func(r *redact.Redactor, s string) string {
		out, names := r.Apply(s)
		for _, n := range names {
			if n == "truncated" {
				handoffGap(&p, "evidence_truncated")
			}
		}
		return out
	}
	p.Issue.Title = clean(handoffMessage, p.Issue.Title)
	p.Issue.Environment = clean(handoffText, p.Issue.Environment)
	p.Issue.Fingerprint = clean(handoffText, p.Issue.Fingerprint)
	details := make(map[string]string, len(p.Transition.Details))
	for k, v := range p.Transition.Details {
		details[k] = clean(handoffText, v)
	}
	p.Transition.Details = details
	if p.Sample != nil {
		e := *p.Sample
		e.FingerprintOverride = ""
		e.ExceptionType = clean(handoffText, e.ExceptionType)
		e.Message = clean(handoffMessage, e.Message)
		e.StackTrace = clean(handoffStack, e.StackTrace)
		e.RequestID = clean(handoffText, e.RequestID)
		e.Route = clean(handoffText, e.Route)
		// IDs and source kind are validated at ingestion; apply bounds to old
		// records as well, without forwarding arbitrary redaction labels.
		e.TraceID = clean(handoffText, e.TraceID)
		e.SpanID = clean(handoffText, e.SpanID)
		e.SourceKind = clean(handoffText, e.SourceKind)
		e.Redactions = nil
		frames := e.Frames
		if len(frames) > api.IssueHandoffMaxFrames {
			frames = frames[:api.IssueHandoffMaxFrames]
			handoffGap(&p, "frames_truncated")
		}
		e.Frames = append([]api.IssueFrame(nil), frames...)
		for i := range e.Frames {
			e.Frames[i].File = clean(handoffFrame, e.Frames[i].File)
			e.Frames[i].Function = clean(handoffFrame, e.Frames[i].Function)
		}
		p.Sample = &e
	}
	if p.Release != nil {
		r := *p.Release
		r.CommitSHA = clean(handoffText, r.CommitSHA)
		r.ImageDigest = clean(handoffText, r.ImageDigest)
		p.Release = &r
	}
	if p.Request != nil {
		r := *p.Request
		r.Route = clean(handoffText, r.Route)
		r.Method = clean(handoffText, r.Method)
		r.TraceID = clean(handoffText, r.TraceID)
		r.Spans = append([]api.IssueHandoffSpan{}, r.Spans...)
		if len(r.Spans) > api.IssueHandoffMaxSpans {
			r.Spans = r.Spans[:api.IssueHandoffMaxSpans]
			r.SpansTruncated = true
		}
		for i := range r.Spans {
			s := &r.Spans[i]
			s.SpanID = clean(handoffText, s.SpanID)
			s.ParentSpanID = clean(handoffText, s.ParentSpanID)
			s.Name = clean(handoffText, s.Name)
			s.Kind = clean(handoffText, s.Kind)
			s.Status = clean(handoffText, s.Status)
		}
		p.Request = &r
		if r.SpansTruncated {
			handoffGap(&p, "spans_truncated")
		}
	}
	sort.Strings(p.Gaps)
	raw, err := json.Marshal(p)
	if err != nil || len(raw) <= api.IssueHandoffMaxBytes {
		return raw, err
	}
	// JSON escaping can expand a bounded input sixfold. Drop large excerpts
	// together and make that loss explicit; preserve identity and references.
	if p.Sample != nil {
		p.Sample.StackTrace = ""
		p.Sample.Frames = nil
	}
	if p.Request != nil {
		p.Request.Spans = []api.IssueHandoffSpan{}
		p.Request.SpansTruncated = true
	}
	handoffGap(&p, "payload_truncated")
	sort.Strings(p.Gaps)
	raw, err = json.Marshal(p)
	if err == nil && len(raw) > api.IssueHandoffMaxBytes {
		return nil, fmt.Errorf("issue handoff metadata exceeds %d bytes", api.IssueHandoffMaxBytes)
	}
	return raw, err
}

// HandoffSpans projects only named fields. SQL, arbitrary attributes, and other
// raw span data are never decoded into the outbound payload.
func HandoffSpans(raw json.RawMessage) ([]api.IssueHandoffSpan, bool, string) {
	if len(raw) == 0 || string(raw) == "null" {
		return []api.IssueHandoffSpan{}, false, "spans_unavailable"
	}
	if len(raw) > api.IssueHandoffSpanSummaryMaxBytes {
		return []api.IssueHandoffSpan{}, true, "span_summary_too_large"
	}
	var spans []api.IssueHandoffSpan
	if err := json.Unmarshal(raw, &spans); err != nil {
		return []api.IssueHandoffSpan{}, false, "span_summary_invalid"
	}
	sort.SliceStable(spans, func(i, j int) bool {
		if spans[i].DurationNanos == spans[j].DurationNanos {
			return spans[i].SpanID < spans[j].SpanID
		}
		return spans[i].DurationNanos > spans[j].DurationNanos
	})
	truncated := len(spans) > api.IssueHandoffMaxSpans
	if truncated {
		spans = spans[:api.IssueHandoffMaxSpans]
	}
	if len(spans) == 0 {
		return []api.IssueHandoffSpan{}, false, "spans_unavailable"
	}
	return spans, truncated, ""
}
