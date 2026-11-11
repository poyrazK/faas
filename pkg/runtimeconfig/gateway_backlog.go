package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"sync/atomic"

	"github.com/onebox-faas/faas/pkg/api"
)

// KeyGatewayAwaitingResponseMax is how many app requests one
// gatewayd-internal holds before their response head is written; past it the
// gateway answers 503 gateway_overloaded (pkg/gateway/backlog). Unset keeps
// api.GatewayAwaitingResponseMax.
const KeyGatewayAwaitingResponseMax = "gateway_awaiting_response_max"

// ApplyGatewayAwaitingResponseMax stores the key's value in max. handled is
// false for any other key.
func ApplyGatewayAwaitingResponseMax(max *atomic.Int64, key string, value json.RawMessage) (handled bool, err error) {
	if key != KeyGatewayAwaitingResponseMax {
		return false, nil
	}
	v, err := Int(value)
	if err != nil {
		return true, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if !api.ValidGatewayAwaitingResponseMax(v) {
		return true, fmt.Errorf("%s: %d is outside %d to %d", key, v, api.GatewayAwaitingResponseMaxMin, api.GatewayAwaitingResponseMaxMax)
	}
	max.Store(int64(v))
	return true, nil
}
