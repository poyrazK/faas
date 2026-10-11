package main

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDevPatchStatusReportsDelivery(t *testing.T) {
	const (
		project   = "gregale-api"
		workspace = "cccccccccccccccccccccccccccccccc"
	)
	e := setup(t, api.PlanPro)
	ctx := context.Background()
	created := e.do(t, http.MethodPut, "/v1/dev/sessions/"+project, api.UpsertDevSessionRequest{WorkspaceID: workspace}, nil)
	if created.Code != http.StatusCreated {
		t.Fatalf("create session = %d: %s", created.Code, created.Body.String())
	}
	var session api.DevSessionResponse
	if err := json.Unmarshal(created.Body.Bytes(), &session); err != nil {
		t.Fatal(err)
	}
	base, err := e.store.CreateDeployment(ctx, state.Deployment{AppID: session.App.ID, Kind: state.DeploymentKindTarball,
		Status: state.DeployLive, ImageDigest: appTaskTestDigest, CreatedAt: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	publish := func() int64 {
		patch, err := e.store.CreateDevSourcePatch(ctx, state.DevSourcePatch{AppID: session.App.ID, BaseDeploymentID: base.ID,
			ImageDir: "/app", Archive: []byte("p"), Digest: "0000000000000000000000000000000000000000000000000000000000000000",
			ExpiresAt: time.Now().Add(time.Hour)})
		if err != nil {
			t.Fatal(err)
		}
		return patch.Generation
	}
	status := func(generation string) (int, api.DevPatchStatusResponse) {
		t.Helper()
		rec := e.do(t, http.MethodGet, "/v1/dev/sessions/"+project+"/patches/"+generation+"?workspace_id="+workspace, nil, nil)
		var body api.DevPatchStatusResponse
		if rec.Code == http.StatusOK {
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatal(err)
			}
		}
		return rec.Code, body
	}

	applied := publish()
	if code, body := status("1"); code != http.StatusOK || body.State != api.DevPatchStatePending || body.AppliedAt != nil {
		t.Fatalf("before ack = %d %+v, want pending", code, body)
	}
	if err := e.store.RecordDevSourcePatchApplied(ctx, session.App.ID, base.ID, applied, 85, ""); err != nil {
		t.Fatal(err)
	}
	if code, body := status("1"); code != http.StatusOK || body.State != api.DevPatchStateApplied || body.ApplyMS != 85 || body.AppliedAt == nil {
		t.Fatalf("after ack = %d %+v, want applied", code, body)
	}

	failed := publish()
	if err := e.store.RecordDevSourcePatchApplied(ctx, session.App.ID, base.ID, failed, 3, "apply_failed"); err != nil {
		t.Fatal(err)
	}
	if code, body := status("2"); code != http.StatusOK || body.State != api.DevPatchStateFailed || body.ErrorCode != "apply_failed" {
		t.Fatalf("failed ack = %d %+v, want failed", code, body)
	}

	for _, generation := range []string{"0", "abc", "99"} {
		if code, _ := status(generation); code != http.StatusNotFound {
			t.Fatalf("generation %q = %d, want 404", generation, code)
		}
	}
}
