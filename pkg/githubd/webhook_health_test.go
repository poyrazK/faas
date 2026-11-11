package githubd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// A GitHub App subscribed to more events than githubd routes must not see
// failed deliveries for them: 4xx here showed as red rows in the App's
// delivery log and would trip the delivery-health alert.
func TestServerWebhook_UnroutedEventIsAcceptedAsIgnored(t *testing.T) {
	svc := newRecording(t)
	deliveries := &recordingDeliveryStore{}
	secret := []byte("github-app-webhook-secret")
	s := newServerUnderTest(t, svc)
	s.WebhookSecret = secret
	s.Deliveries = deliveries
	body := []byte(`{"action":"completed"}`)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-Hub-Signature-256", webhookSignature(body, secret))
	req.Header.Set("X-GitHub-Event", "check_suite")
	req.Header.Set("X-GitHub-Delivery", "delivery-check-suite")
	rr := httptest.NewRecorder()
	s.WebhookLoopbackHandler().ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted || !strings.Contains(rr.Body.String(), `"ignored"`) {
		t.Fatalf("status/body = %d %q, want 202 ignored", rr.Code, rr.Body.String())
	}
	if len(deliveries.deliveries) != 0 {
		t.Fatalf("deliveries = %+v, want none enqueued", deliveries.deliveries)
	}
	if got := scrape(t, s); !strings.Contains(got, `githubd_test_ops_total{code="ignored",op="webhook_push"} 1`) {
		t.Fatalf("metrics missing ignored code:\n%s", got)
	}
}

// Each refusal is counted under a code that names its fix, and logged with
// the GitHub delivery identity at most once a minute per code. The secret
// mismatch that hid prod's webhooks for weeks was one silent 401 per push.
func TestServerWebhook_RejectionsAreCodedAndLoggedOncePerCode(t *testing.T) {
	var logs bytes.Buffer
	svc := NewService(slog.New(slog.NewJSONHandler(&logs, nil)))
	secret := []byte("github-app-webhook-secret")
	s := newServerUnderTest(t, svc)
	s.WebhookSecret = secret
	s.Deliveries = &recordingDeliveryStore{}
	body := []byte(`{"action":"opened"}`)

	post := func(sig, delivery string) int {
		req := httptest.NewRequest(http.MethodPost, "/webhooks/github", bytes.NewReader(body))
		req.Header.Set("X-Hub-Signature-256", sig)
		req.Header.Set("X-GitHub-Event", "pull_request")
		req.Header.Set("X-GitHub-Delivery", delivery)
		req.Header.Set("X-GitHub-Hook-ID", "5123")
		rr := httptest.NewRecorder()
		s.WebhookLoopbackHandler().ServeHTTP(rr, req)
		return rr.Code
	}
	if got := post(webhookSignature(body, []byte("stale-secret")), "delivery-a"); got != http.StatusUnauthorized {
		t.Fatalf("stale secret status = %d, want 401", got)
	}
	if got := post(webhookSignature(body, []byte("stale-secret")), "delivery-b"); got != http.StatusUnauthorized {
		t.Fatalf("second stale secret status = %d, want 401", got)
	}
	if got := post(webhookSignature(body, secret), ""); got != http.StatusBadRequest {
		t.Fatalf("missing delivery id status = %d, want 400", got)
	}

	metrics := scrape(t, s)
	for _, want := range []string{
		`githubd_test_ops_total{code="signature_invalid",op="webhook_push"} 2`,
		`githubd_test_ops_total{code="missing_metadata",op="webhook_push"} 1`,
	} {
		if !strings.Contains(metrics, want) {
			t.Errorf("metrics missing %q:\n%s", want, metrics)
		}
	}

	var lines []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(logs.String()), "\n") {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if entry["msg"] == "githubd: webhook delivery rejected" {
			lines = append(lines, entry)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("rejection log lines = %d (%v), want one per code", len(lines), lines)
	}
	first := lines[0]
	if first["code"] != webhookRejectSignature || first["delivery_id"] != "delivery-a" ||
		first["event"] != "pull_request" || first["hook_id"] != "5123" || first["status"] != float64(http.StatusUnauthorized) {
		t.Fatalf("first rejection log = %v", first)
	}
	if strings.Contains(logs.String(), "sha256=") || strings.Contains(logs.String(), `\"action\"`) {
		t.Fatalf("rejection log leaked the signature or body: %s", logs.String())
	}
}

func TestWebhookRejectLog_OncePerCodePerInterval(t *testing.T) {
	var l webhookRejectLog
	now := time.Unix(1_700_000_000, 0)
	steps := []struct {
		code  string
		after time.Duration
		want  bool
	}{
		{webhookRejectSignature, 0, true},
		{webhookRejectSignature, 10 * time.Second, false},
		{webhookRejectMetadata, 10 * time.Second, true},
		{webhookRejectSignature, webhookRejectLogEvery, true},
		{webhookRejectSignature, webhookRejectLogEvery + time.Second, false},
	}
	for i, step := range steps {
		if got := l.allow(step.code, now.Add(step.after)); got != step.want {
			t.Fatalf("step %d allow(%s, +%s) = %v, want %v", i, step.code, step.after, got, step.want)
		}
	}
}

