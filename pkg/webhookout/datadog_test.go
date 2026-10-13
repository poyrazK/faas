package webhookout

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"
)

func TestDatadogFormat_SendsEventBodyWithAPIKeyInsteadOfSignature(t *testing.T) {
	var gotHeader http.Header
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader = r.Header.Clone()
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	at := time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
	d := NewDispatcher(DispatcherOptions{
		MaxAttempts: 1, HTTPClient: srv.Client(), HeaderSet: HeaderSetWebhook, Format: DeliveryFormatDatadog,
	})
	res := d.Dispatch(context.Background(), Target{URL: srv.URL, Signer: NewSigner([]byte("dd-key-123"))}, Event{
		ID: "delivery-1", OccurredAt: at, AppID: "app-1", Service: "Shop",
		Type: "rollout.aborted",
		Data: json.RawMessage(`{"deployment_id":"0123456789abcdef0123456789abcdef","reason":"5xx rate"}`),
	})
	if res.Err != nil {
		t.Fatalf("dispatch: %v", res.Err)
	}
	if gotHeader.Get(datadogAPIKeyHeader) != "dd-key-123" {
		t.Fatalf("DD-API-KEY = %q", gotHeader.Get(datadogAPIKeyHeader))
	}
	if gotHeader.Get("X-Faas-Webhook-Signature") != "" {
		t.Fatal("a datadog delivery must not carry a Gregale signature")
	}
	var body datadogEvent
	if err := json.Unmarshal(gotBody, &body); err != nil {
		t.Fatalf("body %s: %v", gotBody, err)
	}
	if body.Title != "[Shop] rollout.aborted" || body.AlertType != "warning" || body.DateHappened != at.Unix() {
		t.Fatalf("event = %+v", body)
	}
	if body.Text != "Rollout of deployment 01234567 aborted: 5xx rate." || body.AggregationKey != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("text/aggregation = %q / %q", body.Text, body.AggregationKey)
	}
	for _, want := range []string{"source:gregale", "event:rollout.aborted", "service:shop", "deployment_id:0123456789abcdef0123456789abcdef"} {
		if !slices.Contains(body.Tags, want) {
			t.Errorf("tags %v missing %q", body.Tags, want)
		}
	}
}

func TestMarshalDatadogEvent_AlertTypes(t *testing.T) {
	for event, want := range map[string]string{
		"deployment.live": "success", "deployment.failed": "error",
		"rollout.completed": "success", "rollout.aborted": "warning", "app.parked": "info",
	} {
		body, err := marshalDatadogEvent(Event{Type: event, AppID: "app-1"})
		if err != nil {
			t.Fatal(err)
		}
		var got datadogEvent
		_ = json.Unmarshal(body, &got)
		if got.AlertType != want {
			t.Errorf("%s alert_type = %q, want %q", event, got.AlertType, want)
		}
	}
}
