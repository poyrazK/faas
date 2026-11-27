package state_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgAPISandboxScenarioLibraryPersistsAppScopedRevisions(t *testing.T) {
	store, _, ctx := pgStoreWithPool(t)
	account, err := store.CreateAccount(ctx, "sandbox-library@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "sandbox-library", RAMMB: 512, Type: state.AppTypeApp})
	if err != nil {
		t.Fatal(err)
	}
	id, requestID := uuid.NewString(), uuid.NewString()
	input := api.APISandboxScenarioInput{
		Seed: json.RawMessage(`{"id":"demo"}`),
		APISandboxScenarioGuide: api.APISandboxScenarioGuide{
			Name:     "Export",
			Requests: []api.APISandboxExampleRequest{{Title: "Read export", Method: "GET", Path: "/exports/demo", ExpectedStatus: 200}},
		},
	}
	origin := state.APISandboxScenarioOrigin{RequestID: requestID}
	saved, err := store.PutAPISandboxLibraryScenario(ctx, app.ID, id, input, origin)
	if err != nil || saved.ID != id || saved.SourceRequestID != requestID {
		t.Fatalf("save library scenario: %+v %v", saved, err)
	}
	if retry, err := store.PutAPISandboxLibraryScenario(ctx, app.ID, id, input, origin); err != nil || retry.Scenario.Revision != saved.Scenario.Revision {
		t.Fatalf("identical retry changed a library scenario: %+v %v", retry, err)
	}
	if _, err := store.GetAPISandboxLibraryScenarioByRevision(ctx, uuid.NewString(), saved.Scenario.Revision); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("another app could read the library revision: %v", err)
	}
	if _, err := store.PutAPISandboxLibraryScenario(ctx, app.ID, uuid.NewString(), input, state.APISandboxScenarioOrigin{}); !errors.Is(err, state.ErrAPISandboxScenarioNameTaken) {
		t.Fatalf("duplicate scenario name returned %v", err)
	}

	type result struct {
		scenario api.APISandboxLibraryScenario
		err      error
	}
	updates := make(chan result, 2)
	for _, name := range []string{"Check download", "Verify export response"} {
		go func() {
			edited := input
			edited.Name = name
			entry, err := store.PutAPISandboxLibraryScenario(ctx, app.ID, id, edited,
				state.APISandboxScenarioOrigin{ExpectedRevision: saved.Scenario.Revision})
			updates <- result{entry, err}
		}()
	}
	a, b := <-updates, <-updates
	if b.err == nil {
		a, b = b, a
	}
	if a.err != nil || !errors.Is(b.err, state.ErrConflict) || a.scenario.SourceRequestID != requestID {
		t.Fatalf("expected one revision change with preserved provenance: %+v %+v", a, b)
	}
	choices, err := store.ListAPISandboxLibraryScenarios(ctx, app.ID)
	if err != nil || len(choices) != 1 || choices[0].ID != id || choices[0].Revision != a.scenario.Scenario.Revision {
		t.Fatalf("library list lost the current revision: %+v %v", choices, err)
	}
	if err := store.DeleteAPISandboxLibraryScenario(ctx, app.ID, id, saved.Scenario.Revision); !errors.Is(err, state.ErrConflict) {
		t.Fatalf("stale delete returned %v", err)
	}
	if err := store.DeleteAPISandboxLibraryScenario(ctx, app.ID, id, a.scenario.Scenario.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetAPISandboxLibraryScenario(ctx, app.ID, id); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("removed scenario remained available: %v", err)
	}
}
