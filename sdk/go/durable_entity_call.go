// ADR-938: typed, pure guest transitions.
package faas

import (
	"encoding/json"
	"fmt"
	"time"
)

// DurableEntityCall holds decoded local values, not ownership authority.
// Application code must validate business fields and schema versions.
type DurableEntityCall[State, Payload any] struct {
	Entity           DurableEntityIdentity
	RequestID, Event string
	Version          uint64
	State            State
	Payload          Payload
	request          DurableEntityHandlerRequest
}

// DecodeDurableEntityCall initializes version zero with a JSON-cloned initial
// value. It never resets committed state when its type cannot be decoded.
func DecodeDurableEntityCall[State, Payload any](body []byte, initial State) (DurableEntityCall[State, Payload], error) {
	var out DurableEntityCall[State, Payload]
	request, err := DecodeDurableEntityHandlerRequest(body)
	if err != nil {
		return out, err
	}
	data := request.State.Data
	if request.State.Version == 0 {
		data, err = json.Marshal(initial)
		if err != nil {
			return out, fmt.Errorf("encode initial entity state: %w", err)
		}
	}
	return decodeDurableEntityTypedCall[State, Payload](request, data)
}

func decodeDurableEntityTypedCall[State, Payload any](request DurableEntityHandlerRequest, data json.RawMessage) (DurableEntityCall[State, Payload], error) {
	var out DurableEntityCall[State, Payload]
	if err := json.Unmarshal(data, &out.State); err != nil {
		return out, fmt.Errorf("decode entity state: %w", err)
	}
	if err := json.Unmarshal(request.Payload, &out.Payload); err != nil {
		return out, fmt.Errorf("decode entity payload: %w", err)
	}
	out.Entity, out.RequestID, out.Event, out.Version, out.request = request.Entity, request.RequestID, request.Event, request.State.Version, request
	if out.Event == "" {
		out.Event = "invoke"
	}
	return out, nil
}

type DurableEntityTransitionBuilder[State any] struct {
	request  DurableEntityHandlerRequest
	data     State
	result   any
	alarm    *time.Time
	outbox   []DurableEntityWebhookIntent
	err      error
	validate func(State) error
}

// Transition preserves the current alarm by default. The returned builder only
// computes response bytes; handlers must not perform external side effects.
func (c DurableEntityCall[State, Payload]) Transition(data State, result any) *DurableEntityTransitionBuilder[State] {
	var alarm *time.Time
	if c.request.State.AlarmAt != nil {
		at := *c.request.State.AlarmAt
		alarm = &at
	}
	return &DurableEntityTransitionBuilder[State]{request: c.request, data: data, result: result, alarm: alarm}
}

func (b *DurableEntityTransitionBuilder[State]) ScheduleAlarm(at time.Time) *DurableEntityTransitionBuilder[State] {
	at = at.UTC()
	b.alarm = &at
	return b
}
func (b *DurableEntityTransitionBuilder[State]) ClearAlarm() *DurableEntityTransitionBuilder[State] {
	b.alarm = nil
	return b
}

// Webhook appends a registered-hook intent, never sends it. A rejected intent
// poisons the builder so ignoring this error cannot publish partial work.
func (b *DurableEntityTransitionBuilder[State]) Webhook(webhookID, event string, payload any) error {
	if b.err != nil {
		return b.err
	}
	intent, err := b.request.WebhookIntent(webhookID, event, payload)
	if err != nil {
		b.err = err
		return err
	}
	b.outbox = append(b.outbox, intent)
	return nil
}

func (b *DurableEntityTransitionBuilder[State]) Encode() ([]byte, error) {
	if b.err != nil {
		return nil, b.err
	}
	if b.validate != nil {
		if err := b.validate(b.data); err != nil {
			return nil, err
		}
	}
	data, err := json.Marshal(b.data)
	if err != nil {
		return nil, fmt.Errorf("encode entity state: %w", err)
	}
	result, err := json.Marshal(b.result)
	if err != nil {
		return nil, fmt.Errorf("encode entity result: %w", err)
	}
	return b.request.EncodeTransition(DurableEntityTransition{Data: data, Result: result, AlarmAt: b.alarm, Outbox: b.outbox})
}
