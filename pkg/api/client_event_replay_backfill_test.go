package api

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

type eventBackfillItemsRoundTripper func(*http.Request) (*http.Response, error)

func (f eventBackfillItemsRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestListEventReplayBackfillItemsSerializesPageFilters(t *testing.T) {
	var query url.Values
	client := NewClient("https://example.invalid", "")
	client.http = &http.Client{Transport: eventBackfillItemsRoundTripper(func(r *http.Request) (*http.Response, error) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/event-replays/job-id/items" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
		}
		query = r.URL.Query()
		body := `{"job_id":"job-id","items":[{"event_source":"orders","event_id":"evt-1","event_type":"invoice.paid","accepted_at":"2026-10-06T12:00:00Z","state":"failed","attempts":2,"retryable":true,"updated_at":"2026-10-06T12:01:00Z"}],"next_after":"next"}`
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}
	page, err := client.ListEventReplayBackfillItems(context.Background(), "job-id", "failed", "cursor+/?", 17)
	if err != nil {
		t.Fatal(err)
	}
	want := url.Values{"state": {"failed"}, "after": {"cursor+/?"}, "limit": {"17"}}
	if !reflect.DeepEqual(query, want) || page.JobID != "job-id" || len(page.Items) != 1 || page.Items[0].EventID != "evt-1" || page.NextAfter != "next" {
		t.Fatalf("query=%v page=%+v", query, page)
	}
}
