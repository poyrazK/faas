package issues

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestIssueHandoffBoundsAndRedaction(t *testing.T) {
	p := api.IssueHandoff{SchemaVersion: 1, Type: "issue.handoff", Issue: api.Issue{Title: "failure alice@example.com"},
		Sample:  &api.IssueEvent{ExceptionType: "Failure", Message: "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456", StackTrace: strings.Repeat("<&", api.IssueStackMaxBytes), Frames: make([]api.IssueFrame, api.IssueMaxFrames)},
		Request: &api.IssueHandoffRequest{Spans: make([]api.IssueHandoffSpan, api.IssueHandoffMaxSpans+1)}}
	for i := range p.Sample.Frames {
		p.Sample.Frames[i] = api.IssueFrame{File: strings.Repeat("<", api.IssueMaxFrameBytes), Function: strings.Repeat("&", api.IssueMaxFrameBytes)}
	}
	for i := range p.Request.Spans {
		p.Request.Spans[i] = api.IssueHandoffSpan{Name: "query alice@example.com", SpanID: fmt.Sprint(i)}
	}
	raw, err := MarshalHandoff(p)
	if err != nil || len(raw) > api.IssueHandoffMaxBytes {
		t.Fatalf("unbounded packet: %d bytes, %v", len(raw), err)
	}
	if strings.Contains(string(raw), "alice@example.com") || strings.Contains(string(raw), "abcdefghijklmnopqrstuvwxyz123456") {
		t.Fatalf("sensitive evidence forwarded: %s", raw)
	}
	var out api.IssueHandoff
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Sample.Frames) > api.IssueHandoffMaxFrames || len(out.Request.Spans) > api.IssueHandoffMaxSpans || !out.Request.SpansTruncated || len(out.Gaps) == 0 {
		t.Fatalf("missing bounded evidence markers: %+v", out)
	}
	if len(p.Sample.Frames) != api.IssueMaxFrames || p.Sample.Message != "Authorization: Bearer abcdefghijklmnopqrstuvwxyz123456" {
		t.Fatal("builder mutated source evidence")
	}
}

func TestIssueHandoffSpanProjection(t *testing.T) {
	spans, truncated, gap := HandoffSpans(json.RawMessage(`[{"span_id":"a","name":"query","duration_nanos":4,"db_statement":"secret SQL","attributes":{"authorization":"secret token"}},{"span_id":"b","name":"worker","duration_nanos":8}]`))
	if truncated || gap != "" || len(spans) != 2 || spans[0].SpanID != "b" {
		t.Fatalf("span projection = %+v %t %q", spans, truncated, gap)
	}
	raw, _ := json.Marshal(spans)
	if strings.Contains(string(raw), "secret") || strings.Contains(string(raw), "attributes") || strings.Contains(string(raw), "db_statement") {
		t.Fatalf("raw span fields forwarded: %s", raw)
	}
	for _, tc := range []struct {
		raw []byte
		gap string
	}{{nil, "spans_unavailable"}, {[]byte(`null`), "spans_unavailable"}, {[]byte(`{}`), "span_summary_invalid"}, {[]byte(strings.Repeat(" ", api.IssueHandoffSpanSummaryMaxBytes+1)), "span_summary_too_large"}} {
		out, _, gap := HandoffSpans(tc.raw)
		if len(out) != 0 || gap != tc.gap {
			t.Fatalf("invalid summary: %+v %q want %q", out, gap, tc.gap)
		}
	}
}

func TestIssueHandoffEscapedPayloadOverflow(t *testing.T) {
	p := api.IssueHandoff{Sample: &api.IssueEvent{
		Message:    strings.Repeat("<", api.IssueMessageMaxBytes),
		StackTrace: strings.Repeat("<", api.IssueHandoffStackMaxBytes),
		Frames:     make([]api.IssueFrame, api.IssueHandoffMaxFrames),
	}, Request: &api.IssueHandoffRequest{Spans: make([]api.IssueHandoffSpan, api.IssueHandoffMaxSpans)}}
	for i := range p.Sample.Frames {
		p.Sample.Frames[i] = api.IssueFrame{File: strings.Repeat("<", api.IssueMaxFrameBytes), Function: strings.Repeat("<", api.IssueMaxFrameBytes)}
	}
	for i := range p.Request.Spans {
		s := strings.Repeat("<", api.IssueMaxTypeBytes)
		p.Request.Spans[i] = api.IssueHandoffSpan{SpanID: s, ParentSpanID: s, Name: s, Kind: s, Status: s}
	}
	raw, err := MarshalHandoff(p)
	if err != nil || len(raw) > api.IssueHandoffMaxBytes {
		t.Fatalf("overflow: %d %v", len(raw), err)
	}
	var out api.IssueHandoff
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(out.Gaps, "payload_truncated") || out.Sample.StackTrace != "" || len(out.Sample.Frames) != 0 || len(out.Request.Spans) != 0 || !out.Request.SpansTruncated {
		t.Fatalf("silent payload loss: %+v", out)
	}
}
