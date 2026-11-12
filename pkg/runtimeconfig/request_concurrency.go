// adr: 20261101215228

package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	// KeyAppRequestConcurrencyDefault is the per-instance request limit of
	// an app that declares no per_instance_concurrency. 0 (the default) is
	// unset: the plan's concurrency_per_vm.
	KeyAppRequestConcurrencyDefault = "app_request_concurrency_default"
	// KeyAppRequestConcurrencyMax is the largest per_instance_concurrency an
	// app may declare, and vmmd's per-instance admission limit for an app
	// without one. 0 (the default) is unset: the plan's concurrency_per_vm.
	KeyAppRequestConcurrencyMax = "app_request_concurrency_max"
)

// RequestConcurrencyKeys are the keys RequestConcurrency applies.
var RequestConcurrencyKeys = []string{KeyAppRequestConcurrencyDefault, KeyAppRequestConcurrencyMax}

// ValidRequestConcurrency reports whether v is a valid value for either
// request key: 0 (unset) or 1 to api.AppRequestConcurrencyBound.
func ValidRequestConcurrency(v int) bool {
	return v >= 0 && v <= api.AppRequestConcurrencyBound
}

// RequestConcurrency applies the request keys to the process
// (api.SetRequestConcurrency), which the gateway, schedd and apid resolve
// per-instance limits through. The zero value is ready to use; until a key's
// first applied value the process keeps the plan bounds.
type RequestConcurrency struct {
	mu sync.Mutex
	rc api.RequestConcurrency
}

// Apply installs one key's value. handled is false for any other key.
func (r *RequestConcurrency) Apply(key string, value json.RawMessage) (handled bool, err error) {
	if key != KeyAppRequestConcurrencyDefault && key != KeyAppRequestConcurrencyMax {
		return false, nil
	}
	v, err := Int(value)
	if err != nil {
		return true, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if !ValidRequestConcurrency(v) {
		return true, fmt.Errorf("%s: %d is outside 0 to %d", key, v, api.AppRequestConcurrencyBound)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if key == KeyAppRequestConcurrencyDefault {
		r.rc.Default = v
	} else {
		r.rc.Max = v
	}
	api.SetRequestConcurrency(r.rc)
	return true, nil
}
