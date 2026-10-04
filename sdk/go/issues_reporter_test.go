package faas_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

func TestIssueReporterCapturesExceptionAndBoundedContext(t *testing.T) {
	type captured struct {
		Event faas.IssueEvent
		Auth  string
	}
	var got captured
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/apps/exports/issue-events" {
			t.Errorf("path = %q", r.URL.Path)
		}
		got.Auth = r.Header.Get("Authorization")
		if err := json.NewDecoder(r.Body).Decode(&got.Event); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: srv.URL, App: "exports", Token: "g_issue_test", HTTPClient: srv.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	defer reporter.Close(context.Background())

	eventID := reporter.CaptureException(errors.New("invalid format"), faas.IssueContext{
		RequestID: "request-123", TraceID: strings.Repeat("a", 32), Route: "/exports",
		SourceKind: "worker", InvocationID: "c4f3d348-8842-4eec-b6e4-9666096e5d48",
	})
	if eventID == "" {
		t.Fatal("CaptureException did not enqueue an event")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if got.Auth != "Bearer g_issue_test" {
		t.Fatalf("Authorization = %q", got.Auth)
	}
	if got.Event.EventID != eventID || got.Event.SourceKind != "worker" || got.Event.RequestID != "request-123" || got.Event.Route != "/exports" {
		t.Fatalf("captured event context = %+v", got.Event)
	}
	if got.Event.InvocationID != "c4f3d348-8842-4eec-b6e4-9666096e5d48" || got.Event.ExceptionType == "" || got.Event.Message != "invalid format" {
		t.Fatalf("captured event details = %+v", got.Event)
	}
	if len(got.Event.Frames) == 0 || len(got.Event.StackTrace) == 0 {
		t.Fatalf("stack evidence missing: %+v", got.Event)
	}
	if !strings.Contains(got.Event.Frames[0].Function, "TestIssueReporterCapturesExceptionAndBoundedContext") {
		t.Fatalf("first stack frame did not identify the application capture site: %+v", got.Event.Frames[0])
	}
	if strings.Contains(string(mustJSON(t, got.Event)), "tenant_id") || strings.Contains(string(mustJSON(t, got.Event)), "locals") {
		t.Fatalf("unexpected unbounded fields in event: %+v", got.Event)
	}
	if stats := reporter.Stats(); stats.Queued != 0 || stats.Accepted != 1 || stats.Dropped != 0 {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestIssueReporterRetriesIdenticalPayload(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	attempt := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		mu.Lock()
		bodies = append(bodies, body)
		attempt++
		current := attempt
		mu.Unlock()
		if current == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: srv.URL, App: "exports", Token: "g_issue_test", RetryDelay: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	eventID := reporter.CaptureException(errors.New("temporary outage"))
	if eventID == "" {
		t.Fatal("CaptureException did not enqueue an event")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Flush(ctx); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	if err := reporter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 {
		t.Fatalf("got %d attempts, want 2", len(bodies))
	}
	if string(bodies[0]) != string(bodies[1]) {
		t.Fatalf("retry payload changed:\nfirst %s\nsecond %s", bodies[0], bodies[1])
	}
	var event faas.IssueEvent
	if err := json.Unmarshal(bodies[0], &event); err != nil || event.EventID != eventID {
		t.Fatalf("retry event identity = %+v, err %v", event, err)
	}
}

func TestIssueReporterBoundsQueueAndReportsOverflow(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-entered:
		default:
			close(entered)
		}
		<-release
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: srv.URL, App: "exports", Token: "g_issue_test", MaxQueue: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reporter.CaptureException(errors.New("first")) == "" {
		t.Fatal("first event was not queued")
	}
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	if id := reporter.CaptureException(errors.New("overflow")); id != "" {
		t.Fatalf("overflow event unexpectedly queued with ID %s", id)
	}
	if stats := reporter.Stats(); stats.Queued != 1 || stats.Dropped != 1 {
		t.Fatalf("Stats before release = %+v", stats)
	}
	close(release)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if stats := reporter.Stats(); stats.Queued != 0 || stats.Accepted != 1 || stats.Dropped != 1 {
		t.Fatalf("Stats after close = %+v", stats)
	}
}

func TestIssueReporterRecoverAndRepanicPreservesValue(t *testing.T) {
	var event faas.IssueEvent
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Errorf("decode body: %v", err)
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{BaseURL: srv.URL, App: "exports", Token: "g_issue_test"})
	if err != nil {
		t.Fatal(err)
	}
	const panicValue = "original panic"
	func() {
		defer func() {
			if got := recover(); got != panicValue {
				t.Fatalf("recovered value = %#v, want %#v", got, panicValue)
			}
		}()
		defer reporter.RecoverAndRepanic(faas.IssueContext{RequestID: "panic-request"})
		panic(panicValue)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if event.ExceptionType != "panic" || event.Message != panicValue || event.RequestID != "panic-request" {
		t.Fatalf("panic event = %+v", event)
	}
	if len(event.Frames) == 0 || strings.Contains(event.Frames[0].Function, "RecoverAndRepanic") {
		t.Fatalf("panic stack starts inside reporter instead of application: %+v", event.Frames)
	}
	applicationFrame := false
	for _, frame := range event.Frames {
		if strings.Contains(frame.Function, "TestIssueReporterRecoverAndRepanicPreservesValue") {
			applicationFrame = true
			break
		}
	}
	if !applicationFrame {
		t.Fatalf("panic stack does not include its application boundary: %+v", event.Frames)
	}
}

func TestIssueReporterDoesNotFollowRedirects(t *testing.T) {
	var forwarded bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		forwarded = true
		w.WriteHeader(http.StatusAccepted)
	}))
	defer destination.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: redirect.URL, App: "exports", Token: "g_issue_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	reporter.CaptureException(errors.New("redirect"))
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if forwarded {
		t.Fatal("reporter followed a redirect and forwarded the ingest request")
	}
	if stats := reporter.Stats(); stats.Accepted != 0 || stats.Dropped != 1 {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestIssueReporterCloseDeadlineLeavesQueuedEventVisible(t *testing.T) {
	requestSeen := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case requestSeen <- struct{}{}:
		default:
		}
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: srv.URL, App: "exports", Token: "g_issue_test", RetryDelay: time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reporter.CaptureException(errors.New("offline")) == "" {
		t.Fatal("event was not queued")
	}
	select {
	case <-requestSeen:
	case <-time.After(time.Second):
		t.Fatal("delivery did not start")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := reporter.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Close error = %v, want deadline exceeded", err)
	}
	if stats := reporter.Stats(); stats.Queued != 1 || stats.Accepted != 0 {
		t.Fatalf("Stats after timed-out close = %+v", stats)
	}
	flushCtx, flushCancel := context.WithTimeout(context.Background(), time.Second)
	defer flushCancel()
	if err := reporter.Flush(flushCtx); !errors.Is(err, faas.ErrIssueReporterStopped) {
		t.Fatalf("Flush after stopped worker = %v, want ErrIssueReporterStopped", err)
	}
}

