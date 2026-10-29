package faas_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/poyrazK/faas/sdk/go/preview"
)

func entityHandlerFixture(t *testing.T) preview.DurableEntityHandlerRequest {
	t.Helper()
	body, err := os.ReadFile("testdata/durable-entity-handler-request.json")
	if err != nil {
		t.Fatal(err)
	}
	request, err := preview.DecodeDurableEntityHandlerRequest(body)
	if err != nil {
		t.Fatal(err)
	}
	return request
}

func TestDurableEntityHandlerMatchesSharedWireContract(t *testing.T) {
	request := entityHandlerFixture(t)
	payload := map[string]any{"reservation_key": request.Entity.Key, "quantity": 2}
	intent, err := request.WebhookIntent("6dd283da-3c14-40de-9d47-bb71fb35be9a", "reservation.confirmed", payload)
	if err != nil {
		t.Fatal(err)
	}
	payload["quantity"] = 99
	next := json.RawMessage(`{"status":"reserved","quantity":2}`)
	body, err := request.EncodeTransition(preview.DurableEntityTransition{Data: next, Result: next, Outbox: []preview.DurableEntityWebhookIntent{intent}})
	if err != nil {
		t.Fatal(err)
	}
	expected, err := os.ReadFile("testdata/durable-entity-handler-transition.json")
	if err != nil {
		t.Fatal(err)
	}
	var got, want any
	if json.Unmarshal(body, &got) != nil || json.Unmarshal(expected, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("guest wire contract differs: %s", body)
	}
	// Constructed intents capture the original payload, without bucket or claim
	// authority in either direction. Omitting Outbox does not clear pending work.
	plain, err := request.EncodeTransition(preview.DurableEntityTransition{Data: next, Result: next})
	if err != nil || strings.Contains(string(plain), "outbox") || strings.Contains(string(body), "claim_token") {
		t.Fatal(string(plain), err)
	}
}

func TestDurableEntityHandlerPreservesFullUint64StateVersion(t *testing.T) {
	request := entityHandlerFixture(t)
	request.State.Version = ^uint64(0)
	body, err := json.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := preview.DecodeDurableEntityHandlerRequest(body)
	if err != nil || decoded.State.Version != request.State.Version {
		t.Fatal("Go handler rounded the state version", decoded.State.Version, err)
	}
}

func TestDurableEntityHandlerRequiresNegotiatedOutbox(t *testing.T) {
	for _, protocol := range []int{preview.DurableEntityHandlerProtocolVersion, preview.DurableEntityOutboxHandlerProtocolVersion} {
		request := entityHandlerFixture(t)
		request.ProtocolVersion = protocol
		if protocol == preview.DurableEntityHandlerProtocolVersion {
			request.Limits = nil
		}
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := preview.DecodeDurableEntityHandlerRequest(body)
		if err != nil || decoded.ProtocolVersion != protocol {
			t.Fatal(decoded, err)
		}
		_, err = decoded.WebhookIntent("6dd283da-3c14-40de-9d47-bb71fb35be9a", "reservation.confirmed", nil)
		if (err == nil) != (protocol == preview.DurableEntityOutboxHandlerProtocolVersion) {
			t.Fatal("outbox support did not follow negotiated version", err)
		}
		_, err = decoded.EncodeTransition(preview.DurableEntityTransition{Data: json.RawMessage(`null`), Result: json.RawMessage(`null`), Outbox: []preview.DurableEntityWebhookIntent{}})
		if (err == nil) != (protocol == preview.DurableEntityOutboxHandlerProtocolVersion) {
			t.Fatal("v1 accepted an outbox field", err)
		}
	}
}

func TestDurableEntityHandlerRejectsInvalidAndOversizedTransitions(t *testing.T) {
	for _, kind := range []string{"url", "nil-uuid", "event", "escaped-payload", "batch", "batch-bytes", "data", "alarm", "transition"} {
		t.Run(kind, func(t *testing.T) {
			request := entityHandlerFixture(t)
			intent, err := request.WebhookIntent("6dd283da-3c14-40de-9d47-bb71fb35be9a", "reservation.confirmed", nil)
			if err != nil {
				t.Fatal(err)
			}
			next := preview.DurableEntityTransition{Data: json.RawMessage(`null`), Result: json.RawMessage(`null`), Outbox: []preview.DurableEntityWebhookIntent{intent}}
			switch kind {
			case "url":
				next.Outbox[0].WebhookID = "https://receiver.example.test"
			case "nil-uuid":
				next.Outbox[0].WebhookID = "00000000-0000-0000-0000-000000000000"
			case "event":
				next.Outbox[0].EventType = "private\x00event"
			case "escaped-payload":
				next.Outbox[0].Payload = json.RawMessage(`"` + strings.Repeat("<", request.Limits.OutboxPayloadBytes/2) + `"`)
			case "batch":
				for len(next.Outbox) <= request.Limits.OutboxMessages {
					next.Outbox = append(next.Outbox, intent)
				}
			case "batch-bytes":
				request.Limits.OutboxBytes = request.Limits.OutboxPayloadBytes
				payload := json.RawMessage(`"` + strings.Repeat("x", request.Limits.OutboxPayloadBytes/2) + `"`)
				next.Outbox[0].Payload = payload
				next.Outbox = append(next.Outbox, next.Outbox[0])
			case "data":
				next.Data = json.RawMessage(`{`)
			case "alarm":
				zero := time.Time{}
				next.AlarmAt = &zero
			case "transition":
				request.Limits.TransitionBytes = request.Limits.OutboxBytes
				next.Data = json.RawMessage(`"` + strings.Repeat("x", request.Limits.TransitionBytes) + `"`)
			}
			if _, err := request.EncodeTransition(next); !errors.Is(err, preview.ErrDurableEntityHandler) {
				t.Fatal("invalid transition accepted", err)
			}
		})
	}
}

func TestDurableEntityHandlerRejectsUnsupportedEnvelopes(t *testing.T) {
	for _, kind := range []string{"version", "limits", "limit-value", "scope", "payload", "state", "trailing", "authority", "bytes"} {
		t.Run(kind, func(t *testing.T) {
			request := entityHandlerFixture(t)
			switch kind {
			case "version":
				request.ProtocolVersion = 99
			case "limits":
				request.Limits = nil
			case "limit-value":
				request.Limits.OutboxMessages = 0
			case "scope":
				request.Entity.Key = "\x00"
			case "payload":
				request.Payload = nil
			case "state":
				request.State.Data = nil
			}
			body, err := json.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			// Null is legitimate JSON state/payload, including the initial state.
			if kind == "payload" || kind == "state" {
				body = []byte(strings.Replace(string(body), `"`+kind+`":`, `"missing_`+kind+`":`, 1))
			}
			if kind == "trailing" {
				body = append(body, []byte(` {}`)...)
			}
			if kind == "authority" {
				body = []byte(strings.TrimSuffix(string(body), "}") + `,"claim_token":"private"}`)
			}
			if kind == "bytes" {
				body = append(body, []byte(strings.Repeat(" ", preview.DurableEntityHandlerMaxRequestBytes))...)
			}
			if _, err := preview.DecodeDurableEntityHandlerRequest(body); !errors.Is(err, preview.ErrDurableEntityHandler) {
				t.Fatal("invalid envelope accepted", err)
			}
		})
	}
}
