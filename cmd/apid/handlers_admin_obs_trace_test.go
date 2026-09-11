package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

const testOperatorTraceID = "4bf92f3577b34da6a3ce929d0e0e4736"

func TestObsTraceLookupCorrelatesIntentAndEvents(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	ctx := context.Background()
	if _, err := e.store.InsertOperatorIntent(ctx, state.OperatorIntentKindForcePark,
		"instance-1", nil, e.acct.ID, "incident_123", nil, traceStringPtr(testOperatorTraceID)); err != nil {
		t.Fatal(err)
	}
	if err := e.store.AppendEventWithTrace(ctx, "system:schedd", "operator.action.force_park.outcome",
		nil, []byte(`{"result":"succeeded"}`), traceStringPtr(testOperatorTraceID)); err != nil {
		t.Fatal(err)
	}
	rec := e.do(t, http.MethodGet, "/v1/admin/obs/traces/"+testOperatorTraceID+"?limit=25", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", rec.Code, rec.Body.String())
	}
	var response api.ObsTraceLookupResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.TraceID != testOperatorTraceID || response.Limit != 25 || len(response.Intents) != 1 || len(response.Events) != 1 {
		t.Fatalf("response = %+v", response)
	}
	if response.Intents[0].TraceID != testOperatorTraceID || response.Events[0].Kind != "operator.action.force_park.outcome" {
		t.Fatalf("correlation missing: %+v", response)
	}
}

func TestObsTraceLookupRejectsInvalidAndMissingTrace(t *testing.T) {
	e := newObsEnv(t, api.ScopesAdminOnly, "ops@faas.dev", "ops@faas.dev")
	for _, tc := range []struct {
		path string
		want int
	}{
		{path: "/v1/admin/obs/traces/not-hex", want: http.StatusBadRequest},
		{path: "/v1/admin/obs/traces/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", want: http.StatusNotFound},
	} {
		rec := e.do(t, http.MethodGet, tc.path, nil, nil)
		if rec.Code != tc.want {
			t.Errorf("%s status = %d, want %d body=%s", tc.path, rec.Code, tc.want, rec.Body.String())
		}
	}
}

func traceStringPtr(value string) *string { return &value }