func TestIssueReporterContainsCustomTransportPanic(t *testing.T) {
	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: "https://api.example.test", App: "exports", Token: "g_issue_test",
		HTTPClient: &http.Client{Transport: panicRoundTripper{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reporter.CaptureException(errors.New("transport panic")) == "" {
		t.Fatal("event was not queued")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := reporter.Close(ctx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if stats := reporter.Stats(); stats.Queued != 0 || stats.Dropped != 1 {
		t.Fatalf("Stats = %+v", stats)
	}
}

func TestIssueReporterContainsPanickingErrorFormatter(t *testing.T) {
	reporter, err := faas.NewIssueReporter(faas.IssueReporterOptions{
		BaseURL: "https://api.example.test", App: "exports", Token: "g_issue_test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if id := reporter.CaptureException(panickingError{}); id != "" {
		t.Fatalf("panicking formatter unexpectedly queued event %s", id)
	}
	if stats := reporter.Stats(); stats.Queued != 0 || stats.Dropped != 1 {
		t.Fatalf("Stats = %+v", stats)
	}
	if err := reporter.Close(context.Background()); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestNewIssueReporterRejectsUnsafeConfiguration(t *testing.T) {
	cases := []faas.IssueReporterOptions{
		{BaseURL: "ftp://api.example.test", App: "exports", Token: "g_issue_test"},
		{BaseURL: "https://user:pass@api.example.test", App: "exports", Token: "g_issue_test"},
		{BaseURL: "https://api.example.test/path", App: "exports", Token: "g_issue_test"},
		{BaseURL: "https://api.example.test", App: "exports", Token: "owner-key"},
		{BaseURL: "https://api.example.test", Token: "g_issue_test"},
	}
	for _, options := range cases {
		if reporter, err := faas.NewIssueReporter(options); err == nil {
			_ = reporter.Close(context.Background())
			t.Errorf("NewIssueReporter(%+v) unexpectedly succeeded", options)
		}
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

type panicRoundTripper struct{}

func (panicRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	panic("custom transport panic")
}

type panickingError struct{}

func (panickingError) Error() string {
	panic("error formatter panic")
}
