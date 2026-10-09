// adr: 844
package durableentity

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGuestOutboxSharedWireContractCommitsAtomically(t *testing.T) {
	requestBody, err := os.ReadFile("../../sdk/go/testdata/durable-entity-handler-request.json")
	if err != nil {
		t.Fatal(err)
	}
	var envelope HandlerRequest
	if err := json.Unmarshal(requestBody, &envelope); err != nil || envelope.ProtocolVersion != api.DurableEntityOutboxProtocolVersion || !reflect.DeepEqual(envelope.Limits, OutboxHandlerLimits()) {
		t.Fatal("SDK fixture does not match the platform contract", err)
	}
	body, err := os.ReadFile("../../sdk/go/testdata/durable-entity-handler-transition.json")
	if err != nil {
		t.Fatal(err)
	}
	transition, err := DecodeTransitionForProtocol(body, envelope.ProtocolVersion)
	if err != nil || len(transition.Outbox) != 1 || transition.Outbox[0].EventType != "reservation.confirmed" {
		t.Fatal(transition, err)
	}
	f := newFixture(t)
	result, err := f.manager.Execute(t.Context(), f.claim, Request{ID: "reserve-123", Payload: envelope.Payload}, func(context.Context, View) (Transition, error) {
		return transition, nil
	})
	if err != nil || result.Version != 1 {
		t.Fatal(result, err)
	}
	pendingOutbox(t, f.manager, f.id, 1, 1)
}

func TestGuestOutboxVersionAndShapeFailClosed(t *testing.T) {
	valid := `{"data":{},"result":null,"outbox":[{"webhook_id":"6dd283da-3c14-40de-9d47-bb71fb35be9a","event_type":"reservation.confirmed","payload":null}]}`
	for _, body := range []string{valid, `{"data":{},"result":null,"outbox":[]}`, `{"data":{},"result":null,"outbox":null}`} {
		if _, err := DecodeTransitionForProtocol([]byte(body), api.DurableEntityProtocolVersion); !errors.Is(err, ErrInvalid) {
			t.Fatal("v1 accepted outgoing work", err)
		}
		if _, err := DecodeTransitionForProtocol([]byte(body), api.DurableEntityOutboxProtocolVersion); err != nil {
			t.Fatal("v2 rejected a valid transition", err)
		}
	}
	for _, body := range []string{`{}`, valid + `{}`, strings.Replace(valid, `"payload":null`, `"payload":null,"url":"https://arbitrary.example.test"`, 1), strings.Replace(valid, `"event_type":"reservation.confirmed"`, `"event_type":" "`, 1), strings.Replace(valid, `6dd283da-3c14-40de-9d47-bb71fb35be9a`, `https://receiver.example.test`, 1), strings.Replace(valid, `"outbox":[`, `"outbox":{`, 1)} {
		if _, err := DecodeTransitionForProtocol([]byte(body), api.DurableEntityOutboxProtocolVersion); !errors.Is(err, ErrInvalid) {
			t.Fatal("v2 accepted invalid work", err)
		}
	}
	if _, err := DecodeTransitionForProtocol([]byte(`{"data":{},"result":null}`), 99); !errors.Is(err, ErrInvalid) {
		t.Fatal("unknown version was accepted", err)
	}
}

func TestGuestOutboxDecoderRejectsEncodedBounds(t *testing.T) {
	for _, kind := range []string{"count", "payload", "escaped", "batch", "transition"} {
		t.Run(kind, func(t *testing.T) {
			intents := []OutboxIntent{outboxIntent()}
			switch kind {
			case "count":
				for len(intents) <= api.MaxDurableEntityOutboxPerTransition {
					intents = append(intents, outboxIntent())
				}
			case "payload":
				intents[0].Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes) + `"`)
			case "escaped":
				intents[0].Payload = json.RawMessage(`"` + strings.Repeat("<", api.MaxDurableEntityOutboxPayloadBytes/2) + `"`)
			case "batch":
				intents[0].Payload = json.RawMessage(`"` + strings.Repeat("x", api.MaxDurableEntityOutboxPayloadBytes-2) + `"`)
				for len(intents) < api.MaxDurableEntityOutboxPerTransition {
					intents = append(intents, intents[0])
				}
			}
			body, err := json.Marshal(map[string]any{"data": nil, "result": nil, "outbox": intents})
			if err != nil {
				t.Fatal(err)
			}
			if kind == "transition" {
				body = append(body, []byte(strings.Repeat(" ", api.MaxDurableEntitySnapshotBytes))...)
			}
			if _, err := DecodeTransitionForProtocol(body, api.DurableEntityOutboxProtocolVersion); !errors.Is(err, ErrLimit) {
				t.Fatal("oversized batch accepted", err)
			}
		})
	}
}
