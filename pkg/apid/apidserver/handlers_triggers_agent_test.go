// adr: 20261027090010
package apidserver_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/agents"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
	"github.com/onebox-faas/faas/pkg/state"
	agentstore "github.com/onebox-faas/faas/pkg/state/agents"
)

// triggerAgentLookup knows a set of agent names; the trigger API only looks
// agents up.
type triggerAgentLookup struct {
	agents.Store
	names map[string]bool
}

func (l triggerAgentLookup) Agent(_ context.Context, _, name string) (agentstore.Agent, agentstore.Version, error) {
	if !l.names[name] {
		return agentstore.Agent{}, agentstore.Version{}, agentstore.ErrNotFound
	}
	return agentstore.Agent{Name: name, LatestVersion: 1}, agentstore.Version{Version: 1}, nil
}

// A broker trigger may target an agent while the agents capability is
// released to its account; otherwise the field does not exist.
func TestTriggerAgentTargetFollowsAgentsCapability(t *testing.T) {
	e := apidserver.Setup(t, api.PlanPro)
	installAgentGate(t, *e.S(), "event-triggers")
	app, err := (*e.Store()).CreateApp(t.Context(), state.App{AccountID: (*e.Acct()).ID, Slug: "agent-target-app"})
	if err != nil {
		t.Fatal(err)
	}
	kafka := json.RawMessage(`{"brokers":["b:9092"],"topic":"orders","group":"g"}`)
	agent, missing, batch, record := "order-review", "nobody", api.TriggerDeliveryBatch, api.TriggerDeliveryRecord
	create := func(slug string, kind api.TriggerKind, config json.RawMessage, name *string, delivery *string) (int, string, api.Trigger) {
		rec := e.Do(t, http.MethodPost, "/v1/triggers", api.CreateTriggerRequest{AppID: app.ID, Kind: kind, Slug: slug,
			Config: config, Agent: name, Delivery: delivery}, nil)
		var created api.Trigger
		_ = json.Unmarshal(rec.Body.Bytes(), &created)
		return rec.Code, rec.Body.String(), created
	}

	if code, body, _ := create("hidden", api.TriggerKindKafka, kafka, &agent, nil); code != http.StatusBadRequest || !strings.Contains(body, `unknown field \"agent\"`) {
		t.Fatalf("agents off = %d %s", code, body)
	}
	releaseAgentsTo(t, *e.S(), (*e.Acct()).ID)
	(*e.S()).ConfigureAgentsForTest()
	(*e.S()).SetAgentStoreForTest(triggerAgentLookup{names: map[string]bool{agent: true}})
	(*e.S()).WithTriggerBatchDeliveryEnabled(true)

	for _, tc := range []struct {
		name     string
		kind     api.TriggerKind
		config   json.RawMessage
		agent    *string
		delivery *string
	}{
		{"unknown agent", api.TriggerKindKafka, kafka, &missing, nil},
		{"batch delivery", api.TriggerKindKafka, kafka, &agent, &batch},
		{"queue kind", api.TriggerKindQueue, json.RawMessage(`{"mode":"queue"}`), &agent, nil},
	} {
		if code, body, _ := create(strings.ReplaceAll(tc.name, " ", "-"), tc.kind, tc.config, tc.agent, tc.delivery); code != http.StatusUnprocessableEntity {
			t.Fatalf("%s = %d %s, want 422", tc.name, code, body)
		}
	}

	code, body, created := create("orders", api.TriggerKindKafka, kafka, &agent, nil)
	if code != http.StatusCreated || created.Agent != agent || !created.Enabled || created.Delivery != record {
		t.Fatalf("create = %d %s", code, body)
	}
	rec := e.Do(t, http.MethodGet, "/v1/triggers/"+created.ID, nil, nil)
	var read api.Trigger
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &read) != nil || read.Agent != agent {
		t.Fatalf("read = %d %s", rec.Code, rec.Body)
	}
	if rec := e.Do(t, http.MethodPatch, "/v1/triggers/"+created.ID, api.UpdateTriggerRequest{Delivery: &batch}, nil); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("batch delivery on an agent trigger = %d %s, want 422", rec.Code, rec.Body)
	}
	_, body, plain := create("plain", api.TriggerKindKafka, kafka, nil, nil)
	if plain.Agent != "" || strings.Contains(body, `"agent"`) {
		t.Fatalf("a plain trigger shows an agent: %s", body)
	}
}
