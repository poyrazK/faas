package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestManagedRealtimeRetainedMessagesPersistAndReportGap(t *testing.T) {
	e := setup(t, api.PlanPro)
	endpointID := createRealtimeEndpointForTest(t, e)
	path := "/v1/apps/rt-actions/realtime/endpoints/" + endpointID + "/channels/updates/retained-messages"
	request := api.ManagedRealtimeRetainedMessageRequest{
		DataBase64: base64.StdEncoding.EncodeToString([]byte("first")), IdempotencyKey: "one",
	}
	if disabled := e.do(t, http.MethodPost, path, request, nil); disabled.Code != http.StatusNotFound {
		t.Fatalf("preview-disabled append = %d %s", disabled.Code, disabled.Body)
	}
	if disabled := e.do(t, http.MethodGet, path+"?after=0", nil, nil); disabled.Code != http.StatusNotFound {
		t.Fatalf("preview-disabled read = %d %s", disabled.Code, disabled.Body)
	}
	e.s.WithRealtimeHistoryPreviewEnabled(true)
	first := e.do(t, http.MethodPost, path, request, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("append = %d %s", first.Code, first.Body)
	}
	var message api.ManagedRealtimeRetainedMessageResponse
	if err := json.Unmarshal(first.Body.Bytes(), &message); err != nil || message.Sequence != 1 {
		t.Fatalf("first message = %+v, %v", message, err)
	}
	duplicate := e.do(t, http.MethodPost, path, request, nil)
	if duplicate.Code != http.StatusCreated {
		t.Fatalf("duplicate = %d %s", duplicate.Code, duplicate.Body)
	}
	if err := json.Unmarshal(duplicate.Body.Bytes(), &message); err != nil || message.Sequence != 1 {
		t.Fatalf("duplicate message = %+v, %v", message, err)
	}
	request.DataBase64 = base64.StdEncoding.EncodeToString([]byte("different"))
	if conflict := e.do(t, http.MethodPost, path, request, nil); conflict.Code != http.StatusConflict {
		t.Fatalf("conflicting key = %d %s", conflict.Code, conflict.Body)
	}
	for i := 2; i <= state.ManagedRealtimeHistoryMaxMessages+1; i++ {
		_, err := e.store.AppendManagedRealtimeChannelMessage(
			t.Context(), endpointID, "updates", []byte(fmt.Sprintf("m%d", i)), false, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	gap := e.do(t, http.MethodGet, path+"?after=0", nil, nil)
	if gap.Code != http.StatusGone || problemCode(t, gap) != "history_unavailable" {
		t.Fatalf("expired cursor = %d %s", gap.Code, gap.Body)
	}
	page := e.do(t, http.MethodGet, path+"?after=1&limit=2", nil, nil)
	if page.Code != http.StatusOK {
		t.Fatalf("history = %d %s", page.Code, page.Body)
	}
	var history api.ManagedRealtimeRetainedHistoryResponse
	if err := json.Unmarshal(page.Body.Bytes(), &history); err != nil {
		t.Fatal(err)
	}
	if history.OldestSequence != 2 || history.LatestSequence != 1025 || !history.HasMore || len(history.Messages) != 2 || history.Messages[0].Sequence != 2 {
		t.Fatalf("history = %+v", history)
	}
}

func TestManagedRealtimeHistoryUsagePreview(t *testing.T) {
	e := setup(t, api.PlanPro)
	path := "/v1/account/realtime-history-usage"
	if response := e.do(t, http.MethodGet, path, nil, nil); response.Code != http.StatusNotFound {
		t.Fatalf("preview disabled = %d %s", response.Code, response.Body)
	}
	e.s.WithRealtimeHistoryPreviewEnabled(true)
	endpointID := createRealtimeEndpointForTest(t, e)
	if _, err := e.store.AppendManagedRealtimeChannelMessage(t.Context(), endpointID, "updates", []byte("payload"), false, ""); err != nil {
		t.Fatal(err)
	}
	response := e.do(t, http.MethodGet, path, nil, nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("usage = %d %s headers=%v", response.Code, response.Body, response.Header())
	}
	var usage api.ManagedRealtimeHistoryUsageResponse
	if err := json.Unmarshal(response.Body.Bytes(), &usage); err != nil {
		t.Fatal(err)
	}
	if usage.ObservedAt == "" || usage.EndpointCount != 1 || usage.ChannelCount != 1 ||
		usage.StoredMessageCount != 1 || usage.StoredPayloadBytes != 7 ||
		usage.ReplayableMessageCount != 1 || usage.ReplayablePayloadBytes != 7 {
		t.Fatalf("usage = %+v", usage)
	}
	for _, tc := range []struct {
		scope string
		want  int
	}{{api.ScopeUsageRead, http.StatusOK}, {api.ScopeAppsRead, http.StatusForbidden}} {
		key, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := e.store.CreateAPIKey(t.Context(), e.acct.ID, hash, "realtime-usage-test", []string{tc.scope}); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("Authorization", "Bearer "+key)
		rec := httptest.NewRecorder()
		e.h.ServeHTTP(rec, req)
		if rec.Code != tc.want {
			t.Fatalf("scope %s: got %d, want %d: %s", tc.scope, rec.Code, tc.want, rec.Body)
		}
	}
}
