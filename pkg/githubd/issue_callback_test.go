package githubd

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/issuetracker"
)

type issueCallbackStoreFake struct {
	event issuetracker.CallbackEvent
	calls int
	fail  bool
}

func (s *issueCallbackStoreFake) KnownIssueTrackerCallback(context.Context, issuetracker.CallbackEvent) (bool, error) {
	return false, nil
}
func (s *issueCallbackStoreFake) EnqueueIssueTrackerCallback(_ context.Context, e issuetracker.CallbackEvent) (string, error) {
	s.calls++
	if s.fail {
		return "", errors.New("storage unavailable")
	}
	s.event = e
	return "accepted", nil
}
func (s *issueCallbackStoreFake) ProcessIssueTrackerCallback(context.Context) (bool, error) {
	return false, nil
}
func (s *issueCallbackStoreFake) CleanupIssueTrackerCallbacks(context.Context) error { return nil }
func TestIssueCallbacksUseSignedDurableInboxAndRetryFailedScheduling(t *testing.T) {
	svc := newRecording(t)
	callbacks := &issueCallbackStoreFake{}
	svc.IssueCallbacks = callbacks
	server := newServerUnderTest(t, svc)
	server.WebhookSecret = []byte("secret")
	inbox := &recordingDeliveryStore{}
	server.Deliveries = inbox
	body := []byte(`{"action":"closed","installation":{"id":42},"repository":{"id":100,"full_name":"owner/repo"},"issue":{"number":42,"html_url":"https://github.com/owner/repo/issues/42"}}`)
	for _, valid := range []bool{false, true} {
		req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(body))
		req.Header.Set("X-GitHub-Event", "issues")
		req.Header.Set("X-GitHub-Delivery", "delivery")
		if valid {
			req.Header.Set("X-Hub-Signature-256", SignForTest(body, server.WebhookSecret))
		}
		out := httptest.NewRecorder()
		server.WebhookLoopbackHandler().ServeHTTP(out, req)
		if !valid && out.Code != http.StatusUnauthorized {
			t.Fatal("unsigned callback accepted")
		}
		if valid && out.Code != http.StatusAccepted {
			t.Fatal(out.Code, out.Body.String())
		}
	}
	if len(inbox.deliveries) != 1 || callbacks.calls != 0 || inbox.deliveries[0].InstallationID != 42 {
		t.Fatal("inbox was bypassed")
	}
	callbacks.fail = true
	if err := svc.HandleWebhookDelivery(t.Context(), inbox.deliveries[0]); err == nil {
		t.Fatal("failed queue write acknowledged as success")
	}
	callbacks.fail = false
	if err := svc.HandleWebhookDelivery(t.Context(), inbox.deliveries[0]); err != nil || callbacks.event.TicketID != "42" {
		t.Fatal(callbacks.event, err)
	}
}

type issueCallbackResolverFake struct{}

func (issueCallbackResolverFake) Resolve(context.Context, int64) ([]byte, error) {
	return []byte("secret"), nil
}
func (issueCallbackResolverFake) Invalidate(int64) {}
func TestIssueCallbackRejectsSecretInstallationAndPayloadInstallationMismatch(t *testing.T) {
	server := newServerUnderTest(t, newRecording(t))
	server.SecretResolver = issueCallbackResolverFake{}
	inbox := &recordingDeliveryStore{}
	server.Deliveries = inbox
	body := []byte(`{"action":"closed","installation":{"id":42},"repository":{"id":100,"full_name":"owner/repo"},"issue":{"number":42,"html_url":"https://github.com/owner/repo/issues/42"}}`)
	req := httptest.NewRequest("POST", "/webhooks/github", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "issues")
	req.Header.Set("X-GitHub-Delivery", "delivery")
	req.Header.Set("X-GitHub-Installation-id", "43")
	req.Header.Set("X-Hub-Signature-256", SignForTest(body, []byte("secret")))
	out := httptest.NewRecorder()
	server.WebhookLoopbackHandler().ServeHTTP(out, req)
	if out.Code != 401 || len(inbox.deliveries) != 0 {
		t.Fatal("secret scope was bypassed", out.Code)
	}
}
