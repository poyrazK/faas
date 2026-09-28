package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"testing"
)

func TestListEventDeliveriesPageByEventIdentitySerializesFilters(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/payments/event-deliveries" {
			t.Errorf("request = %s %s, want GET /v1/apps/payments/event-deliveries", r.Method, r.URL.Path)
		}
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"app_slug":"payments","deliveries":[],"fanout_failures":[]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	_, err := client.ListEventDeliveriesPageByEventIdentity(context.Background(), "payments", "orders.us", "evt/42", "failed", "delivery-cursor", "fanout-cursor", 17)
	if err != nil {
		t.Fatalf("list deliveries: %v", err)
	}
	want := url.Values{
		"event_source":  {"orders.us"},
		"event_id":      {"evt/42"},
		"state":         {"failed"},
		"before":        {"delivery-cursor"},
		"fanout_before": {"fanout-cursor"},
		"limit":         {"17"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query = %v, want %v", got, want)
	}
}

func TestListEventFanoutAttemptHistorySerializesEventAndRecipientFilters(t *testing.T) {
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/payments/event-deliveries/attempts" {
			t.Errorf("request = %s %s, want GET /v1/apps/payments/event-deliveries/attempts", r.Method, r.URL.Path)
		}
		got = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"app_slug":"payments","event_source":"orders.us","event_id":"evt/42","history":[]}`))
	}))
	defer srv.Close()

	client := NewClient(srv.URL, "")
	_, err := client.ListEventFanoutAttemptHistory(context.Background(), "payments", "orders.us", "evt/42", "sub-1", "history-cursor", 17)
	if err != nil {
		t.Fatalf("list fanout history: %v", err)
	}
	want := url.Values{
		"event_source":    {"orders.us"},
		"event_id":        {"evt/42"},
		"subscription_id": {"sub-1"},
		"before":          {"history-cursor"},
		"limit":           {"17"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("query = %v, want %v", got, want)
	}
}
