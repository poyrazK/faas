package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestAPIInvocationEnqueuePreservesRevisionHeader(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	ctx := context.Background()
	appID := apidserver.MustSeedApp(t, e, "versioned-invoke")
	app, err := (*e.Store()).AppByID(ctx, appID)
	if err != nil {
		t.Fatal(err)
	}
	manifest := app.Manifest
	manifest.RevisionPinTTLSeconds = 3600
	if _, err := (*e.Store()).UpdateApp(ctx, appID, state.UpdateAppParams{Manifest: &manifest}); err != nil {
		t.Fatal(err)
	}
	old, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: appID, ImageDigest: "sha256:old"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, old.ID); err != nil {
		t.Fatal(err)
	}
	newer, err := (*e.Store()).CreateDeployment(ctx, state.Deployment{AppID: appID, ImageDigest: "sha256:new"})
	if err != nil {
		t.Fatal(err)
	}
	if err := (*e.Store()).MarkDeploymentLive(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	for _, route := range []struct {
		path string
		body any
		want int
	}{
		{"/v1/apps/versioned-invoke/invoke/async", api.InvokeRequest{Payload: json.RawMessage(`{"ok":true}`)}, http.StatusAccepted},
		{"/v1/apps/versioned-invoke/queues/send", api.QueueSendRequest{Payload: json.RawMessage(`{"ok":true}`)}, http.StatusCreated},
	} {
		rec := e.Do(t, http.MethodPost, route.path, route.body, map[string]string{api.RevisionHeader: old.ID})
		if rec.Code != route.want {
			t.Fatalf("%s = %d %s", route.path, rec.Code, rec.Body.String())
		}
		if rec.Header().Get(api.RevisionHeader) != old.ID {
			t.Fatalf("%s response revision = %q", route.path, rec.Header().Get(api.RevisionHeader))
		}
		var accepted struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &accepted); err != nil {
			t.Fatal(err)
		}
		inv, err := (*e.Store()).InvocationByID(ctx, accepted.ID)
		if err != nil {
			t.Fatal(err)
		}
		var headers map[string]string
		if err := json.Unmarshal(inv.Headers, &headers); err != nil || headers[api.RevisionHeader] != old.ID {
			t.Fatalf("%s stored headers = %s, %v", route.path, inv.Headers, err)
		}
	}
	bad := e.Do(t, http.MethodPost, "/v1/apps/versioned-invoke/invoke/async", api.InvokeRequest{}, map[string]string{api.RevisionHeader: "not-a-uuid"})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("invalid revision = %d %s", bad.Code, bad.Body.String())
	}
}
