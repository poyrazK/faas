// adr: 20261021150000
package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/actioncompare"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestScenarioPlatformCaptureSealsWorkflowCallsIntoRunEvidence(t *testing.T) {
	for _, backend := range []string{"memory", "postgres"} {
		t.Run(backend, func(t *testing.T) {
			f := state.NewEffectFixture(t, backend, state.ActionCaptureTestRun)
			ctx := t.Context()
			base, app := *f.Base(), f.App()
			capturer := base.(state.ScenarioPlatformCaptureStore)
			ordinary, err := base.CreateApp(ctx, state.App{AccountID: f.Account().ID, Slug: "ordinary-" + uuid.NewString()[:8], Type: state.AppTypeApp, Runtime: "node22", RAMMB: 256})
			if err != nil {
				t.Fatal(err)
			}
			start := func(slug, key, input string) (state.ScenarioPlatformCallResult, bool, error) {
				return capturer.CaptureScenarioPlatformCall(ctx, app.ID, state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun, Address: slug,
					Name: "invoice", IdempotencyKey: key, Message: json.RawMessage(`{"input":` + input + `}`)})
			}
			event := func(run, key, name string) (state.ScenarioPlatformCallResult, bool, error) {
				return capturer.CaptureScenarioPlatformCall(ctx, app.ID, state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowEvent, Address: run,
					Name: name, IdempotencyKey: key, Message: json.RawMessage(`{"payload":{"by":"ops"}}`)})
			}
			if _, captured, err := capturer.CaptureScenarioPlatformCall(ctx, ordinary.ID, state.ScenarioPlatformCallCapture{Kind: api.ScenarioPlatformKindWorkflowRun,
				Address: app.Slug, Name: "invoice", Message: json.RawMessage(`{}`)}); captured || err != nil {
				t.Fatalf("an ordinary app's start was captured: %v %v", captured, err)
			}
			if _, captured, err := start("missing-"+uuid.NewString()[:8], "", `{}`); !captured || !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("start of an unknown app: %v %v", captured, err)
			}
			first, _, err := start(app.Slug, "k1", `{"order":1}`)
			if err != nil || uuid.Validate(first.ID) != nil || first.Replayed {
				t.Fatalf("member start: %+v %v", first, err)
			}
			if replay, _, err := start(app.Slug, "k1", `{"order":1.0}`); err != nil || !replay.Replayed || replay.ID != first.ID {
				t.Fatalf("idempotent replay: %+v %v", replay, err)
			}
			if _, _, err := start(app.Slug, "k1", `{"order":2}`); !errors.Is(err, state.ErrScenarioIdempotencyConflict) {
				t.Fatalf("reused key with other input: %v", err)
			}
			second, _, err := start(app.Slug, "", `{"order":3}`)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := start(ordinary.Slug, "", `{}`); err != nil {
				t.Fatalf("start of an account app: %v", err)
			}
			if _, _, err := event(second.ID, "e1", "approved"); err != nil {
				t.Fatalf("event to a captured run: %v", err)
			}
			if replay, _, err := event(second.ID, "e1", "approved"); err != nil || !replay.Replayed {
				t.Fatalf("replayed event: %+v %v", replay, err)
			}
			if _, _, err := event(second.ID, "e1", "rejected"); !errors.Is(err, state.ErrScenarioIdempotencyConflict) {
				t.Fatalf("reused event key with another name: %v", err)
			}
			if _, _, err := event(uuid.NewString(), "", "approved"); !errors.Is(err, state.ErrNotFound) {
				t.Fatalf("event to an unknown run: %v", err)
			}
			external := &state.WorkflowRun{AppID: ordinary.ID, WorkflowName: "invoice", Input: json.RawMessage(`{}`), DefinitionSnapshot: json.RawMessage(`{"name":"invoice","steps":[]}`)}
			if err := base.(state.WorkflowStore).CreateWorkflowRun(ctx, external); err != nil {
				t.Fatal(err)
			}
			if _, _, err := event(external.ID, "", "approved"); err != nil {
				t.Fatalf("event to an account run outside the rehearsal: %v", err)
			}
			snapshot := sealTestActions(t, f)
			if _, err := actioncompare.ValidateSnapshot(&snapshot); err != nil || snapshot.Status != "complete" || snapshot.PlatformCaptureVersion != api.ScenarioPlatformCaptureVersion {
				t.Fatalf("invalid sealed platform evidence: %+v %v", snapshot, err)
			}
			targets := map[string]int{}
			for _, call := range snapshot.PlatformCalls {
				if call.Workload != "primary" {
					t.Fatalf("call workload = %+v", call)
				}
				targets[call.Kind+" "+call.Target+" "+call.Name]++
			}
			want := map[string]int{
				"workflow_run workload:primary invoice":                                   2,
				"workflow_run app:" + ordinary.Slug + " invoice":                          1,
				"workflow_event run:primary/workload:primary/invoice#2 approved":          1,
				"workflow_event run:" + state.CanonicalMemUUID(external.ID) + " approved": 1,
			}
			if len(targets) != len(want) {
				t.Fatalf("sealed platform calls = %v, want %v", targets, want)
			}
			for key, count := range want {
				if targets[key] != count {
					t.Fatalf("sealed platform calls = %v, want %v", targets, want)
				}
			}
			if _, _, err := start(app.Slug, "", `{}`); !errors.Is(err, state.ErrScenarioActionsSealed) {
				t.Fatalf("late start after seal: %v", err)
			}
		})
	}
}
