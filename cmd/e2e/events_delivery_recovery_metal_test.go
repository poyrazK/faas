//go:build metal

package e2e_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/e2etest"
	"github.com/onebox-faas/faas/pkg/e2etest/eventdelivery"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 618
// Both handlers execute in real Firecracker guests. Only their OCI registry
// and ingress Host-header adapters are fixtures; event requests traverse the
// actual scheduler, public/internal gateways, VMMD and guest transport.
func TestEventDeliveryRecoveryWholeReceiptMetal(t *testing.T) {
	runNativeEventDeliveryRecovery(t, "event", "0")
}

func TestEventDeliveryRecoveryIndependentRecipientsMetal(t *testing.T) {
	runNativeEventDeliveryRecovery(t, "recipient", "1")
}

func runNativeEventDeliveryRecovery(t *testing.T, mode, adoption string) {
	t.Helper()
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		t.Skip("native delivery qualification requires Linux/amd64")
	}
	if os.Getenv("FAAS_TEST_KERNEL") == "" || os.Getenv("FAAS_BUILDER_BASE_PATH") == "" || os.Getenv("DATABASE_URL") == "" {
		t.Skip("native delivery qualification requires kernel, builder base and PostgreSQL")
	}
	if _, err := os.Stat("/dev/kvm"); err != nil {
		t.Skipf("native delivery qualification requires /dev/kvm: %v", err)
	}
	if os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("native delivery qualification cannot disable PostgreSQL tests")
	}
	pool := pgtest.OpenMigrated(t)
	if pool == nil {
		t.Fatal("native delivery qualification requires PostgreSQL")
	}
	if err := dbMigrateUp(t, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Minute)
	defer cancel()
	registry := e2etest.NewFakeRegistry()
	t.Cleanup(registry.Close)
	builder, _ := e2etest.HelloImage("onebox-faas/builder-base", "")
	e2etest.OverrideBuilderBase(t, registry.AddImage("onebox-faas/builder-base", builder))
	base, _ := e2etest.BaseLayerImage("onebox-faas/deploy-base", "event-delivery-native")
	e2etest.OverrideDeployBase(t, registry.AddImage("onebox-faas/deploy-base", base))
	image, _, err := e2etest.EventDeliveryConsumerImage(ctx, "library/event-delivery-consumer")
	if err != nil {
		t.Fatal(err)
	}
	imageRef := registry.AddImage("library/event-delivery-consumer", image)
	h := e2etest.Start(t, pool, e2etest.DeployWake|e2etest.GatewaydPublic,
		"FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED="+adoption)
	defer h.DumpLogs(t)
	key := h.SeedAccount(ctx, api.PlanHobby, "event-delivery-native-"+mode)
	store := state.NewPgStore(pool)
	controlToken := uuid.NewString()
	healthy, healthyURL, healthyInstance := deployNativeEventConsumer(ctx, t, h, store, key, "delivery-good", imageRef, controlToken)
	failing, failingURL, failingInstance := deployNativeEventConsumer(ctx, t, h, store, key, "delivery-bad", imageRef, controlToken)
	source, eventType, eventID := "acceptance.events.delivery", "delivery.recovery", "native-delivery-"+uuid.NewString()
	for _, app := range []state.App{healthy, failing} {
		if _, _, err := store.UpsertEventSubscription(ctx, app.AccountID, app.ID, source, eventType, json.RawMessage(`{}`)); err != nil {
			t.Fatalf("configure event subscription: %v", err)
		}
	}
	if err := h.KillSchedd(); err != nil {
		t.Fatal(err)
	}
	gateCtx, gateCancel := context.WithTimeout(ctx, 3*time.Minute)
	defer gateCancel()
	report, err := eventdelivery.Run(gateCtx, eventdelivery.Config{
		APIURL: h.APIDURL, APIToken: key, ControlToken: controlToken,
		HealthyApp: healthy.Slug, FailingApp: failing.Slug, HealthyURL: healthyURL, FailingURL: failingURL,
		Source: source, EventType: eventType, EventID: eventID, RoutingMode: mode,
		ExpectedAttempts: api.MustLimitsFor(api.PlanHobby).MaxQueueAttempts,
	}, eventdelivery.Hooks{
		Accepted: func(ctx context.Context) error {
			backlog, err := api.NewClient(h.APIDURL, key).GetEventBacklog(ctx, api.EventBacklogOptions{})
			if err != nil {
				return err
			}
			if len(backlog.Recipients) != 2 || len(backlog.Consumers) != 2 || backlog.UnattributedReceipts != 0 {
				return fmt.Errorf("accepted native event lost pending consumers: %+v", backlog)
			}
			for _, recipient := range backlog.Recipients {
				if recipient.EventSource != source || recipient.EventID != eventID || recipient.State != "pending" || recipient.Attempts != 0 {
					return fmt.Errorf("incorrect accepted native recipient: %+v", recipient)
				}
			}
			return h.RestartScheddContext(ctx)
		},
		RetryPending: func(ctx context.Context) error {
			if err := h.SetScheddEnv("FAAS_EVENT_RECIPIENT_CLAIMS_ENABLED", "0"); err != nil {
				return err
			}
			return h.RestartScheddContext(ctx)
		},
		DeadLetter: func(context.Context) error { return h.RestartAPID() },
	})
	if err != nil {
		t.Fatal(err)
	}
	if report.Gate != "pass" {
		t.Fatalf("native delivery gate: %+v", report)
	}
	// A fresh guest would reset the controlled in-memory evidence. Check that
	// the pinned single consumer processes survived platform process restarts.
	for appID, instanceID := range map[string]string{healthy.ID: healthyInstance, failing.ID: failingInstance} {
		instances, err := store.ListActiveInstancesForApp(ctx, appID, 2)
		if err != nil || len(instances) != 1 || instances[0].ID != instanceID || instances[0].State != string(state.StateRunning) {
			t.Fatalf("native consumer instance changed during recovery: instances=%+v err=%v", instances, err)
		}
	}
	foreignKey := h.SeedAccount(ctx, api.PlanHobby, "event-delivery-native-foreign")
	receiptPath := "/v1/events/receipt?" + url.Values{"source": {source}, "id": {eventID}}.Encode()
	for _, path := range []string{receiptPath, report.Receipt.Recipients[0].AttemptHistoryURL} {
		_, code, err := nativeEventAPIRequest(ctx, h, foreignKey, http.MethodGet, path, nil)
		if err != nil || code != http.StatusNotFound {
			t.Fatalf("foreign native delivery evidence: status=%d err=%v", code, err)
		}
	}
	evidence, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native event delivery: mode=%s healthy_instance=%s failing_instance=%s report=%s", mode, healthyInstance, failingInstance, evidence)
}

