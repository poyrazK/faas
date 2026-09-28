package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestWorkPolicyAsyncInvokeAPI(t *testing.T) {
	e := setup(t, api.PlanPro)
	app := mustSeedApp(t, e, "work-api")
	path := "/v1/apps/work-api/work-policies/document-index"
	policy := api.UpsertWorkPolicyRequest{MaxRunningPerKey: 1, PendingUpdates: "keep_latest", DebounceMS: 3000, ExpiresAfterMS: 600000}
	put := e.do(t, http.MethodPut, path, policy, nil)
	if put.Code != http.StatusOK {
		t.Fatalf("PUT = %d %s", put.Code, put.Body.String())
	}
	var configured api.WorkPolicyResponse
	if err := json.Unmarshal(put.Body.Bytes(), &configured); err != nil {
		t.Fatal(err)
	}
	if configured.Name != "document-index" || configured.Revision != 1 {
		t.Fatalf("policy = %+v", configured)
	}
	listed := e.do(t, http.MethodGet, "/v1/apps/work-api/work-policies", nil, nil)
	if listed.Code != http.StatusOK {
		t.Fatalf("GET = %d %s", listed.Code, listed.Body.String())
	}
	var policies api.WorkPolicyListResponse
	if err := json.Unmarshal(listed.Body.Bytes(), &policies); err != nil {
		t.Fatal(err)
	}
	if len(policies.Policies) != 1 || policies.Policies[0].Name != configured.Name {
		t.Fatalf("list = %+v", policies)
	}
	invoke := func(key string) state.Invocation {
		t.Helper()
		response := e.do(t, http.MethodPost, "/v1/apps/work-api/invoke/async", api.InvokeRequest{
			Payload: json.RawMessage(`{"document_id":"d1"}`),
			Work:    &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(key)},
		}, nil)
		if response.Code != http.StatusAccepted {
			t.Fatalf("invoke = %d %s", response.Code, response.Body.String())
		}
		var accepted api.AsyncInvokeResponse
		if err := json.Unmarshal(response.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		row, err := e.store.InvocationByID(context.Background(), accepted.ID)
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	first, second, other := invoke(`"d1"`), invoke(`"d1"`), invoke(`"d2"`)
	first, _ = e.store.InvocationByID(context.Background(), first.ID)
	if first.State != state.InvocationSuperseded || second.State != state.InvocationPending || other.State != state.InvocationPending {
		t.Fatalf("states = %s, %s, %s", first.State, second.State, other.State)
	}
	if !bytes.Equal(first.WorkKeyDigest, second.WorkKeyDigest) || bytes.Equal(first.WorkKeyDigest, other.WorkKeyDigest) || second.WorkSequence != 2 || second.WorkPolicyRevision != 1 {
		t.Fatalf("lanes = %+v, %+v, %+v", first, second, other)
	}
	if app != second.AppID {
		t.Fatalf("invocation app = %s, want %s", second.AppID, app)
	}
	bad := e.do(t, http.MethodPost, "/v1/apps/work-api/invoke/async", api.InvokeRequest{
		Work: &api.InvokeWork{Policy: "document-index", Key: json.RawMessage(`{"not":"scalar"}`)},
	}, nil)
	assertProblem(t, bad, http.StatusBadRequest, api.CodeValidation)
}
