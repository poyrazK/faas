// ADR-939.
package faas_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	faas "github.com/poyrazK/faas/sdk/go"
)

type schemaCounter struct {
	Count int `json:"count"`
}

func counterSchema() faas.DurableEntityStateSchema[schemaCounter] {
	return faas.DurableEntityStateSchema[schemaCounter]{Version: 2, Initial: func() schemaCounter { return schemaCounter{} }, Validate: func(s schemaCounter) error {
		if s.Count < 0 {
			return errors.New("negative count")
		}
		return nil
	}, Migrations: map[uint32]func(json.RawMessage) (json.RawMessage, error){1: func(body json.RawMessage) (json.RawMessage, error) {
		var old struct {
			Total *int `json:"total"`
		}
		if err := json.Unmarshal(body, &old); err != nil {
			return nil, err
		}
		if old.Total == nil {
			return nil, errors.New("missing total")
		}
		return json.Marshal(schemaCounter{Count: *old.Total})
	}}}
}

func TestSchemaUpgradeStaysLocalUntilVersionedTransition(t *testing.T) {
	request := entityHandlerFixture(t)
	request.State.Version = 7
	request.State.Data = json.RawMessage(`{"schema_version":1,"data":{"total":4}}`)
	at := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	request.State.AlarmAt = &at
	body, _ := json.Marshal(request)
	call, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, counterSchema())
	if err != nil || call.State.Count != 4 || call.Version != 7 || call.StoredSchemaVersion != 1 || !call.Migrated {
		t.Fatal(call, err)
	}
	if string(request.State.Data) != `{"schema_version":1,"data":{"total":4}}` {
		t.Fatal("decode changed stored source")
	}
	transition := call.Transition(schemaCounter{Count: 5}, schemaCounter{Count: 5})
	if err := transition.Webhook("6dd283da-3c14-40de-9d47-bb71fb35be9a", "counter.updated", map[string]int{"count": 5}); err != nil {
		t.Fatal(err)
	}
	encoded, err := transition.Encode()
	if err != nil {
		t.Fatal(err)
	}
	var result faas.DurableEntityTransition
	if json.Unmarshal(encoded, &result) != nil || result.AlarmAt == nil || !result.AlarmAt.Equal(at) || len(result.Outbox) != 1 || string(result.Data) != `{"schema_version":2,"data":{"count":5}}` {
		t.Fatal(string(encoded))
	}
	request.State.Data = result.Data
	request.State.Version++
	body, _ = json.Marshal(request)
	schema := counterSchema()
	schema.Migrations = nil
	schema.Initial = func() schemaCounter { t.Fatal("initialized committed state"); return schemaCounter{} }
	replay, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, schema)
	if err != nil || replay.Migrated || replay.State.Count != 5 {
		t.Fatal(replay, err)
	}
	if _, err := replay.Transition(schemaCounter{Count: -1}, nil).Encode(); err == nil {
		t.Fatal("invalid next schema encoded")
	}
}

func TestSchemaRejectsRollbackMalformedAndIncompleteChain(t *testing.T) {
	for _, kind := range []string{"future", "missing-step", "malformed", "zero", "unversioned", "callback-failure", "oversized", "invalid-result"} {
		t.Run(kind, func(t *testing.T) {
			request := entityHandlerFixture(t)
			request.State.Version = 1
			request.State.Data = json.RawMessage(`{"schema_version":1,"data":{"total":4}}`)
			schema := counterSchema()
			switch kind {
			case "future":
				request.State.Data = json.RawMessage(`{"schema_version":3,"data":{"count":4}}`)
			case "missing-step":
				schema.Version = 3
			case "malformed":
				request.State.Data = json.RawMessage(`{"schema_version":1}`)
			case "zero":
				request.State.Data = json.RawMessage(`{"schema_version":0,"data":{}}`)
			case "unversioned":
				request.State.Data = json.RawMessage(`{"total":4}`)
			case "callback-failure":
				schema.Migrations[1] = func(json.RawMessage) (json.RawMessage, error) { return nil, errors.New("failed upgrade") }
			case "oversized":
				schema.Migrations[1] = func(json.RawMessage) (json.RawMessage, error) {
					return json.Marshal(strings.Repeat("x", faas.DurableEntityHandlerMaxTransitionBytes))
				}
			case "invalid-result":
				schema.Migrations[1] = func(json.RawMessage) (json.RawMessage, error) { return json.RawMessage(`{"count":-1}`), nil }
			}
			body, _ := json.Marshal(request)
			if _, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, schema); err == nil {
				t.Fatal("unsafe migration accepted", kind)
			}
		})
	}
}

func TestSchemaInitialStateAndExplicitLegacyAdoption(t *testing.T) {
	request := entityHandlerFixture(t)
	body, _ := json.Marshal(request)
	call, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, counterSchema())
	if err != nil || call.Migrated || call.State.Count != 0 || call.StoredSchemaVersion != 2 {
		t.Fatal(call, err)
	}
	request.State.Version = 9
	request.State.Data = json.RawMessage(`{"total":6}`)
	body, _ = json.Marshal(request)
	schema := counterSchema()
	legacy := uint32(1)
	schema.LegacyVersion = &legacy
	call, err = faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, schema)
	if err != nil || !call.Migrated || call.State.Count != 6 {
		t.Fatal(call, err)
	}
	schema.Version = 3
	calls := 0
	schema.Migrations[1] = func(raw json.RawMessage) (json.RawMessage, error) { calls++; return raw, nil }
	if _, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, schema); err == nil || calls != 0 {
		t.Fatal("incomplete chain ran a callback", calls, err)
	}
}

func TestSchemaCapturesCompleteMigrationChainBeforeCallbacks(t *testing.T) {
	request := entityHandlerFixture(t)
	request.State.Version = 7
	request.State.Data = json.RawMessage(`{"schema_version":1,"data":{"total":4}}`)
	body, _ := json.Marshal(request)
	schema := counterSchema()
	schema.Version = 3
	first := schema.Migrations[1]
	schema.Migrations[1] = func(raw json.RawMessage) (json.RawMessage, error) { delete(schema.Migrations, 2); return first(raw) }
	schema.Migrations[2] = func(raw json.RawMessage) (json.RawMessage, error) {
		var state schemaCounter
		if err := json.Unmarshal(raw, &state); err != nil {
			return nil, err
		}
		state.Count++
		return json.Marshal(state)
	}
	call, err := faas.DecodeDurableEntitySchemaCall[schemaCounter, map[string]any](body, schema)
	if err != nil || call.State.Count != 5 {
		t.Fatal(call, err)
	}
	encoded, err := call.Transition(call.State, nil).Encode()
	if err != nil {
		t.Fatal(err)
	}
	var result faas.DurableEntityTransition
	if json.Unmarshal(encoded, &result) != nil || string(result.Data) != `{"schema_version":3,"data":{"count":5}}` {
		t.Fatal(string(encoded))
	}
}
