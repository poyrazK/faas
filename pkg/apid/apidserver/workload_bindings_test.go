package apidserver_test

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/capgrant"
	"github.com/onebox-faas/faas/pkg/platformflags"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/workloadidentity"
)

// A project service calls Gregale on itself or a sibling app with its own
// gregale:bindings workload token, authorized by its gregale.yaml `uses:`
// grants, instead of an admin API key (ADR-20261122191423).
func TestBindingTokensFollowUsesGrants(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	store := *e.Store()
	acct := *e.Acct()
	ctx := t.Context()

	project, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "assetforge"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := store.CreateProject(ctx, state.Project{AccountID: acct.ID, Slug: "elsewhere"})
	if err != nil {
		t.Fatal(err)
	}
	create := func(projectID, slug, workload string, uses *capgrant.Uses) state.App {
		t.Helper()
		app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, ProjectID: projectID, Slug: slug, WorkloadName: workload,
			Status: state.AppActive, RAMMB: 256, Manifest: state.AppManifest{Uses: uses}})
		if err != nil {
			t.Fatal(err)
		}
		return app
	}
	web := create(project.ID, "assetforge-web", "web", &capgrant.Uses{
		Queues:       []capgrant.QueueGrant{{App: "worker", Queue: "transforms"}},
		Realtime:     []capgrant.RealtimeGrant{{App: "self", Channels: "room:*"}},
		DelayedTasks: []string{"self"},
	})
	create(project.ID, "assetforge-worker", "worker", nil)
	create(other.ID, "elsewhere-worker", "worker", nil)

	instance := func(appID, scope string) state.Instance {
		t.Helper()
		dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: appID, Scope: scope, Kind: state.DeploymentKindImage,
			ImageDigest: "sha256:" + uuid.NewString(), Status: state.DeployLive})
		if err != nil {
			t.Fatal(err)
		}
		in, err := store.CreateInstanceWithMode(ctx, appID, dep.ID, string(state.StateRunning), 256, "default-local", "", "")
		if err != nil {
			t.Fatal(err)
		}
		return in
	}
	live := instance(web.ID, "")

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := workloadidentity.NewSigner(key, workloadidentity.DefaultIssuer, "bindings-test", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if *(*e.S()).WorkloadVerifier(), err = workloadidentity.NewVerifier(signer.JWKS(), workloadidentity.DefaultIssuer); err != nil {
		t.Fatal(err)
	}
	bearer := func(in state.Instance, environment, audience string) map[string]string {
		t.Helper()
		token, err := signer.MintEnvironment(time.Now(), acct.ID, web.ID, in.ID, environment, audience)
		if err != nil {
			t.Fatal(err)
		}
		return map[string]string{"Authorization": "Bearer " + token.AccessToken}
	}
	token := bearer(live, "", workloadidentity.BindingsAudience)

	send := func(slug, queue string, auth map[string]string) (int, api.Problem) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/queues/send", api.QueueSendRequest{QueueName: queue, Payload: json.RawMessage(`{}`)}, auth)
		var problem api.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &problem)
		return rec.Code, problem
	}

	// Off by default: the token is refused, API keys are unaffected.
	if code, _ := send("assetforge-worker", "transforms", token); code != http.StatusNotFound {
		t.Fatalf("flag off: %d", code)
	}
	if code, p := send("assetforge-worker", "transforms", nil); code != http.StatusCreated {
		t.Fatalf("API key send: %d %+v", code, p)
	}

	(*e.S()).EnablePlatformFlagForAll(platformflags.CrossAppBindings)

	if code, p := send("assetforge-worker", "transforms", token); code != http.StatusCreated {
		t.Fatalf("granted send: %d %+v", code, p)
	}
	code, p := send("assetforge-worker", "thumbnails", token)
	if code != http.StatusForbidden || p.Code != apidserver.CodeBindingNotGranted || !strings.Contains(p.Detail, `queues "thumbnails" on "worker"`) {
		t.Fatalf("ungranted queue: %d %+v", code, p)
	}
	// "worker" names the service of the caller's own project only.
	if code, p := send("elsewhere-worker", "transforms", token); code != http.StatusForbidden || p.Code != apidserver.CodeBindingNotGranted {
		t.Fatalf("other project's worker: %d %+v", code, p)
	}
	stage := e.Do(t, http.MethodPost, "/v1/apps/assetforge-worker/queues/send",
		api.QueueSendRequest{QueueName: "transforms", Environment: "staging", Payload: json.RawMessage(`{}`)}, token)
	if stage.Code == http.StatusCreated || stage.Code == http.StatusOK {
		t.Fatalf("binding send to a stage queue: %d %s", stage.Code, stage.Body)
	}
	if code, _ := send("assetforge-web", "", token); code != http.StatusForbidden {
		t.Fatalf("self queue without a grant: %d", code)
	}
	// A token naming a stage environment is refused before the instance's
	// deployment is read. It is a 403, not a 401: a correctly signed token
	// must not count against the shared per-IP auth-failure limiter.
	if code, p := send("assetforge-worker", "transforms", bearer(live, "staging", workloadidentity.BindingsAudience)); code != http.StatusForbidden || p.Code != api.CodeForbidden {
		t.Fatalf("staging token: %d %+v", code, p)
	}
	if code, _ := send("assetforge-worker", "transforms", bearer(live, "", workloadidentity.QueuesAudience)); code != http.StatusUnauthorized {
		t.Fatalf("queues audience: %d", code)
	}
	// A slug that does not exist answers like an existing app the caller has
	// no grant on, so a workload cannot probe which slugs exist.
	if code, p := send("no-such-app", "transforms", token); code != http.StatusForbidden || p.Code != apidserver.CodeBindingNotGranted {
		t.Fatalf("missing target: %d %+v", code, p)
	}

	batch := e.Do(t, http.MethodPost, "/v1/apps/assetforge-worker/queues/send-batch", api.QueueSendBatchRequest{QueueName: "other",
		Messages: []api.QueueSendBatchMessage{{Payload: json.RawMessage(`{}`)}}}, token)
	if batch.Code != http.StatusForbidden {
		t.Fatalf("ungranted batch: %d %s", batch.Code, batch.Body)
	}

	delayed := func(slug string) int {
		t.Helper()
		return e.Do(t, http.MethodPost, "/v1/apps/"+slug+"/delayed-tasks", api.DelayedTaskRequest{
			Payload: json.RawMessage(`{}`), ScheduledAt: time.Now().Add(time.Hour).UTC()}, token).Code
	}
	if code := delayed("assetforge-web"); code != http.StatusCreated {
		t.Fatalf("delayed task on self: %d", code)
	}
	if code := delayed("assetforge-worker"); code != http.StatusForbidden {
		t.Fatalf("delayed task on worker: %d", code)
	}

	publish := func(channel string) (int, api.Problem) {
		t.Helper()
		rec := e.Do(t, http.MethodPost, "/v1/apps/assetforge-web/realtime/endpoints/"+uuid.NewString()+"/channels/"+channel+"/publish",
			map[string]any{"event": "update", "data": "{}"}, token)
		var problem api.Problem
		_ = json.Unmarshal(rec.Body.Bytes(), &problem)
		return rec.Code, problem
	}
	if code, p := publish("lobby"); code != http.StatusForbidden || p.Code != apidserver.CodeBindingNotGranted {
		t.Fatalf("ungranted channel: %d %+v", code, p)
	}
	// A granted channel passes the binding check and reaches the handler,
	// which answers for the missing endpoint.
	if code, p := publish("room:1"); code == http.StatusUnauthorized || p.Code == apidserver.CodeBindingNotGranted {
		t.Fatalf("granted channel refused by the binding check: %d %+v", code, p)
	}
	// A 128-byte Idempotency-Key, the most the route accepts, still fits
	// after the binding route scopes it to the caller app.
	keyed := map[string]string{"Authorization": token["Authorization"], "Idempotency-Key": strings.Repeat("k", 128)}
	rec := e.Do(t, http.MethodPost, "/v1/apps/assetforge-web/realtime/endpoints/"+uuid.NewString()+"/channels/room:1/publish",
		map[string]any{"event": "update", "data": "{}"}, keyed)
	if strings.Contains(rec.Body.String(), "Idempotency-Key") {
		t.Fatalf("scoped idempotency key rejected: %d %s", rec.Code, rec.Body)
	}
}
