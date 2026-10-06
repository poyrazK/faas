package eventdelivery

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/events"
)

func TestConsumerProtectsControlsAndPreservesDuplicateEvidence(t *testing.T) {
	consumer, err := NewConsumer("control-token")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(consumer)
	defer server.Close()
	g := &gate{cfg: Config{APIURL: "http://api.invalid", ControlToken: "control-token"}, client: server.Client()}
	p := Preparation{Source: "acceptance.events.delivery", EventID: "fixture-event", EventType: "delivery.recovery", SubscriptionID: uuid.NewString(), Fail: true}
	data, _ := json.Marshal(p)
	resp, err := server.Client().Post(server.URL+"/__gate/events", "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized control status=%d", resp.StatusCode)
	}
	if err := g.control(t.Context(), server.URL, "POST", "/__gate/events", p, nil); err != nil {
		t.Fatal(err)
	}
	if err := g.control(t.Context(), server.URL, "POST", "/__gate/events", p, nil); err == nil {
		t.Fatal("identity reuse erased evidence")
	}
	envelope := events.Envelope{SpecVersion: events.CloudEventsSpecVersion, DataContentType: events.JSONDataContentType, ID: p.EventID, Source: p.Source, Type: p.EventType, AccountID: uuid.NewString(), Time: time.Now(), Data: json.RawMessage(`{}`)}
	body, _ := json.Marshal(envelope)
	id := uuid.NewString()
	deliver := func(withHeaders bool, want int) {
		t.Helper()
		req, _ := http.NewRequestWithContext(context.Background(), "POST", server.URL+"/", bytes.NewReader(body))
		if withHeaders {
			for name, value := range map[string]string{"X-Gregale-Event-Id": p.EventID, "X-Gregale-Event-Source": p.Source, "X-Gregale-Event-Type": p.EventType, "X-Gregale-Event-Subscription-Id": p.SubscriptionID, "X-Faas-Invocation-Id": id} {
				req.Header.Set(name, value)
			}
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatalf("delivery status=%d want %d", response.StatusCode, want)
		}
	}
	deliver(false, 422)
	deliver(true, 503)
	if err := g.control(t.Context(), server.URL, "POST", "/__gate/recover", p, nil); err != nil {
		t.Fatal(err)
	}
	deliver(true, 200)
	deliver(true, 200)
	var stats ConsumerStats
	if err := g.control(t.Context(), server.URL, "GET", "/__gate/events?source="+p.Source+"&event_id="+p.EventID, nil, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Attempts != 3 || stats.Failures != 1 || stats.SuccessfulResponses != 2 || stats.Effects != 1 {
		t.Fatalf("fixture lost duplicate/failure evidence: %+v", stats)
	}
	if err := verifyConsumer(stats, id, 2, 1); err == nil {
		t.Fatal("gate accepted duplicate delivery")
	}
}

func TestGateRejectsForeignActionsAndRedirects(t *testing.T) {
	for _, path := range []string{"https://foreign.invalid/v1/events", "//foreign.invalid/v1/events", "/__gate/recover", "/v1/events#fragment"} {
		if relativeAPIPath(path) == nil {
			t.Fatalf("accepted unsafe API action %q", path)
		}
	}
	var targetCalls atomic.Int64
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL+"/v1/events", http.StatusTemporaryRedirect)
	}))
	defer redirect.Close()
	cfg := Config{APIURL: redirect.URL, APIToken: "secret", ControlToken: "control", HealthyApp: "a", FailingApp: "b", HealthyURL: "http://healthy.invalid", FailingURL: "http://failing.invalid", Source: "source", EventID: "id", EventType: "type", RoutingMode: "recipient", ExpectedAttempts: 3}
	if _, err := Run(t.Context(), cfg, Hooks{}); err == nil {
		t.Fatal("gate followed redirect")
	}
	if targetCalls.Load() != 0 {
		t.Fatal("gate sent credentials to redirected host")
	}
}
