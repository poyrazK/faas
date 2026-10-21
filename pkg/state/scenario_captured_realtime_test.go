// adr: 20261021180000
package state

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestScenarioPlatformCaptureNamesRealtimeEndpoints(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := newEffectFixture(t, backend, actionCaptureTestRun)
			ctx := t.Context()
			capturer := f.base.(ScenarioPlatformCaptureStore)
			ordinary, err := f.base.CreateApp(ctx, App{AccountID: f.account.ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			endpoint := func(appID string) string {
				created, err := f.base.(ManagedRealtimeEndpointStore).CreateManagedRealtimeEndpointIfUnderQuota(ctx, ManagedRealtimeEndpoint{AccountID: f.account.ID, AppID: appID, CallbackURL: "https://example.com",
					ConnectPath: "/connect", MessagePath: "/message", DisconnectPath: "/disconnect",
					CallbackAuthTokenSealed: []byte("callback"), AuthTokenSealed: []byte("client"), Enabled: true}, 5, 10)
				if err != nil {
					t.Fatal(err)
				}
				return created.ID
			}
			first, second, foreign := endpoint(f.app.ID), endpoint(f.app.ID), endpoint(ordinary.ID)
			publish := func(slug, endpointID, key, data string) error {
				_, _, err := capturer.CaptureScenarioPlatformCall(ctx, f.app.ID, ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimePublish, Address: slug,
					Endpoint: endpointID, Name: "updates", IdempotencyKey: key, Message: json.RawMessage(`{"data_base64":"` + data + `"}`)})
				return err
			}
			if err := publish(f.app.Slug, second, "", "aGk="); err != nil {
				t.Fatalf("publish to a member endpoint: %v", err)
			}
			if err := publish("primary", first, "k1", "aGk="); err != nil {
				t.Fatalf("publish by workload name: %v", err)
			}
			if err := publish("primary", first, "k1", "aGk="); err != nil {
				t.Fatalf("replayed publish: %v", err)
			}
			if err := publish("primary", first, "k1", "Ynll"); !errors.Is(err, ErrScenarioIdempotencyConflict) {
				t.Fatalf("reused key with another message: %v", err)
			}
			if err := publish(ordinary.Slug, foreign, "", "aGk="); err != nil {
				t.Fatalf("publish to an account app endpoint: %v", err)
			}
			if err := publish(f.app.Slug, foreign, "", "aGk="); !errors.Is(err, ErrNotFound) {
				t.Fatalf("endpoint of another app: %v", err)
			}
			if err := publish(f.app.Slug, uuid.NewString(), "", "aGk="); !errors.Is(err, ErrNotFound) {
				t.Fatalf("unknown endpoint: %v", err)
			}
			if _, _, err := capturer.CaptureScenarioPlatformCall(ctx, f.app.ID, ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindRealtimeSignal, Address: f.app.Slug,
				Endpoint: first, Name: "updates", Message: json.RawMessage(`{"data":{"typing":true}}`)}); err != nil {
				t.Fatalf("signal: %v", err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" {
				t.Fatalf("invalid sealed realtime evidence: %+v %v", snapshot, err)
			}
			targets := map[string]int{}
			for _, call := range snapshot.PlatformCalls {
				targets[call.Kind+" "+call.Target+" "+call.Name]++
			}
			want := map[string]int{
				"realtime_publish workload:primary/endpoint#1 updates":                                          1,
				"realtime_publish workload:primary/endpoint#2 updates":                                          1,
				"realtime_publish app:" + ordinary.Slug + "/endpoint:" + canonicalMemUUID(foreign) + " updates": 1,
				"realtime_signal workload:primary/endpoint#1 updates":                                           1,
			}
			if len(targets) != len(want) {
				t.Fatalf("sealed realtime calls = %v, want %v", targets, want)
			}
			for key, count := range want {
				if targets[key] != count {
					t.Fatalf("sealed realtime calls = %v, want %v", targets, want)
				}
			}
		})
	}
}
