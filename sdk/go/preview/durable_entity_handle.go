// ADR-938: scope-bound typed entity clients.
package faas

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
)

// DurableEntityHandle binds selectors, never a VM, owner or bucket location.
// Copying a handle retains its scope. Each invocation needs an explicit replay ID.
type DurableEntityHandle[Payload, Result any] struct {
	client *Client
	slug   string
	scope  DurableEntityInspectRequest
}

type DurableEntityResult[Result any] struct {
	Value    Result
	Version  uint64
	Replayed bool
}

func NewDurableEntityHandle[Payload, Result any](client *Client, slug string, scope DurableEntityInspectRequest) (*DurableEntityHandle[Payload, Result], error) {
	if client == nil || client.Client == nil || slug == "" || !durableEntityIdentity(scope.Namespace, 0) || !durableEntityIdentity(scope.Key, 0) {
		return nil, errors.New("faas: entity handle requires a client, app, namespace and key")
	}
	return &DurableEntityHandle[Payload, Result]{client: client, slug: slug, scope: scope}, nil
}

// Invoke preserves acknowledgement metadata even if typed result decoding fails.
// After any uncertain/decode error, reuse the request ID and exact payload.
func (h *DurableEntityHandle[Payload, Result]) Invoke(ctx context.Context, requestID string, payload Payload) (DurableEntityResult[Result], error) {
	var out DurableEntityResult[Result]
	if requestID == "" {
		return out, errors.New("faas: entity request_id is required")
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return out, fmt.Errorf("encode entity payload: %w", err)
	}
	raw, err := h.client.InvokeDurableEntity(ctx, h.slug, DurableEntityInvokeRequest{Namespace: h.scope.Namespace, Key: h.scope.Key, Environment: h.scope.Environment, PlatformTenantID: h.scope.PlatformTenantID, RequestID: requestID, Payload: body})
	if err != nil {
		return out, err
	}
	out.Version, out.Replayed = raw.Version, raw.Replayed
	if err := json.Unmarshal(raw.Value, &out.Value); err != nil {
		return out, fmt.Errorf("decode committed entity result: %w", err)
	}
	return out, nil
}

func (h *DurableEntityHandle[Payload, Result]) Inspect(ctx context.Context) (DurableEntityInspectResponse, error) {
	return h.client.InspectDurableEntity(ctx, h.slug, h.scope)
}

// Retry accepts a caller-observed fence; it never silently fetches a newer one.
func (h *DurableEntityHandle[Payload, Result]) Retry(ctx context.Context, request DurableEntityRetryRequest) (DurableEntityRetryResponse, error) {
	request.Namespace, request.Key = h.scope.Namespace, h.scope.Key
	request.Environment, request.PlatformTenantID = h.scope.Environment, h.scope.PlatformTenantID
	return h.client.RetryDurableEntity(ctx, h.slug, request)
}
