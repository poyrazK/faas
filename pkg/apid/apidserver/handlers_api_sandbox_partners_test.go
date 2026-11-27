package apidserver

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPISandboxPartnerHandoff(t *testing.T) {
	e, parent, source := sandboxReleaseFixture(t)
	ctx := t.Context()
	base := "/v1/apps/" + parent.Slug + "/deployments/" + source.ID + "/api-sandboxes"
	input := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{}`), APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name: "List notes", Requests: []api.APISandboxExampleRequest{{Title: "Read sample notes", Method: "GET", Path: "/notes", ExpectedStatus: 200}},
		},
	}
	scenario, err := e.store.PutAPISandboxScenario(ctx, source.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	copyPath, recipientPath := base+"/"+id, "/v1/api-sandboxes/"+id
	create := api.APISandboxCreateRequest{ScenarioRevision: scenario.Revision, PartnerLabel: "Integration team"}
	if response := e.do(t, http.MethodPut, copyPath, create, nil); response.Code != http.StatusAccepted {
		t.Fatalf("reserve partner copy: %d %s", response.Code, response.Body)
	}
	app, err := e.store.AppBySlug(ctx, apiSandboxSlug(id))
	if err != nil || !app.Manifest.APISandboxPartner {
		t.Fatalf("partner app missing: %v", err)
	}
	create.PartnerLabel = "Different team"
	if response := e.do(t, http.MethodPut, copyPath, create, nil); response.Code != http.StatusConflict {
		t.Fatalf("changed a reserved partner copy: %d %s", response.Code, response.Body)
	}
	issue := func() string {
		t.Helper()
		response := e.do(t, http.MethodPost, copyPath+"/partner/invite", nil, nil)
		var invite api.APISandboxPartnerInviteResponse
		if err := json.Unmarshal(response.Body.Bytes(), &invite); err != nil || response.Code != http.StatusCreated ||
			!strings.HasPrefix(invite.Token, api.APISandboxPartnerInvitePrefix) || api.ValidConsumerKeyFormat(invite.Token) {
			t.Fatalf("issue invite: %d, %v", response.Code, err)
		}
		return invite.Token
	}
	authorize := func(token string) map[string]string { return map[string]string{"Authorization": "Bearer " + token} }
	first, current := issue(), issue()
	if response := e.do(t, http.MethodPost, recipientPath+"/redeem", nil, authorize(first)); response.Code != http.StatusUnauthorized {
		t.Fatalf("replaced invite was still active: %d", response.Code)
	}
	if response := e.do(t, http.MethodPost, recipientPath+"/redeem", nil, authorize(current)); response.Code != http.StatusConflict {
		t.Fatalf("unverified copy accepted a partner: %d", response.Code)
	}

	deployment, err := e.store.CreateAPISandboxDeployment(ctx, e.acct.ID, app.ID, source.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.store.UpdateDeploymentStatus(ctx, deployment.ID, state.DeployLive, ""); err != nil {
		t.Fatal(err)
	}
	e.s.apiSandboxProbeClient = &http.Client{Transport: sandboxRoundTripper(func(r *http.Request) (*http.Response, error) {
		headers := make(http.Header)
		headers.Set(api.RevisionHeader, deployment.ID)
		return &http.Response{StatusCode: http.StatusOK, Header: headers, Body: io.NopCloser(strings.NewReader(
			`{"openapi":"3.1.0","info":{"title":"Notes","version":"1"},"paths":{}}`))}, nil
	})}
	for range 2 {
		if err := e.s.processAPISandboxOperation(ctx, dueSandboxOperations{e.store}); err != nil {
			t.Fatal(err)
		}
	}
	if response := e.do(t, http.MethodPost, copyPath+"/invites", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("issued a reusable key on a private copy: %d", response.Code)
	}

	// A replay and the legitimate exchange may hit different APID workers.
	var wg sync.WaitGroup
	results := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		wg.Go(func() { results <- e.do(t, http.MethodPost, recipientPath+"/redeem", nil, authorize(current)) })
	}
	wg.Wait()
	close(results)
	var receipt api.APISandboxInviteResponse
	successes := 0
	for response := range results {
		switch response.Code {
		case http.StatusOK:
			successes++
			if err := json.Unmarshal(response.Body.Bytes(), &receipt); err != nil || !api.ValidConsumerKeyFormat(receipt.Token) ||
				receipt.Sandbox.Partner != nil || receipt.Sandbox.Scenario == nil {
				t.Fatalf("bad redemption receipt: %v", err)
			}
		case http.StatusUnauthorized:
		default:
			t.Fatalf("unexpected redemption status: %d %s", response.Code, response.Body)
		}
	}
	if successes != 1 {
		t.Fatalf("one invite produced %d successful redemptions", successes)
	}
	if response := e.do(t, http.MethodGet, recipientPath+"/invite", nil, authorize(receipt.Token)); response.Code != http.StatusOK {
		t.Fatalf("redeemed key couldn't read the scenario guide: %d", response.Code)
	}
	p, err := e.store.GetAPISandboxPartner(ctx, app.ID)
	if err != nil || p.RedeemedAt == nil || p.FirstSuccessAt != nil {
		t.Fatalf("redemption state is invalid (%v)", err)
	}
	if err := e.store.RecordAPISandboxPartnerSuccess(ctx, app.ID, p.ConsumerKeyID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	var listing api.APISandboxListResponse
	response := e.do(t, http.MethodGet, base, nil, nil)
	if err := json.Unmarshal(response.Body.Bytes(), &listing); err != nil || len(listing.Sandboxes) != 1 ||
		listing.Sandboxes[0].Partner == nil || listing.Sandboxes[0].Partner.FirstSuccessAt == nil {
		t.Fatalf("owner couldn't see partner progress: %d (%v)", response.Code, err)
	}
	if response := e.do(t, http.MethodPost, copyPath+"/partner/invite", nil, nil); response.Code != http.StatusConflict {
		t.Fatalf("replaced redeemed access without revocation: %d", response.Code)
	}
	if response := e.do(t, http.MethodDelete, copyPath+"/partner", nil, nil); response.Code != http.StatusNoContent {
		t.Fatalf("revoke partner: %d %s", response.Code, response.Body)
	}
	if response := e.do(t, http.MethodGet, recipientPath+"/invite", nil, authorize(receipt.Token)); response.Code != http.StatusUnauthorized {
		t.Fatalf("revoked credential could read the scenario guide: %d", response.Code)
	}
	issue()
}
