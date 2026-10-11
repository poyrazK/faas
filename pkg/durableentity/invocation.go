package durableentity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// HandlerRequest is the versioned guest protocol. It carries business state
// and verified scope, never a claim token or object-storage credentials.
type HandlerRequest struct {
	ProtocolVersion int             `json:"protocol_version"`
	Event           string          `json:"event,omitempty"`
	Entity          ID              `json:"entity"`
	RequestID       string          `json:"request_id"`
	Payload         json.RawMessage `json:"payload"`
	State           View            `json:"state"`
	DeploymentID    string          `json:"deployment_id"`
	Limits          *HandlerLimits  `json:"limits,omitempty"` // Protocol v2 only.
}

// HandlerLimits advertise central bounds, not pending queue contents or capacity.
// The engine still checks the complete queue and snapshot before any upload.
type HandlerLimits struct {
	TransitionBytes    int `json:"transition_bytes"`
	IdentityBytes      int `json:"identity_bytes"`
	OutboxMessages     int `json:"outbox_messages"`
	OutboxPayloadBytes int `json:"outbox_payload_bytes"`
	OutboxBytes        int `json:"outbox_bytes"`
}

func OutboxHandlerLimits() *HandlerLimits {
	return &HandlerLimits{
		TransitionBytes: api.MaxDurableEntitySnapshotBytes, IdentityBytes: api.MaxDurableEntityIdentityBytes,
		OutboxMessages: api.MaxDurableEntityOutboxPerTransition, OutboxPayloadBytes: api.MaxDurableEntityOutboxPayloadBytes,
		OutboxBytes: api.MaxDurableEntityOutboxBytes,
	}
}

// Invoke owns an entity for one synchronous call. Local calls wait without
// allocating permanent workers; another process receives ErrBusy and retries.
// The handler must finish inside the configured lease. A crash is recovered
// by takeover after expiry. Leases fence commits, not external side effects.
func (m *Manager) Invoke(ctx context.Context, id ID, owner string, request Request, handler func(context.Context, View) (Transition, error)) (Result, error) {
	if IsAlarmRequestID(request.ID) || handler == nil {
		return Result{}, ErrInvalid
	}
	return m.invoke(ctx, id, owner, request, func(ctx context.Context, _ Claim, view View) (Transition, error) {
		return handler(ctx, view)
	})
}

func (m *Manager) invoke(ctx context.Context, id ID, owner string, request Request, handler func(context.Context, Claim, View) (Transition, error)) (Result, error) {
	if !id.valid() || !validIdentity(owner) || !validIdentity(request.ID) || !json.Valid(request.Payload) || handler == nil {
		return Result{}, ErrInvalid
	}
	unlock, err := m.lock(ctx, id.prefix()+"invocations/")
	if err != nil {
		return Result{}, err
	}
	defer unlock()
	claim, err := m.Acquire(ctx, id, owner)
	if err != nil {
		return Result{}, err
	}
	result, err := m.Execute(ctx, claim, request, func(ctx context.Context, view View) (Transition, error) {
		return handler(ctx, claim, view)
	})
	// Cleanup cannot undo an acknowledged commit. Failure only delays the next
	// process until expiry. Use a bounded context even if the caller went away.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
	defer cancel()
	_ = m.Release(cleanup, claim)
	return result, err
}

func DecodeTransition(body []byte) (Transition, error) {
	return DecodeTransitionForProtocol(body, api.DurableEntityProtocolVersion)
}

// DecodeTransitionForProtocol uses the version selected by the platform. A guest
// cannot upgrade itself; v1 rejects even an empty or null outbox field.
func DecodeTransitionForProtocol(body []byte, protocol int) (Transition, error) {
	if protocol != api.DurableEntityProtocolVersion && protocol != api.DurableEntityOutboxProtocolVersion {
		return Transition{}, ErrInvalid
	}
	if len(body) > api.MaxDurableEntitySnapshotBytes {
		return Transition{}, exceeded("transition_bytes", api.MaxDurableEntitySnapshotBytes, len(body))
	}
	var transition Transition
	var outgoing struct {
		Data    json.RawMessage `json:"data"`
		Result  json.RawMessage `json:"result"`
		AlarmAt *time.Time      `json:"alarm_at,omitempty"`
		Outbox  []OutboxIntent  `json:"outbox,omitempty"`
	}
	var target any = &transition
	if protocol == api.DurableEntityOutboxProtocolVersion {
		target = &outgoing
	}
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return Transition{}, fmt.Errorf("decode entity transition: %w", ErrInvalid)
	}
	if protocol == api.DurableEntityOutboxProtocolVersion {
		transition = Transition{Data: outgoing.Data, Result: outgoing.Result, AlarmAt: outgoing.AlarmAt, Outbox: outgoing.Outbox}
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !json.Valid(transition.Data) || !json.Valid(transition.Result) || !validAlarm(transition.AlarmAt) {
		return Transition{}, ErrInvalid
	}
	if err := ValidateOutboxIntents(transition.Outbox); err != nil {
		return Transition{}, err
	}
	return transition, nil
}
