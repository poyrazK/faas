//go:build metal && queue_binding_native

package e2e_test

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/queuestarter"
	"github.com/onebox-faas/faas/pkg/state"
)

type queueNativeJourney struct {
	h                                                              *e2etest.Harness
	pool                                                           *pgxpool.Pool
	store                                                          *state.PgStore
	client                                                         *api.Client
	key, slug, appID, deployment, sourcePath, bindingID, accountID string
	source                                                         []byte
	owned                                                          map[string]bool
	artifacts                                                      map[string]string
	manifest                                                       queuestarter.RuntimeManifest
}

func TestQueueBindingNativeMetal(t *testing.T) {
	queueNativePrerequisites(t)
	var j *queueNativeJourney
	verified := false
	// Registered first: emit only after all registered cleanups succeed. The
	// cleanup phase rehashes the bundle after stopping daemons, before temporary
	// source/binary files are intentionally removed by their registered cleanups.
	t.Cleanup(func() {
		if t.Failed() || !verified {
			return
		}
		encoded, err := json.Marshal(j.manifest)
		if err != nil {
			t.Error(err)
			return
		}
		t.Log(queuestarter.IdentityPrefix + string(encoded))
	})
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("disposable queue catalog missing")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatal(err)
	}
	j = newQueueNativeJourney(t, pool)
	phase := func(name string, fn func(*testing.T)) {
		t.Helper()
		if !t.Run(name, fn) {
			t.FailNow()
		}
	}
	phase("starter_deploy_and_park", func(t *testing.T) { j.deploy(t, "initial_app_layer"); j.park(t) })
	phase("consent_and_review_without_publication", func(t *testing.T) {

		_, err := j.client.SetQueueBindingReleasePolicy(t.Context(), j.slug, "default", api.SetQueueBindingReleasePolicyRequest{SetBindingReleasePolicyRequest: api.SetBindingReleasePolicyRequest{Mode: "enforce", ExpectedRevision: new(int64), MaxVerificationAge: "5m"}, QueueRequirements: map[string]string{"default": api.QueueEvidenceHandlerCompletion}})
		if err != nil {
			t.Fatal("cannot configure queue completion release enforcement")
		}
		j.waitHealthy(t)
		before := j.count(t)
		review, err := j.client.ReviewBindingVerification(t.Context(), j.slug, api.ReviewBindingVerificationRequest{DeploymentID: j.deployment})
		if err != nil || !review.Ready || !review.QueuePublishRequired || len(review.Plan) != 1 || review.Plan[0].Action != "queue-canary" {
			t.Fatal("queue review did not select the safe completion probe")
		}
		request := api.CreateQueueBindingCanaryRequest{DeploymentID: j.deployment, Binding: "default", TTLSeconds: 120}
		j.request(t, http.MethodPost, j.appPath()+"/bindings/queue-canaries", request, http.StatusBadRequest)
		j.request(t, http.MethodPost, j.appPath()+"/bindings/verification-runs", api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: review.DeploymentID, ReviewDigest: review.ReviewDigest, TTLSeconds: 120}, http.StatusBadRequest)
		if j.count(t) != before {
			t.Fatal("review or denied consent published work")
		}
		j.promote(t, false)
	})
	var completed api.QueueBindingCanary
	phase("parked_delivery_and_release_qualification", func(t *testing.T) {
		j.park(t)
		canary := j.createCanary(t, 120)
		completed = j.waitCanary(t, canary.ID, "succeeded")
		j.requireRestore(t)
		j.promote(t, true)
	})
	phase("scheduler_restart_receipt_resume", func(t *testing.T) {
		j.cancel(t, completed.ID)
		j.waitHealthy(t)
		review, err := j.client.ReviewBindingVerification(t.Context(), j.slug, api.ReviewBindingVerificationRequest{DeploymentID: j.deployment})
		if err != nil || !review.Ready || len(review.Plan) != 1 {
			t.Fatal("cannot review durable queue verification")
		}
		run, err := j.client.CreateBindingVerificationRun(t.Context(), j.slug, api.CreateBindingVerificationRunRequest{ID: uuid.NewString(), DeploymentID: review.DeploymentID, ReviewDigest: review.ReviewDigest, TTLSeconds: 120, QueuePublish: true})
		if err != nil {
			t.Fatal("cannot create durable queue receipt")
		}
		if err := j.h.KillSchedd(); err != nil {
			t.Fatal(err)
		}
		before := j.count(t)
		// Deliberately discard the successful admission response. The next client
		// has only the original receipt ID, as after an uncertain response.
		j.request(t, http.MethodPost, j.appPath()+"/bindings/verification-runs/"+run.ID+"/steps/0/admit", nil, http.StatusOK)
		if j.count(t) != before+1 {
			t.Fatal("durable admission did not publish exactly one invocation")
		}
		resumed, err := j.client.ResumeBindingVerificationRun(t.Context(), j.slug, run.ID)
		if err != nil || resumed.ID != run.ID || !resumed.ExpiresAt.Equal(run.ExpiresAt) || len(resumed.Steps) != 1 || resumed.Steps[0].ResourceID == "" {
			t.Fatal("resume lost receipt identity, resource or original expiry")
		}
		id := resumed.Steps[0].ResourceID
		j.owned[id] = true
		again, err := j.client.AdmitBindingVerificationRunStep(t.Context(), j.slug, run.ID, 0)
		if err != nil || again.ResourceID != id || j.count(t) != before+1 {
			t.Fatal("resume created a replacement queue message")
		}
		if err := j.h.RestartSchedd(); err != nil {
			t.Fatal(err)
		}
		completed = j.waitCanary(t, id, "succeeded")
		queueNativeWait(t, "durable receipt qualification", func(ctx context.Context) bool {
			result, err := j.client.CompleteBindingVerificationRun(ctx, j.slug, run.ID)
			return err == nil && result.ID == run.ID && result.Status == "passed" && result.ExpiresAt.Equal(run.ExpiresAt)
		})
		if j.count(t) != before+1 {
			t.Fatal("scheduler recovery duplicated publication")
		}
		j.promote(t, true)
	})
	phase("cancelled_evidence_rejected", func(t *testing.T) { j.cancel(t, completed.ID); j.promote(t, false) })
	phase("expired_evidence_rejected", func(t *testing.T) {
		// Observe a real successful delivery before its short, original deadline.
		canary := j.createCanary(t, 15)
		j.waitCanary(t, canary.ID, "succeeded")
		j.promote(t, true)
		j.waitCanary(t, canary.ID, "expired")
		j.promote(t, false)
	})
	phase("completed_replay_rejected", func(t *testing.T) {
		completed = j.waitCanary(t, j.createCanary(t, 120).ID, "succeeded")
		before := j.count(t)
		j.request(t, http.MethodPost, j.appPath()+"/queues/dead_letter/"+completed.ID+"/replay", nil, http.StatusNotFound)
		if j.count(t) != before {
			t.Fatal("completed-canary replay published replacement work")
		}
		j.cancel(t, completed.ID)
		j.promote(t, false)
	})
	phase("wrong_deployment_evidence_rejected", func(t *testing.T) {
		previous := j.deployment
		j.deploy(t, "candidate_app_layer")
		if previous == j.deployment {
			t.Fatal("second source deployment reused deployment identity")
		}
		j.waitHealthy(t)
		// Complete on the still-serving old revision after the candidate build,
		// so its proof is fresh and expiry cannot explain the rejection.
		prior, err := j.client.CreateQueueBindingCanary(t.Context(), j.slug, api.CreateQueueBindingCanaryRequest{DeploymentID: previous, Binding: "default", TTLSeconds: 120, PublishConsent: true})
		if err != nil || prior.ID == "" {
			t.Fatal("cannot publish old-revision control canary")
		}
		j.owned[prior.ID] = true
		j.waitCanaryFor(t, prior.ID, "succeeded", previous)
		j.promote(t, false)
		j.waitCanary(t, j.createCanary(t, 120).ID, "succeeded")
		j.promote(t, true)
	})
	phase("independent_cleanup", func(t *testing.T) {
		if j.count(t) != len(j.owned) {
			t.Fatal("independent inventory found untracked or duplicated queue invocations")
		}
		for id := range j.owned {
			j.cancel(t, id)
		}
		j.request(t, http.MethodDelete, j.appPath()+"/queue-bindings/"+j.bindingID, nil, http.StatusNoContent)
		queueNativeWait(t, "queue consumer retirement", func(ctx context.Context) bool {
			bindings, err := j.store.ListQueueBindingsForApp(ctx, j.accountID, j.appID)
			return err == nil && len(bindings) == 0
		})
		j.h.Stop()
		for id := range j.owned {
			canary, err := j.store.GetQueueBindingCanary(t.Context(), j.accountID, j.appID, id)
			if err != nil || canary.Status != "cancelled" {
				t.Fatal("cleanup left qualifying queue authority")
			}
		}
		final, err := e2etest.QueueGuestRuntimeManifest(j.artifacts, j.manifest.SourceRevision, true)
		if err != nil || !reflect.DeepEqual(final, j.manifest) {
			t.Fatal("queue runtime/source bundle changed")
		}
		verified = true
	})
}

