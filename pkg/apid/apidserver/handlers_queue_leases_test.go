package apidserver_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// queueLeaseApp is a worker-mode app with a pull binding on queue "jobs" and
// n ready messages.
func queueLeaseApp(t *testing.T, e apidserver.TestEnv, slug string, n int) state.App {
	t.Helper()
	ctx := context.Background()
	store := *e.Store()
	app, err := store.CreateApp(ctx, state.App{AccountID: (*e.Acct()).ID, Slug: slug, Type: state.AppTypeApp,
		WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	rec := e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/queue-bindings", api.CreateQueueBindingRequest{
		Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: "worker", MaxConcurrency: 5}, nil)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create pull binding = %d: %s", rec.Code, rec.Body.String())
	}
	var binding api.QueueBindingResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &binding); err != nil {
		t.Fatal(err)
	}
	for range n {
		if _, err := store.EnqueueInvocation(ctx, state.Invocation{AccountID: (*e.Acct()).ID, AppID: app.ID, Source: state.InvocationQueue,
			QueueName: "jobs", QueueBindingID: binding.ID, DueAt: time.Now().Add(-time.Second), Payload: json.RawMessage(`{"job":1}`)}); err != nil {
			t.Fatal(err)
		}
	}
	return app
}

func TestQueueLeaseAPIKeyLeaseAckNack(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	queueLeaseApp(t, e, "lease-worker", 2)
	rec := e.Do(t, http.MethodPost, "/v1/apps/lease-worker/queues/jobs/leases", api.QueueLeaseRequest{MaxMessages: 2, VisibilityTimeoutSeconds: 60}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("lease = %d: %s", rec.Code, rec.Body.String())
	}
	var leased api.QueueLeaseResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &leased); err != nil || len(leased.Messages) != 2 {
		t.Fatalf("leased %+v err=%v", leased, err)
	}
	if string(leased.Messages[0].Payload) != `{"job":1}` || leased.Messages[0].Attempt != 1 || leased.Messages[0].Receipt == "" {
		t.Fatalf("message %+v", leased.Messages[0])
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/lease-worker/queues/jobs/leases:ack",
		api.QueueLeaseReceiptsRequest{Receipts: []string{leased.Messages[0].Receipt}}, nil)
	var acked api.QueueLeaseReceiptsResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &acked) != nil || len(acked.Succeeded) != 1 {
		t.Fatalf("ack = %d: %s", rec.Code, rec.Body.String())
	}
	rec = e.Do(t, http.MethodPost, "/v1/apps/lease-worker/queues/jobs/leases:nack",
		api.QueueLeaseReceiptsRequest{Receipts: []string{leased.Messages[1].Receipt}, Error: "downstream unavailable"}, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("nack = %d: %s", rec.Code, rec.Body.String())
	}
	store := *e.Store()
	done, _ := store.InvocationByID(context.Background(), leased.Messages[0].ID)
	retried, _ := store.InvocationByID(context.Background(), leased.Messages[1].ID)
	if done.State != state.InvocationCompleted || retried.State != state.InvocationPending || retried.LastError != "downstream unavailable" {
		t.Fatalf("acked=%s nacked=%s (%q)", done.State, retried.State, retried.LastError)
	}

	rec = e.Do(t, http.MethodPost, "/v1/apps/lease-worker/queues/jobs/leases", api.QueueLeaseRequest{VisibilityTimeoutSeconds: 1 << 30}, nil)
	apidserver.AssertProblem(t, rec, http.StatusBadRequest, api.CodeValidation)
	rec = e.Do(t, http.MethodPost, "/v1/apps/lease-worker/queues/unbound/leases", nil, nil)
	apidserver.AssertProblem(t, rec, http.StatusNotFound, api.CodeNotFound)
}

func TestQueueLeaseWorkloadTokenRecordsInstance(t *testing.T) {
	ctx := context.Background()
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	app := queueLeaseApp(t, e, "token-worker", 1)
	queueLeaseApp(t, e, "other-worker", 0)
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:lease", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 128, "default-local", "", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "queues-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).WorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	bearer := func(audience string) map[string]string {
		token, err := signer.Mint(time.Now(), (*e.Acct()).ID, app.ID, instance.ID, audience)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + token.AccessToken}
	}

	// A Flags token cannot be replayed to lease queue messages.
	rec := e.Do(t, http.MethodPost, "/v1/apps/token-worker/queues/jobs/leases", nil, bearer(workloadidentity.FlagsAudience))
	apidserver.AssertProblem(t, rec, http.StatusUnauthorized, api.CodeUnauthorized)
	// A token names its own app; it cannot lease a neighbor's queue.
	rec = e.Do(t, http.MethodPost, "/v1/apps/other-worker/queues/jobs/leases", nil, bearer(workloadidentity.QueuesAudience))
	apidserver.AssertProblem(t, rec, http.StatusUnauthorized, api.CodeUnauthorized)

	rec = e.Do(t, http.MethodPost, "/v1/apps/token-worker/queues/jobs/leases", nil, bearer(workloadidentity.QueuesAudience))
	var leased api.QueueLeaseResponse
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &leased) != nil || len(leased.Messages) != 1 {
		t.Fatalf("workload lease = %d: %s", rec.Code, rec.Body.String())
	}
	held, err := store.QueueLeasesByInstance(ctx, app.ID)
	if err != nil || held[instance.ID] != 1 {
		t.Fatalf("lease not recorded on the instance: %v err=%v", held, err)
	}
}
