package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"filippo.io/age"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/executionpayload"
)

func createExecutionArtifactGrantAs(t *testing.T, e testEnv, key, executionID, name string) api.ExecutionArtifactGrantResponse {
	t.Helper()
	rec := e.doAs(t, http.MethodPost, "/v1/executions/"+executionID+"/artifact-grants", api.CreateExecutionArtifactGrantRequest{
		ArtifactName: name, ExpiresInSeconds: 300,
	}, nil, key)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create artifact grant = %d: %s", rec.Code, rec.Body.String())
	}
	var grant api.ExecutionArtifactGrantResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	return grant
}

func createExecutionUsingArtifactGrantAs(t *testing.T, e testEnv, key, idempotencyKey, token string) *httptest.ResponseRecorder {
	t.Helper()
	request := api.CreateExecutionRequest{
		Runtime: api.ExecutionRuntimeNode22, Entrypoint: "main.js",
		Files:          []api.ExecutionFile{{Path: "main.js", Content: []byte("export default async () => 1")}},
		ArtifactInputs: []api.ExecutionArtifactInput{{GrantToken: token, Path: "input/result.csv"}},
	}
	return e.doAs(t, http.MethodPost, "/v1/executions", request, map[string]string{"Idempotency-Key": idempotencyKey}, key)
}

func TestExecutionArtifactGrantTransfersOnlyOneArtifactAcrossKeyFamilies(t *testing.T) {
	e := setup(t, api.PlanPro)
	identity := enableExecutionAPIForTest(t, &e)
	agentA := createRunsOnlyKey(t, e, "producer")
	agentB := createRunsOnlyKey(t, e, "consumer")
	owned := createExecutionAs(t, e, agentA, "producer-run", executionRequest())
	finishExecutionWithArtifact(t, e, owned.ID)

	grant := createExecutionArtifactGrantAs(t, e, agentA, owned.ID, "result.csv")
	if grant.ID == "" || grant.Token == "" || grant.SourceExecutionID != owned.ID || grant.ArtifactName != "result.csv" {
		t.Fatalf("artifact grant response = %+v", grant)
	}
	if rec := e.doAs(t, http.MethodGet, "/v1/executions/"+owned.ID, nil, nil, agentB); rec.Code != http.StatusNotFound {
		t.Fatalf("consumer read source execution = %d: %s", rec.Code, rec.Body.String())
	}

	created := createExecutionUsingArtifactGrantAs(t, e, agentB, "consume-once", grant.Token)
	if created.Code != http.StatusAccepted {
		t.Fatalf("consumer execution create = %d: %s", created.Code, created.Body.String())
	}
	var received api.ExecutionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &received); err != nil {
		t.Fatal(err)
	}
	claim, err := e.store.ClaimExecution(context.Background(), "artifact-grant-test", time.Now().UTC().Add(time.Millisecond), time.Minute)
	if err != nil || claim.ID != received.ID {
		t.Fatalf("claim receiving run = %q, %v; want %q", claim.ID, err, received.ID)
	}
	payload, err := executionpayload.DecodeRequest(context.Background(), []*age.X25519Identity{identity}, claim.SealedPayload, claim.PayloadKID)
	if err != nil {
		t.Fatalf("decode receiving run: %v", err)
	}
	found := false
	for _, file := range payload.Files {
		if file.Path == "input/result.csv" && string(file.Content) == "value\n42\n" {
			found = true
		}
	}
	if !found {
		t.Fatalf("receiving bundle did not contain the granted artifact: %+v", payload.Files)
	}
	if replay := createExecutionUsingArtifactGrantAs(t, e, agentB, "consume-again", grant.Token); replay.Code != http.StatusNotFound {
		t.Fatalf("second redemption = %d: %s", replay.Code, replay.Body.String())
	}

	revocable := createExecutionArtifactGrantAs(t, e, agentA, owned.ID, "result.csv")
	if rec := e.doAs(t, http.MethodDelete, "/v1/execution-artifact-grants/"+revocable.ID, nil,
		map[string]string{"Idempotency-Key": "cross-agent-revoke"}, agentB); rec.Code != http.StatusNotFound {
		t.Fatalf("consumer revoked producer grant = %d: %s", rec.Code, rec.Body.String())
	}
	if rec := e.doAs(t, http.MethodDelete, "/v1/execution-artifact-grants/"+revocable.ID, nil,
		map[string]string{"Idempotency-Key": "producer-revoke"}, agentA); rec.Code != http.StatusOK {
		t.Fatalf("producer revoke = %d: %s", rec.Code, rec.Body.String())
	}
	if replay := createExecutionUsingArtifactGrantAs(t, e, agentB, "revoked-grant", revocable.Token); replay.Code != http.StatusNotFound {
		t.Fatalf("redeem revoked grant = %d: %s", replay.Code, replay.Body.String())
	}
}