// scriptedDeliveryStore hands out a fixed set of deliveries once and records
// how the worker settled each. It cancels the worker when the queue drains.
type scriptedDeliveryStore struct {
	mu        sync.Mutex
	queue     []WebhookDelivery
	cancel    context.CancelFunc
	completed []string
	failed    map[string]bool // delivery ID -> dead
}

func (s *scriptedDeliveryStore) Enqueue(context.Context, WebhookDelivery) (bool, error) {
	return false, errors.New("not used")
}

func (s *scriptedDeliveryStore) Claim(context.Context) (WebhookDelivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.queue) == 0 {
		s.cancel()
		return WebhookDelivery{}, ErrNoWebhookDelivery
	}
	next := s.queue[0]
	s.queue = s.queue[1:]
	return next, nil
}

func (s *scriptedDeliveryStore) Complete(_ context.Context, deliveryID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = append(s.completed, deliveryID)
	return nil
}

func (s *scriptedDeliveryStore) Fail(_ context.Context, deliveryID, _ string, _ time.Time, dead bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failed[deliveryID] = dead
	return nil
}

func (s *scriptedDeliveryStore) Prune(context.Context, time.Time) error { return nil }

// A payload that cannot be decoded or routed fails identically on every
// attempt. It is dead on the first one, so the dead-delivery alert fires
// within minutes instead of after the eight-attempt backoff. A pull_request
// action previews do not use is an ignored delivery, not a failure.
func TestRunWebhookDeliveryWorker_SettlesUnprocessableDeliveriesAtOnce(t *testing.T) {
	labeled := strings.Replace(validPRBody, `"action": "opened"`, `"action": "labeled"`, 1)
	if labeled == validPRBody {
		t.Fatal("validPRBody no longer carries the opened action")
	}
	missingHead := strings.ReplaceAll(validPRBody, "0123456789abcdef0123456789abcdef01234567", "abc")
	if missingHead == validPRBody {
		t.Fatal("validPRBody no longer carries its head SHA")
	}
	deliveries := []WebhookDelivery{
		{DeliveryID: "pr-labeled", EventType: "pull_request", Payload: []byte(labeled), Attempts: 1},
		{DeliveryID: "pr-bad-json", EventType: "pull_request", Payload: []byte(`{"action":`), Attempts: 1},
		{DeliveryID: "pr-missing-head", EventType: "pull_request", Payload: []byte(missingHead), Attempts: 1},
		{DeliveryID: "push-missing-fields", EventType: "push", Payload: []byte(`{"ref":"refs/heads/main"}`), Attempts: 1},
		{DeliveryID: "unrouted", EventType: "check_suite", Payload: []byte(`{}`), Attempts: 1},
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	store := &scriptedDeliveryStore{queue: deliveries, cancel: cancel, failed: map[string]bool{}}
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))

	done := make(chan struct{})
	go func() {
		RunWebhookDeliveryWorker(ctx, store, svc, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("worker did not drain the scripted deliveries")
	}

	if len(store.completed) != 1 || store.completed[0] != "pr-labeled" {
		t.Fatalf("completed = %v, want only the labeled pull_request", store.completed)
	}
	for _, id := range []string{"pr-bad-json", "pr-missing-head", "push-missing-fields", "unrouted"} {
		dead, ok := store.failed[id]
		if !ok || !dead {
			t.Errorf("delivery %s: failed=%v dead=%v, want dead on the first attempt", id, ok, dead)
		}
	}
}

// Non-preview actions are ignored deliveries: the sentinel lets the worker
// complete them instead of dead-lettering every labeled or edited PR.
func TestDecodePullRequest_UnknownActionIsIgnoredSentinel(t *testing.T) {
	body := strings.Replace(validPRBody, `"action": "opened"`, `"action": "labeled"`, 1)
	_, err := DecodePullRequest([]byte(body))
	if !errors.Is(err, ErrPullRequestActionIgnored) {
		t.Fatalf("err = %v, want ErrPullRequestActionIgnored", err)
	}
}

// A retryable error keeps its backoff budget: only payload faults skip it.
func TestHandleWebhookEvent_MarksOnlyPayloadFaultsUnprocessable(t *testing.T) {
	svc := NewService(slog.New(slog.NewTextHandler(io.Discard, nil)))
	labeled := strings.Replace(validPRBody, `"action": "opened"`, `"action": "edited"`, 1)
	if err := svc.HandleWebhookEvent(context.Background(), "pull_request", []byte(labeled)); !IsIgnored(err) {
		t.Fatalf("edited pull_request err = %v, want ignored", err)
	}
	if err := svc.HandleWebhookEvent(context.Background(), "workflow_run", []byte(`{}`)); !errors.Is(err, errUnprocessableWebhook) {
		t.Fatalf("unrouted event err = %v, want unprocessable", err)
	}
	if err := svc.HandleWebhookEvent(context.Background(), "installation", []byte(`not json`)); !errors.Is(err, errUnprocessableWebhook) {
		t.Fatalf("undecodable installation err = %v, want unprocessable", err)
	}
	if err := svc.HandleWebhookEvent(context.Background(), "issues", []byte(`not json`)); !errors.Is(err, errUnprocessableWebhook) {
		t.Fatalf("undecodable issues err = %v, want unprocessable", err)
	}
}