func queueNativePrerequisites(t *testing.T) {
	t.Helper()
	if os.Getenv("GREGALE_QUEUE_NATIVE_ACCEPTANCE") != "1" {
		t.Fatal("use make test-queue-bindings-native for isolated queue acceptance")
	}
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		t.Fatal("queue acceptance requires root on x86_64 Linux")
	}
	for _, name := range []string{"DATABASE_URL", "FAAS_TEST_KERNEL", "FAAS_BUILDER_BASE_PATH", "FAAS_GUEST_INIT"} {
		if os.Getenv(name) == "" {
			t.Fatalf("missing queue prerequisite %s", name)
		}
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("queue catalog tests cannot be skipped")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Fatal("queue acceptance requires KVM")
	}
	if err := e2etest.ValidateNativeGuestInit(os.Getenv("FAAS_GUEST_INIT")); err != nil {
		t.Fatal(err)
	}
}

func newQueueNativeJourney(t *testing.T, pool *pgxpool.Pool) *queueNativeJourney {
	t.Helper()
	// Complete binding inventory and promotion use the ordinary PostgreSQL
	// catalog service even when this app has no database bindings. Its fixture
	// backend has no real credential; provisioning and collection stay disabled.
	config := filepath.Join(t.TempDir(), "managed-postgres.json")
	configuration, err := json.Marshal(e2etest.QueueGuestCatalogConfig())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, configuration, 0600); err != nil {
		t.Fatal(err)
	}
	environment := []string{"FAAS_MANAGED_POSTGRES_CONFIG=" + config, "GREGALE_QUEUE_UNUSED_PROVIDER_KEY=unused-native-fixture"}
	for _, name := range []string{"GRYPE_DB_CACHE_DIR", "GRYPE_DB_AUTO_UPDATE", "FAAS_STORAGE_ROOT"} {
		if value := os.Getenv(name); value != "" {
			environment = append(environment, name+"="+value)
		}
	}
	h := e2etest.Start(t, pool, e2etest.All|e2etest.GatewaydPublic, environment...)
	j := &queueNativeJourney{h: h, pool: pool, store: state.NewPgStore(pool), owned: map[string]bool{}, slug: "queue-native-" + randHexSuffix(), sourcePath: filepath.Join(t.TempDir(), "source.tar.gz")}
	t.Cleanup(func() {
		if t.Failed() {
			h.DumpLogs(t)
		}
		h.Stop()
	})
	j.key = h.SeedAccount(t.Context(), api.PlanPro)
	j.accountID = accountIDFromKey(t, t.Context(), pool, j.key)
	j.client = api.NewClient(h.APIDURL, j.key)
	falsy := false
	body := j.request(t, http.MethodPost, "/v1/apps", api.CreateAppRequest{Slug: j.slug, Type: "function", Runtime: "node22", RAMMB: 256, RequireAuthn: &falsy, RevisionPinTTLSeconds: 3600}, http.StatusCreated)
	var app api.AppResponse
	if json.Unmarshal(body, &app) != nil || app.ID == "" {
		t.Fatal("missing queue app identity")
	}
	j.appID = app.ID
	files, err := queuestarter.Source()
	if err != nil {
		t.Fatal(err)
	}
	j.source = buildTarGz(t, files)
	if err := os.WriteFile(j.sourcePath, j.source, 0600); err != nil {
		t.Fatal(err)
	}
	request, err := queuestarter.BindingRequest()
	if err != nil {
		t.Fatal(err)
	}
	body = j.request(t, http.MethodPost, j.appPath()+"/queue-bindings", request, http.StatusCreated)

	var out api.QueueBindingResponse
	if json.Unmarshal(body, &out) != nil || out.ID == "" || !reflect.DeepEqual(out.RetryPolicy, request.RetryPolicy) {
		t.Fatal("missing queue binding identity")
	}
	j.bindingID = out.ID
	revision, err := exec.CommandContext(t.Context(), "git", "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	j.artifacts = map[string]string{"kernel": os.Getenv("FAAS_TEST_KERNEL"), "guest_init": os.Getenv("FAAS_GUEST_INIT"), "builder_base": e2etest.StagedBuilderBasePath()}
	for _, name := range append(append([]string(nil), e2etest.DaemonBinaries...), e2etest.StaticHelperBinaries...) {
		j.artifacts[name] = filepath.Join(h.BinDir, name)
	}
	for _, name := range []string{"firecracker", "jailer"} {
		j.artifacts[name], err = exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
	}
	j.artifacts["e2e_test"], err = os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	j.manifest, err = e2etest.QueueGuestRuntimeManifest(j.artifacts, strings.TrimSpace(string(revision)), false)
	if err != nil {
		t.Fatal(err)
	}
	return j
}
