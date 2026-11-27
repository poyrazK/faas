package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/webhook"
	"github.com/onebox-faas/faas/pkg/webhookout"
)

func TestAPISandboxPartnerWebhookVerifiesReceiverAndRecordsOutcome(t *testing.T) {
	identity, restore := withTestIdentities(t)
	defer restore()
	env, parent, source := sandboxReleaseFixture(t)
	ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
	defer cancel()
	owner := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID
	fixture := json.RawMessage(`{
	  "name":"Verify a partner export webhook",
	  "seed":{},
	  "requests":[{"title":"Start export","method":"POST","path":"/exports","expected_status":202}],
	  "outbound_webhook":{"assertions":[{"pointer":"/export_id","equals":"fixture-only"}]}
	}`)
	var scenario api.APISandboxScenarioResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodPut, owner+"/api-sandbox-scenario", fixture, nil), http.StatusOK, &scenario)
	if scenario.Scenario == nil {
		t.Fatal("saved scenario missing")
	}
	id := uuid.NewString()
	runPath := owner + "/api-sandbox-runs/" + id
	public := "/v1/api-sandbox-runs/" + id
	input := api.APISandboxRunRequest{ScenarioRevision: scenario.Scenario.Revision, PartnerWebhook: true}
	var invitation api.APISandboxReceiverInvite
	readAPISandboxTestResponse(t, env.do(t, http.MethodPost, runPath+"/receiver-invite",
		api.APISandboxReceiverInviteRequest{ScenarioRevision: input.ScenarioRevision}, nil), http.StatusCreated, &invitation)
	if invitation.RunID != id || !strings.HasPrefix(invitation.Token, api.APISandboxReceiverInvitePrefix) ||
		env.do(t, http.MethodPut, runPath, input, nil).Code != http.StatusConflict {
		t.Fatal("run could start without a ready receiver")
	}
	auth := map[string]string{"Authorization": "Bearer " + invitation.Token}
	var proof api.APISandboxReceiverProof
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, public+"/receiver-invite", nil, auth), http.StatusOK, &proof)

	const target = "https://receiver.example.test/callback"
	probeCount := 0
	env.s.apiSandboxReceiverClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		probeCount++
		if req.Method != http.MethodGet || req.URL.String() != target || req.Header.Get("Authorization") != "" {
			t.Errorf("unexpected receiver proof request: %s %s", req.Method, req.URL)
		}
		if probeCount == 1 {
			return &http.Response{StatusCode: http.StatusTemporaryRedirect, Header: http.Header{"Location": {"https://redirect.example.test/"}}, Body: http.NoBody}, nil
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(proof.Proof + "\n"))}, nil
	})}
	for _, invalid := range []string{"http://receiver.example.test/", "https://127.0.0.1/callback", "https://receiver.example.test/callback?key=secret"} {
		if res := env.do(t, http.MethodPost, public+"/receiver-invite", api.APISandboxReceiverURL{URL: invalid}, auth); res.Code != http.StatusBadRequest {
			t.Fatalf("invalid proof target accepted: %d", res.Code)
		}
	}
	if probeCount != 0 {
		t.Fatal("an invalid target was probed")
	}
	url := api.APISandboxReceiverURL{URL: target}
	if res := env.do(t, http.MethodPost, public+"/receiver-invite", url, auth); res.Code != http.StatusUnprocessableEntity || probeCount != 1 {
		t.Fatal("proof redirect was accepted or followed")
	}
	var credentials api.APISandboxReceiverCredentials
	readAPISandboxTestResponse(t, env.do(t, http.MethodPost, public+"/receiver-invite", url, auth), http.StatusOK, &credentials)
	if env.do(t, http.MethodPost, public+"/receiver-invite", url, auth).Code != http.StatusUnauthorized ||
		env.do(t, http.MethodPut, runPath, input, nil).Code != http.StatusConflict {
		t.Fatal("the used invite could be redeemed or the unready receiver could start a run")
	}
	resultAuth := map[string]string{"Authorization": "Bearer " + credentials.OutcomeToken}
	if res := env.do(t, http.MethodPost, public+"/receiver-ready", nil, resultAuth); res.Code != http.StatusNoContent {
		t.Fatalf("confirm readiness: %d %s", res.Code, res.Body)
	}
	var listing api.APISandboxRunsResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, owner+"/api-sandbox-runs", nil, nil), http.StatusOK, &listing)
	if len(listing.Runs) != 0 || len(listing.ReceiverInvites) != 1 || listing.ReceiverInvites[0].Status != "ready" || listing.ReceiverInvites[0].Token != "" {
		t.Fatal("owner list didn't expose the unsigned setup status")
	}
	if res := env.do(t, http.MethodPut, runPath, input, nil); res.Code != http.StatusAccepted {
		t.Fatalf("launch callback scenario: %d %s", res.Code, res.Body)
	}
	app, err := env.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := env.store.CreateAPISandboxDeployment(ctx, env.acct.ID, app.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := env.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	env.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, deployment.ID)
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(
			`{"openapi":"3.1.0","info":{"title":"Exports","version":"1"},"paths":{}}`))}, nil
	})}
	for range 2 {
		if err := env.s.processAPISandboxOperation(ctx, dueSandboxOperations{env.store}); err != nil {
			t.Fatal(err)
		}
	}

	type receipt struct{ deliveryID, attempt string }
	received := make(chan receipt, 3)
	dispatcher := webhook.NewDispatcher(env.store, nil, env.s.log).WithTick(20 * time.Millisecond).
		WithBackoffs(map[state.AppWebhookRetryPolicy][]time.Duration{state.AppWebhookRetryAggressive: {25 * time.Millisecond}})
	dispatcher.IdentityLoader = func() []*age.X25519Identity { return []*age.X25519Identity{identity} }
	dispatcher.HTTPClient = &http.Client{Transport: sandboxRoundTripper(func(req *http.Request) (*http.Response, error) {
		defer req.Body.Close()
		body, err := io.ReadAll(req.Body)
		deliveryID := req.Header.Get("X-Faas-Delivery-Id")
		attempt := req.Header.Get("X-Faas-Webhook-Attempt")
		ts, tsErr := strconv.ParseInt(req.Header.Get("X-Faas-Webhook-Timestamp"), 10, 64)
		signature, ok := strings.CutPrefix(req.Header.Get("X-Faas-Webhook-Signature"), "sha256=")
		if req.Method != http.MethodPost || req.URL.String() != target || deliveryID == "" || !ok ||
			err != nil || tsErr != nil || !strings.Contains(string(body), `"job_id":"`+id+`"`) ||
			webhookout.NewSigner([]byte(credentials.SigningSecret)).Verify(ts, deliveryID, body, signature) != nil {
			t.Error("callback was not signed for the registered receiver and pinned job")
		}
		select {
		case received <- receipt{deliveryID, attempt}:
		default:
			t.Error("unexpected extra callback")
		}
		return &http.Response{StatusCode: http.StatusNoContent, Body: http.NoBody}, nil
	})}
	dispatchCtx, stopDispatcher := context.WithCancel(ctx)
	dispatchDone := make(chan error, 1)
	go func() { dispatchDone <- dispatcher.Run(dispatchCtx) }()
	defer func() {
		stopDispatcher()
		if err := <-dispatchDone; err != nil {
			t.Error(err)
		}
	}()

	worked := make(chan error, 1)
	go func() { worked <- env.s.processAPISandboxRun(ctx, env.store) }()
	inv := waitSandboxWebhookTestInvocation(t, ctx, env.store, id)
	if _, err := env.store.ClaimInvocation(ctx, inv.ID, "", 30); err != nil {
		t.Fatal(err)
	}
	if err := env.store.CompleteInvocation(ctx, inv.ID, json.RawMessage(`{"export_id":"fixture-only"}`)); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-worked:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if len(received) != 2 {
		t.Fatalf("expected two signed callbacks, got %d", len(received))
	}
	first, second := <-received, <-received
	var waiting api.APISandboxRunReport
	readAPISandboxTestResponse(t, env.do(t, http.MethodGet, runPath, nil, nil), http.StatusOK, &waiting)
	if !waiting.PartnerWebhook || waiting.Status != api.APISandboxRunAwaitingOutcome || waiting.PartnerOutcomeUntil == nil ||
		len(waiting.Steps) != 5 || !waiting.Steps[2].AcknowledgementDropped ||
		waiting.Steps[2].HTTPStatus != http.StatusNoContent || waiting.Steps[3].HTTPStatus != http.StatusNoContent ||
		first.attempt != "1" || second.attempt != "2" || first.deliveryID != second.deliveryID ||
		waiting.Steps[2].DeliveryID != first.deliveryID || waiting.Steps[3].DeliveryID != second.deliveryID {
		t.Fatalf("unexpected retry evidence: %+v", waiting.Steps)
	}
	if err := env.s.processAPISandboxRun(ctx, env.store); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("run consumed a worker lease while waiting: %v", err)
	}
	count := 1
	outcome := api.APISandboxReceiverOutcome{DeliveryID: uuid.NewString(), AppliedCount: &count}
	if res := env.do(t, http.MethodPost, public+"/receiver-outcome", outcome, resultAuth); res.Code != http.StatusConflict {
		t.Fatalf("wrong delivery ID accepted: %d", res.Code)
	}
	outcome.DeliveryID = second.deliveryID
	if res := env.do(t, http.MethodPost, public+"/receiver-outcome", outcome, auth); res.Code != http.StatusUnauthorized {
		t.Fatalf("invite was accepted as outcome authority: %d", res.Code)
	}
	if res := env.do(t, http.MethodPost, public+"/receiver-outcome", outcome, resultAuth); res.Code != http.StatusNoContent {
		t.Fatalf("could not record outcome: %d %s", res.Code, res.Body)
	}
	if err := env.s.processAPISandboxRun(ctx, env.store); err != nil {
		t.Fatal(err)
	}
	if res := env.do(t, http.MethodPost, public+"/receiver-outcome", outcome, resultAuth); res.Code != http.StatusNoContent {
		t.Fatalf("same evidence was not idempotent on retry: %d", res.Code)
	}
	count = 2
	if res := env.do(t, http.MethodPost, public+"/receiver-outcome", outcome, resultAuth); res.Code != http.StatusConflict {
		t.Fatalf("outcome was overwritten: %d", res.Code)
	}
	var share api.APISandboxRunShareResponse
	readAPISandboxTestResponse(t, env.do(t, http.MethodPost, runPath+"/report-link", nil, nil), http.StatusCreated, &share)
	response := env.do(t, http.MethodGet, public+"/report", nil, map[string]string{"Authorization": "Bearer " + share.Token})
	var report api.APISandboxRunReport
	readAPISandboxTestResponse(t, response, http.StatusOK, &report)
	if report.Status != api.APISandboxRunPassed || len(report.Steps) != 6 {
		t.Fatalf("unexpected finished report: %d %s", response.Code, response.Body)
	}
	step := report.Steps[5]
	if step.Kind != api.APISandboxStepPartnerOutcome || step.PartnerAppliedCount == nil ||
		*step.PartnerAppliedCount != 1 || step.DeliveryID != second.deliveryID {
		t.Fatal("partner outcome evidence missing from the report")
	}
	for _, private := range []string{target, proof.Proof, credentials.SigningSecret, credentials.OutcomeToken, "fixture-only"} {
		if strings.Contains(response.Body.String(), private) {
			t.Fatal("shared report exposed private setup or result data")
		}
	}
}

func readAPISandboxTestResponse(t *testing.T, res *httptest.ResponseRecorder, status int, into any) {
	t.Helper()
	if res.Code != status {
		t.Fatalf("response status %d, want %d: %s", res.Code, status, res.Body)
	}
	if err := json.Unmarshal(res.Body.Bytes(), into); err != nil {
		t.Fatal(err)
	}
}
