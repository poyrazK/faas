// adr:1352
package apidserver_test

import (
	"encoding/json"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"net/http"
	"testing"
)

func TestQueueCanaryAPIExplicitConsentAndCancellation(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	s, account := *e.Store(), *e.Acct()
	ctx := t.Context()
	app, err := s.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "queue-canary", Type: state.AppTypeFunction, WorkloadClass: state.WorkloadClassHTTP, Manifest: state.AppManifest{RevisionPinTTLSeconds: 3600}})
	if err != nil {
		t.Fatal(err)
	}
	env, _ := json.Marshal(map[string]string{api.QueueCanaryCapabilityEnv: `["orders"]`})
	dep, err := s.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Scope: "default", ImageDigest: "sha256:canary", OverrideEnv: env})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SetDeploymentRootfs(ctx, dep.ID, "/test/rootfs", "test/rootfs", 1); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: account.ID, AppID: app.ID, Name: "orders", QueueName: "orders", Mode: "push", WorkloadClass: state.WorkloadClassHTTP, Enabled: true, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	path := "/v1/apps/" + app.Slug + "/bindings/queue-canaries"
	req := api.CreateQueueBindingCanaryRequest{DeploymentID: uuid.MustParse(dep.ID).String(), Binding: "orders", TTLSeconds: 60}
	response := e.Do(t, http.MethodPost, path, req, nil)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("consent: %d %s", response.Code, response.Body.String())
	}
	req.PublishConsent = true
	response = e.Do(t, http.MethodPost, path, req, nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var out api.QueueBindingCanary
	if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Status != "published" || out.CompletedAt != nil {
		t.Fatalf("publication false pass: %+v", out)
	}
	response = e.Do(t, http.MethodDelete, path+"/"+out.ID, nil, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("cancel: %d %s", response.Code, response.Body.String())
	}
	if err = json.Unmarshal(response.Body.Bytes(), &out); err != nil || out.Status != "cancelled" {
		t.Fatalf("cancel receipt: %+v %v", out, err)
	}
}
