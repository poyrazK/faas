package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/eventdelivery"
)

// adr: 618
// Real APID, schedd, gateway and PostgreSQL deliver to two actual HTTP
// applications. Only VMMD's VM transport is replaced; no invocation lifecycle
// state is fabricated. The same gate and consumer run against staging below.
func TestE2E_EventDeliveryRecovery_WholeReceipt(t *testing.T) {
	runEventDeliveryRecovery(t, false)
}

func TestE2E_EventDeliveryRecovery_IndependentRecipients(t *testing.T) {
	runEventDeliveryRecovery(t, true)
}

func runEventDeliveryRecovery(t *testing.T, independent bool) {
	t.Helper()
	if runtime.GOOS != "linux" {
		t.Skip("process delivery acceptance requires Linux capability introspection; no KVM is needed")
	}
	flag, mode := "0", "event"
	if independent {
		flag, mode = "1", "recipient"
	}
	f := newNormalPathFixtureWithPlanAndEnv(t, "event-gate-"+mode, api.PlanHobby, "FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED="+flag)
	if f == nil {
		t.Fatal("event delivery gate requires PostgreSQL")
	}
	app := createEventFanoutApp(t, f.h, f.key, "event-gate-failing-"+mode)
	source, eventType, eventID := "acceptance.events.delivery", "delivery.recovery", "event-gate-"+mode
	account, err := f.store.AppByID(f.ctx, f.app.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, appID := range []string{f.app.ID, app.ID} {
		if _, _, err := f.store.UpsertEventSubscription(f.ctx, account.AccountID, appID, source, eventType, json.RawMessage(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	// Keep fixtures running throughout the restart and two real retry delays.
	for _, slug := range []string{f.app.Slug, app.Slug} {
		body, code := doReq(t, f.h, f.key, "PATCH", "/v1/apps/"+slug, map[string]any{"require_authn": false, "public_auth": map[string]any{"mode": "open"}, "idle_timeout_s": 120})
		if code != http.StatusOK {
			t.Fatalf("configure fixture %s: %d %s", slug, code, body)
		}
	}
	healthyURL := startEventGateConsumer(t, f, f.app.ID, f.app.Slug)
	failingURL := startEventGateConsumer(t, f, app.ID, app.Slug)
	if err := f.h.KillSchedd(); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cfg := eventdelivery.Config{APIURL: f.h.APIDURL, APIToken: f.key, ControlToken: "event-gate-test-token", HealthyApp: f.app.Slug, FailingApp: app.Slug,
		HealthyURL: healthyURL, FailingURL: failingURL, Source: source, EventID: eventID, EventType: eventType, RoutingMode: mode, ExpectedAttempts: api.MustLimitsFor(api.PlanHobby).MaxQueueAttempts}
	report, err := eventdelivery.Run(ctx, cfg, eventdelivery.Hooks{
		Accepted: func(context.Context) error {
			assertEventGatePendingBacklog(t, f, source, eventID)
			return f.h.RestartSchedd()
		},
		RetryPending: func(context.Context) error {
			// Disabling adoption must preserve existing recipient ownership.
			// The handler's retry schedule also survives an actual SIGKILL.
			if err := f.h.SetScheddEnv("FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED", "0"); err != nil {
				return err
			}
			return f.h.RestartSchedd()
		},
		DeadLetter: func(context.Context) error { return f.h.RestartAPID() },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate != "pass" {
		t.Fatalf("gate report: %+v", report)
	}
	foreign := f.h.SeedAccount(f.ctx, api.PlanHobby, "event-gate-foreign-"+mode)
	receiptURL := "/v1/events/receipt?" + url.Values{"source": {source}, "id": {eventID}}.Encode()
	for _, path := range []string{receiptURL, report.Receipt.Recipients[0].AttemptHistoryURL} {
		body, code := doReq(t, f.h, foreign, "GET", path, nil)
		if code != http.StatusNotFound {
			t.Fatalf("foreign receipt evidence %s: %d %s", path, code, body)
		}
	}
	t.Logf("mode=%s: healthy handler calls=%d; failing calls=%d; retained attempts=%d; process restart and selective recovery passed",
		mode, report.Healthy.Attempts, report.Recovered.Attempts, len(report.FailureHistory))
}

func startEventGateConsumer(t *testing.T, f *normalPathFixture, appID, slug string) string {
	t.Helper()
	_, instance := createNormalPathLiveDeployment(t, f, appID, "event-gate")
	f.vmmd.SetVersion(instance.ID, "event-gate")
	waitForNormalPathResponse(t, f.h, slug+".apps.test.example", "normal-path:event-gate\n", 10*time.Second)
	consumer, err := eventdelivery.NewConsumer("event-gate-test-token")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(consumer)
	t.Cleanup(server.Close)
	f.vmmd.SetForwardHandler(instance.ID, func(ctx context.Context, capture e2etest.RequestCapture) (e2etest.FakeResponse, error) {
		request, err := http.NewRequestWithContext(ctx, capture.Init.Method, server.URL+capture.Init.RequestUri, bytes.NewReader(capture.Body))
		if err != nil {
			return e2etest.FakeResponse{}, err
		}
		for _, header := range capture.Init.Headers {
			request.Header.Add(header.Name, header.Value)
		}
		response, err := server.Client().Do(request)
		if err != nil {
			return e2etest.FakeResponse{}, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return e2etest.FakeResponse{Status: response.StatusCode, Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: response.Header.Get("Content-Type")}}, Body: body}, err
	})
	return server.URL
}

func assertEventGatePendingBacklog(t *testing.T, f *normalPathFixture, source, eventID string) {
	t.Helper()
	body, code := doReq(t, f.h, f.key, "GET", "/v1/events/backlog", nil)
	if code != http.StatusOK {
		t.Fatalf("pending backlog: %d %s", code, body)
	}
	var backlog api.EventBacklogResponse
	if err := json.Unmarshal(body, &backlog); err != nil {
		t.Fatal(err)
	}
	if len(backlog.Recipients) != 2 || len(backlog.Consumers) != 2 || backlog.UnattributedReceipts != 0 {
		t.Fatalf("accepted event must expose both waiting consumers: %+v", backlog)
	}
	for _, r := range backlog.Recipients {
		if r.EventSource != source || r.EventID != eventID || r.State != "pending" || r.Attempts != 0 {
			t.Fatalf("incorrect pending recipient: %+v", r)
		}
	}
	for _, c := range backlog.Consumers {
		if c.WaitingRecipients != 1 || c.PendingRecipients != 1 || c.ProcessingRecipients != 0 {
			t.Fatalf("incorrect consumer aggregate: %+v", c)
		}
	}
}
