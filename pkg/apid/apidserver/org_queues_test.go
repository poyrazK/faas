package apidserver_test

// adr: 202610171908

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
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
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

type orgQueuePausedBody struct {
	reader           io.Reader
	entered, release chan struct{}
	once             sync.Once
}

func (b *orgQueuePausedBody) Read(p []byte) (int, error) {
	b.once.Do(func() { close(b.entered); <-b.release })
	return b.reader.Read(p)
}

func (b *orgQueuePausedBody) Close() error { return nil }

func TestOrgQueueHTTPWorkerSurvivesCreatorErasure(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	orgQueueHTTPJourney(t, *e.S(), *e.Store(), *e.Acct(), e.Do)
}

func orgQueueHTTPJourney(t *testing.T, server *apidserver.Server, store state.Store, creator state.Account, send func(*testing.T, string, string, any, map[string]string) *httptest.ResponseRecorder) {
	t.Helper()
	ctx := t.Context()
	org, err := store.CreateOrg(ctx, state.Org{Slug: "queue-company", Name: "Queue Company", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AddOrgMember(ctx, org.ID, creator.ID, state.OrgRoleDeveloper, nil); err != nil {
		t.Fatal(err)
	}
	_, _ = sharedAppMember(t, store, org, state.OrgRoleOwner, api.ScopesAdminOnly)
	admin, adminHeaders := sharedAppMember(t, store, org, state.OrgRoleAdmin, api.ScopesAdminOnly)
	_, devHeaders := sharedAppMember(t, store, org, state.OrgRoleDeveloper, api.ScopesAdminOnly)
	_, viewerHeaders := sharedAppMember(t, store, org, state.OrgRoleViewer, api.ScopesAdminOnly)
	_, billingHeaders := sharedAppMember(t, store, org, state.OrgRoleBilling, api.ScopesAdminOnly)
	key := func(actor state.Account, scopes []string) map[string]string {
		t.Helper()
		bearer, hash, err := api.GenerateAPIKey()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.CreateOrgAPIKey(ctx, org.ID, actor.ID, hash, "queue journey", scopes, nil); err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + bearer}
	}
	creatorHeaders := key(creator, api.ScopesAdminOnly)
	readHeaders := key(admin, []string{api.ScopeAppsRead})
	app, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "company-queue-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker, Status: state.AppActive, Manifest: state.AppManifest{ExecutionMode: api.ExecutionModeWorker}})
	if err != nil {
		t.Fatal(err)
	}
	base := "/v1/orgs/" + org.Slug + "/apps/" + app.Slug
	body := api.CreateQueueBindingRequest{Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: "worker", MaxConcurrency: 5}
	blocked := send(t, "POST", base+"/queue-bindings", body, creatorHeaders)
	if blocked.Code != 409 {
		t.Fatalf("unadopted queue write: %d %s", blocked.Code, blocked.Body)
	}
	if err := store.UpdateAccountProviderCustomerID(ctx, org.ID, "cus_queue_http"); err != nil {
		t.Fatal(err)
	}
	if err := store.UpdateAccountStripeSubscriptionItem(ctx, org.ID, "si_queue_http"); err != nil {
		t.Fatal(err)
	}
	created := send(t, "POST", base+"/queue-bindings", body, devHeaders)
	var binding api.QueueBindingResponse
	if created.Code != 201 || json.Unmarshal(created.Body.Bytes(), &binding) != nil {
		t.Fatalf("create queue: %d %s", created.Code, created.Body)
	}
	projectionApp, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, Slug: "company-push-projection", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.(state.QueueBindingConsumerStore).CreateQueueBindingWithConsumer(ctx, state.QueueBinding{AccountID: creator.ID, AppID: projectionApp.ID, Name: "hidden", QueueName: "hidden", Mode: "push", WorkloadClass: state.WorkloadClassWorker, Enabled: false, MaxConcurrency: 1}); err != nil {
		t.Fatal(err)
	}
	projections, err := store.ListTriggersForApp(ctx, projectionApp.ID)
	if err != nil || len(projections) != 1 {
		t.Fatalf("private projection: %+v %v", projections, err)
	}
	legacyID := projections[0].ID.String()
	// Legacy /v1/triggers covers personal apps only; a company trigger is an
	// unreviewed queue operation, refused even for its creator (878dcdc0f1).
	// A key bound to the creator's personal organization never reaches a
	// company app (#4982), so the refusal may come first as not found.
	legacyRead := send(t, "GET", "/v1/triggers/"+legacyID, nil, nil)
	roleRefused := legacyRead.Code == 403 && strings.Contains(legacyRead.Body.String(), string(api.CodeOrgRoleForbidden))
	keyRefused := legacyRead.Code == 404 && strings.Contains(legacyRead.Body.String(), string(api.CodeNotFound))
	if (!roleRefused && !keyRefused) || strings.Contains(legacyRead.Body.String(), legacyID) {
		t.Fatalf("legacy creator bypassed queue roles: %d %s", legacyRead.Code, legacyRead.Body)
	}
	legacyList := send(t, "GET", "/v1/triggers", nil, nil)
	var personalTriggers []api.Trigger
	if legacyList.Code != 200 || json.Unmarshal(legacyList.Body.Bytes(), &personalTriggers) != nil || len(personalTriggers) != 0 {
		t.Fatalf("personal inventory leaked company projection: %d %s", legacyList.Code, legacyList.Body)
	}
	for _, tc := range []struct {
		method, path string
		headers      map[string]string
		status       int
	}{
		{"GET", base + "/queue-bindings", viewerHeaders, 200},
		{"GET", base + "/queue-bindings/" + binding.ID + "/status", viewerHeaders, 200},
		{"GET", base + "/queues/state", viewerHeaders, 200},
		{"GET", base + "/queues/peek", viewerHeaders, 403},
		{"GET", base + "/queues/dead_letter", viewerHeaders, 403},
		{"GET", base + "/queue-bindings", billingHeaders, 403},
		{"GET", "/v1/orgs/" + org.Slug + "/queue-usage", billingHeaders, 200},
		{"POST", base + "/queues/jobs/leases", viewerHeaders, 403},
		{"POST", base + "/queues/jobs/leases", readHeaders, 403},
		{"POST", base + "/queues/dead_letter/missing/replay", devHeaders, 403},
		{"GET", strings.Replace(base, org.Slug, "wrong-company", 1) + "/queue-bindings", adminHeaders, 404},
		{"PUT", "/v1/apps/" + app.Slug + "/queue-workload", adminHeaders, 403},
		{"GET", "/v1/triggers?app_id=" + app.ID, adminHeaders, 404},
	} {
		rec := send(t, tc.method, tc.path, nil, tc.headers)
		if rec.Code != tc.status {
			t.Fatalf("%s %s: %d want %d: %s", tc.method, tc.path, rec.Code, tc.status, rec.Body)
		}
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: creator.ID, OrgID: org.ID, Slug: "queue-project"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateProjectEnvironment(ctx, state.ProjectEnvironment{AccountID: creator.ID, ProjectID: project.ID, Slug: "stage"}); err != nil {
		t.Fatal(err)
	}
	stageApp, err := store.CreateApp(ctx, state.App{AccountID: creator.ID, OrgID: org.ID, ProjectID: project.ID, Slug: "stage-queue-worker", WorkloadName: "stage-worker", Type: state.AppTypeApp, WorkloadClass: state.WorkloadClassWorker})
	if err != nil {
		t.Fatal(err)
	}
	stagePath := "/v1/orgs/" + org.Slug + "/projects/" + project.Slug + "/environments/stage/workloads/" + stageApp.Slug + "/queue-bindings"
	revision := int64(0)
	definitions := []api.ProjectEnvironmentQueueBinding{{Name: "jobs", QueueName: "jobs", Mode: "pull", WorkloadClass: "worker", Enabled: true, MaxConcurrency: 2}}
	configured := send(t, "PUT", stagePath, api.ReplaceProjectEnvironmentQueueBindingsRequest{ExpectedRevision: &revision, Bindings: &definitions}, devHeaders)
	if configured.Code != 200 {
		t.Fatalf("company stage configuration: %d %s", configured.Code, configured.Body)
	}
	if inspected := send(t, "GET", stagePath, nil, viewerHeaders); inspected.Code != 200 {
		t.Fatalf("company stage read: %d %s", inspected.Code, inspected.Body)
	}
	var ids []string
	for range 2 {
		rec := send(t, "POST", base+"/queues/send", api.QueueSendRequest{QueueName: "jobs", Payload: json.RawMessage(`{"company":"retained"}`)}, creatorHeaders)
		var sent api.QueueSendResponse
		if rec.Code != 201 || json.Unmarshal(rec.Body.Bytes(), &sent) != nil {
			t.Fatalf("enqueue: %d %s", rec.Code, rec.Body)
		}
		ids = append(ids, sent.ID)
	}
	storedMessage, err := store.InvocationByID(ctx, ids[1])
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:org-queues", Status: state.DeployLive})
	if err != nil {
		t.Fatal(err)
	}
	node, err := store.CreateComputeNode(ctx, state.ComputeNode{ID: uuid.NewString(), Name: "org-queue-node", TargetURL: "unix:///tmp/org-queue-node.sock", VPCPUs: 4, VCPUBudget: 4 * api.CPUOvercommit, MemMB: 4096, MaxConcurrency: 8, AdmissionCeilingMB: 4096, Active: true, Lifecycle: state.NodeLifecycleActive})
	if err != nil {
		t.Fatal(err)
	}
	instance, err := store.CreateInstanceWithMode(ctx, app.ID, dep.ID, string(state.StateRunning), 128, node.ID, "", string(state.InstanceModeWorker))
	if err != nil {
		t.Fatal(err)
	}
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(private, workloadidentity.DefaultIssuer, "company-queue", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	*server.WorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer)
	if err != nil {
		t.Fatal(err)
	}
	token, err := signer.Mint(time.Now(), creator.ID, app.ID, instance.ID, workloadidentity.QueuesAudience)
	if err != nil {
		t.Fatal(err)
	}
	worker := map[string]string{"Authorization": "Bearer " + token.AccessToken}
	first := send(t, "POST", base+"/queues/jobs/leases", api.QueueLeaseRequest{MaxMessages: 1}, worker)
	var leased api.QueueLeaseResponse
	if first.Code != 200 || json.Unmarshal(first.Body.Bytes(), &leased) != nil || len(leased.Messages) != 1 {
		t.Fatalf("worker lease: %d %s", first.Code, first.Body)
	}
	beforeQueued, err := store.CountPendingInvocations(ctx, app.ID, state.InvocationQueue)
	if err != nil {
		t.Fatal(err)
	}
	// Stop a human request after authorization, before its first intent write.
	paused := &orgQueuePausedBody{reader: strings.NewReader(`{"queue_name":"jobs","payload":{"revoked":true}}`), entered: make(chan struct{}), release: make(chan struct{})}
	request := httptest.NewRequest("POST", base+"/queues/send", paused)
	request.Header.Set("Authorization", creatorHeaders["Authorization"])
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	done := make(chan struct{})
	handler := server.Handler()
	go func() { handler.ServeHTTP(recorder, request); close(done) }()
	var release sync.Once
	t.Cleanup(func() { release.Do(func() { close(paused.release) }) })
	select {
	case <-paused.entered:
	case <-done:
		t.Fatalf("request did not reach body: %d %s", recorder.Code, recorder.Body)
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach authorization barrier")
	}
	if err := store.RemoveOrgMember(ctx, org.ID, creator.ID); err != nil {
		t.Fatal(err)
	}
	release.Do(func() { close(paused.release) })
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("revoked queue request did not finish")
	}
	if recorder.Code != 403 {
		t.Fatalf("revoked in-flight enqueue: %d %s", recorder.Code, recorder.Body)
	}
	if pending, err := store.CountPendingInvocations(ctx, app.ID, state.InvocationQueue); err != nil || pending != beforeQueued {
		t.Fatalf("revoked intent enqueued work: %d %v", pending, err)
	}
	if err := store.MarkAccountDeletionPending(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteAccount(ctx, creator.ID); err != nil {
		t.Fatal(err)
	}
	if inspected := send(t, "GET", stagePath, nil, viewerHeaders); inspected.Code != 200 {
		t.Fatalf("stage definition after erasure: %d %s", inspected.Code, inspected.Body)
	}
	ack := api.QueueLeaseReceiptsRequest{Receipts: []string{leased.Messages[0].Receipt}}
	denied := send(t, "POST", base+"/queues/jobs/leases:ack", ack, creatorHeaders)
	if denied.Code < 400 {
		t.Fatalf("erased human retained authority: %d", denied.Code)
	}
	acked := send(t, "POST", base+"/queues/jobs/leases:ack", ack, adminHeaders)
	if acked.Code != 200 {
		t.Fatalf("teammate receipt after erasure: %d %s", acked.Code, acked.Body)
	}
	second := send(t, "POST", base+"/queues/jobs/leases", api.QueueLeaseRequest{MaxMessages: 1}, worker)
	if second.Code != 200 || json.Unmarshal(second.Body.Bytes(), &leased) != nil || len(leased.Messages) != 1 {
		t.Fatalf("existing worker after erasure: %d %s", second.Code, second.Body)
	}
	var expectedPayload bytes.Buffer
	if err := json.Compact(&expectedPayload, storedMessage.Payload); err != nil {
		t.Fatal(err)
	}
	currentMessage, err := store.InvocationByID(ctx, ids[1])
	if err != nil || !bytes.Equal(currentMessage.Payload, storedMessage.Payload) {
		t.Fatalf("stored payload changed after erasure: %v", err)
	}
	if leased.Messages[0].ID != ids[1] || string(leased.Messages[0].Payload) != expectedPayload.String() {
		t.Fatalf("backlog identity or bytes changed: %+v", leased.Messages)
	}
	settled := send(t, "POST", base+"/queues/jobs/leases:ack", api.QueueLeaseReceiptsRequest{Receipts: []string{leased.Messages[0].Receipt}}, worker)
	if settled.Code != http.StatusOK {
		t.Fatalf("worker settlement: %d %s", settled.Code, settled.Body)
	}
	usage := send(t, "GET", "/v1/orgs/"+org.Slug+"/queue-usage", nil, billingHeaders)
	var counters api.OrgQueueUsageResponse
	if usage.Code != 200 || json.Unmarshal(usage.Body.Bytes(), &counters) != nil || counters.Bindings != 2 || counters.Pending != 0 || counters.Processing != 0 {
		t.Fatalf("company usage after erasure: %d %s", usage.Code, usage.Body)
	}
	wrong := send(t, "POST", strings.Replace(base, org.Slug, "wrong-company", 1)+"/queues/jobs/leases", nil, worker)
	if wrong.Code != 401 {
		t.Fatalf("workload crossed org alias: %d", wrong.Code)
	}
}
