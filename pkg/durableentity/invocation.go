package durableentity

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"

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
}

// Invoke owns an entity for one synchronous call. Local calls wait without
// allocating permanent workers; another process receives ErrBusy and retries.
// The handler must finish inside the configured lease. A crash is recovered
// by takeover after expiry. Leases fence commits, not external side effects.
func (m *Manager) Invoke(ctx context.Context, id ID, owner string, request Request, handler func(context.Context, View) (Transition, error)) (Result, error) {
	if IsAlarmRequestID(request.ID) {
		return Result{}, ErrInvalid
	}
	return m.invoke(ctx, id, owner, request, handler)
}

func (m *Manager) invoke(ctx context.Context, id ID, owner string, request Request, handler func(context.Context, View) (Transition, error)) (Result, error) {
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
	result, err := m.Execute(ctx, claim, request, handler)
	// Cleanup cannot undo an acknowledged commit. Failure only delays the next
	// process until expiry. Use a bounded context even if the caller went away.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.DurableEntityReleaseTimeout)
	defer cancel()
	_ = m.Release(cleanup, claim)
	return result, err
}

func DecodeTransition(body []byte) (Transition, error) {
	if len(body) > api.MaxDurableEntitySnapshotBytes {
		return Transition{}, exceeded("transition_bytes", api.MaxDurableEntitySnapshotBytes, len(body))
	}
	var transition Transition
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&transition); err != nil {
		return Transition{}, fmt.Errorf("decode entity transition: %w", ErrInvalid)
	}
	var extra any
	if dec.Decode(&extra) != io.EOF || !json.Valid(transition.Data) || !json.Valid(transition.Result) || !validAlarm(transition.AlarmAt) {
		return Transition{}, ErrInvalid
	}
	return transition, nil
}
