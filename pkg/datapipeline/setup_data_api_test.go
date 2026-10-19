package datapipeline

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestSetupDataAPIRetainsLiveMirrorAndRecoversLostDeployReply(t *testing.T) {
	service, store, account, c, request := setupFixture(t)
	request.DataAPIName, request.AuthAppID = "orders-api", uuid.NewString()
	app, binding := uuid.NewString(), uuid.NewString()
	service.PlanDataAPI = func(context.Context, string, SetupRequest) (DataAPIPlan, error) {
		return DataAPIPlan{AppID: app, AuthAppID: request.AuthAppID, Issuer: "https://auth.example/auth/v1"}, nil
	}
	preparations, submissions := 0, 0
	intents := map[string]string{}
	service.PrepareDataAPI = func(context.Context, Setup) (string, bool, error) { preparations++; return binding, true, nil }
	service.DeployDataAPI = func(_ context.Context, j Setup) (string, string, error) {
		submissions++
		if intents[j.DataAPIPlan.IntentID] == "" {
			intents[j.DataAPIPlan.IntentID] = uuid.NewString()
		}
		if submissions == 1 {
			return "", "", ErrUnavailable
		}
		return intents[j.DataAPIPlan.IntentID], uuid.NewSHA1(uuid.NameSpaceOID, []byte(j.DataAPIPlan.IntentID)).String(), nil
	}
	status := "building"
	service.DataAPIState = func(context.Context, Setup) (string, string, error) {
		if status == "live" {
			return status, "https://orders-api.example", nil
		}
		return status, "", nil
	}
	job, err := service.StartSetup(t.Context(), account, request)
	if err != nil {
		t.Fatal(err)
	}
	savedIntent := job.DataAPIPlan.IntentID
	job = setupTick(t, service, store, job.ID)
	job = setupTick(t, service, store, job.ID)
	job = setupTick(t, service, store, job.ID)
	finishSetupCheck(t, store, account, c, job.DatabaseID)
	job = setupTick(t, service, store, job.ID)
	job = setupTick(t, service, store, job.ID)
	claimed, err := store.Claim(t.Context(), uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if err = store.Finish(t.Context(), claimed, Observation{State: "live"}); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "configuring_api" || job.State != "working" {
		t.Fatalf("API was skipped: %+v", job)
	}
	job = setupTick(t, service, store, job.ID)
	if job.DataAPIPlan.BindingID != binding || job.Phase != "deploying_api" {
		t.Fatalf("binding checkpoint lost: %+v", job)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State != "blocked" || job.Phase != "deploying_api" {
		t.Fatalf("lost reply not recoverable: %+v", job)
	}
	if _, err = store.ControlSetup(t.Context(), account, job.ID, job.Revision, "retry"); err != nil {
		t.Fatal(err)
	}
	service.Store = NewPostgresStore(store.pool)
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "waiting_api" || job.DataAPIPlan.IntentID != savedIntent || len(intents) != 1 {
		t.Fatalf("lost reply duplicated deployment: %+v", job)
	}
	job = setupTick(t, service, store, job.ID)
	if job.State == "ready" || job.DataAPIPlan.Endpoint != "" {
		t.Fatal("endpoint published before readiness")
	}
	status = "failed"
	job = setupTick(t, service, store, job.ID)
	failed := job.DataAPIPlan.DeploymentID
	if job.State != "blocked" || job.Phase != "api_failed" || job.Reason != "data_api_deployment_failed" {
		t.Fatalf("failed deployment not actionable: %+v", job)
	}
	p, err := store.Get(t.Context(), account, job.PipelineID)
	if err != nil || p.State != "live" || p.DesiredState != "running" {
		t.Fatal("API failure interrupted the mirror", err)
	}
	if _, err = store.ControlSetup(t.Context(), account, job.ID, job.Revision, "retry"); err != nil {
		t.Fatal(err)
	}
	job = setupTick(t, service, store, job.ID)
	if job.Phase != "deploying_api" || job.DataAPIPlan.IntentID == savedIntent || job.DataAPIPlan.PreviousDeploymentID != failed || submissions != 2 {
		t.Fatal("retry was not persisted before submission")
	}
	job = setupTick(t, service, store, job.ID)
	if preparations != 1 || len(intents) != 2 || job.DataAPIPlan.BindingID != binding || job.DataAPIPlan.AppID != app {
		t.Fatal("terminal retry replaced stable resources")
	}
	status = "live"
	job = setupTick(t, service, store, job.ID)
	if job.State != "ready" || job.DataAPIPlan.Endpoint != "https://orders-api.example" {
		t.Fatalf("healthy API never completed setup: %+v", job)
	}
}

func TestSetupDataAPIRequiresBothOptionalFieldsAndAvailableOwner(t *testing.T) {
	for _, tc := range []struct{ name, auth string }{{"orders-api", ""}, {"", uuid.NewString()}, {"orders-api", "bad-id"}} {
		service, store, account, _, r := setupFixture(t)
		r.DataAPIName, r.AuthAppID = tc.name, tc.auth
		if _, err := service.StartSetup(t.Context(), account, r); !errors.Is(err, ErrInvalid) {
			t.Fatalf("invalid optional configuration accepted: %v", err)
		}
		rows, err := store.Setups(t.Context(), account)
		if err != nil || len(rows) != 0 {
			t.Fatal("invalid request reserved a mirror", err)
		}
	}
	service, _, account, _, r := setupFixture(t)
	r.DataAPIName, r.AuthAppID = "orders-api", uuid.NewString()
	if _, err := service.StartSetup(t.Context(), account, r); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unconfigured API owner accepted: %v", err)
	}
}