func deployNativeEventConsumer(ctx context.Context, t *testing.T, h *e2etest.Harness, store *state.PgStore, key, slug, image, token string) (state.App, string, string) {
	t.Helper()
	falsy := false
	body, code, err := nativeEventAPIRequest(ctx, h, key, http.MethodPost, "/v1/apps", api.CreateAppRequest{
		Slug: slug, Type: "app", RequireAuthn: &falsy, IdleTimeoutS: 120, MaxConcurrency: 1,
	})
	if err != nil || code != http.StatusCreated {
		t.Fatalf("create native consumer %s: status=%d err=%v body=%s", slug, code, err, body)
	}
	var created api.AppResponse
	if err := json.Unmarshal(body, &created); err != nil {
		t.Fatal(err)
	}
	body, code, err = nativeEventAPIRequest(ctx, h, key, http.MethodPost, "/v1/apps/"+slug+"/deployments", api.CreateDeploymentRequest{
		Image: image, Overrides: &api.CreateDeploymentOverrides{Env: map[string]string{"GREGALE_EVENT_GATE_CONTROL_TOKEN": token}},
	})
	if err != nil || code != http.StatusAccepted {
		t.Fatalf("deploy native consumer %s: status=%d err=%v", slug, code, err)
	}
	var accepted api.DeploymentResponse
	if err := json.Unmarshal(body, &accepted); err != nil {
		t.Fatal(err)
	}
	deployment, err := e2etest.WaitForDeploymentLive(ctx, t, h.Pool, accepted.ID, 90*time.Second)
	if err != nil || deployment.RootfsKey == "" || deployment.RootfsBytes <= 0 {
		t.Fatalf("native consumer image preparation failed: deployment=%+v err=%v", deployment, err)
	}
	body, code, err = nativeEventAPIRequest(ctx, h, key, http.MethodPatch, "/v1/apps/"+slug, map[string]any{
		"require_authn": false, "public_auth": map[string]any{"mode": "open"},
		"scaling_policy": api.ScalingPolicy{MinInstances: 1, MaxInstances: 1},
	})
	if err != nil || code != http.StatusOK {
		t.Fatalf("pin native consumer %s: status=%d err=%v body=%s", slug, code, err, body)
	}
	base := nativeEventIngress(t, h, slug)
	// This protected control endpoint always reaches the actual application;
	// /healthz may be answered by the gateway without waking the guest.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/__gate/events?source=warmup&event_id=warmup", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Gate-Control-Token", token)
	response, err := h.HTTPClient().Do(req)
	if err != nil {
		t.Fatalf("wake native consumer: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusNotFound {
		t.Fatalf("unregistered native consumer event: status=%d", response.StatusCode)
	}
	if _, err := e2etest.WaitForInstanceState(ctx, t, h.Pool, created.ID, state.StateRunning, 30*time.Second); err != nil {
		t.Fatalf("native consumer did not become running: %v", err)
	}
	// WaitForInstanceState includes historical parked rows. Capture only the
	// current resident so snapshot-prime history cannot become guest identity.
	instances, err := store.ListActiveInstancesForApp(ctx, created.ID, 2)
	if err != nil || len(instances) != 1 || instances[0].State != string(state.StateRunning) {
		t.Fatalf("native consumer needs exactly one running guest: instances=%+v err=%v", instances, err)
	}
	app, err := store.AppByID(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	return app, base, instances[0].ID
}

// The adapters set the normal app hostname on control requests to the actual
// public gateway. They never handle, synthesize or count event deliveries.
func nativeEventIngress(t *testing.T, h *e2etest.Harness, slug string) string {
	t.Helper()
	target, err := url.Parse(h.GatewayPublicURL)
	if err != nil || target.Host == "" {
		t.Fatalf("native gate requires public ingress: url=%s err=%v", h.GatewayPublicURL, err)
	}
	proxy := httputil.NewSingleHostReverseProxy(target)
	director := proxy.Director
	proxy.Director = func(request *http.Request) {
		director(request)
		request.Host = slug + ".apps.test.example"
	}
	server := httptest.NewServer(proxy)
	t.Cleanup(server.Close)
	return server.URL
}

func nativeEventAPIRequest(ctx context.Context, h *e2etest.Harness, key, method, path string, body any) ([]byte, int, error) {
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, 0, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, h.APIDURL+path, bytes.NewReader(payload))
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+key)
	req.Header.Set("Content-Type", "application/json")
	response, err := h.HTTPClient().Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return data, response.StatusCode, err
}
