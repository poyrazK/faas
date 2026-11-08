// adr: 20261101215228

package main

import (
	"encoding/json"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// cpuShapeEngine is the part of the engine the CPU shape keys set.
type cpuShapeEngine interface {
	SetCPUShape(api.CPUShape)
	SetGuestVCPUFromCPUApps([]string) error
}

// cpuShapeConfig applies the CPU shape keys (runtimeconfig.CPUShapeKeys) to
// the engine. Until a key's first applied value, the engine keeps the
// compiled behavior: no listed apps and the compiled shape. A value outside
// its bounds fails the apply, and the engine keeps the last value it applied.
type cpuShapeConfig struct {
	engine cpuShapeEngine

	mu       sync.Mutex
	settings runtimeconfig.CPUShapeSettings
}

// apply installs one key's value. handled is false for a key it does not own.
func (c *cpuShapeConfig) apply(key string, value json.RawMessage) (handled bool, err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := c.settings
	if handled, err := next.Apply(key, value); !handled || err != nil {
		return handled, err
	}
	if key == runtimeconfig.KeyGuestVCPUFromCPUApps {
		if err := c.engine.SetGuestVCPUFromCPUApps(next.Apps); err != nil {
			return true, err
		}
	} else {
		c.engine.SetCPUShape(next.Shape)
	}
	c.settings = next
	return true, nil
}
