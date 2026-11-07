// adr: 20261101215228

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

// cpuShapeKeys are the runtime-config keys schedd applies through
// cpuShapeConfig (ADR-20261101215228).
var cpuShapeKeys = []string{
	runtimeconfig.KeyAppCPUBaselineMillicoresPerGB,
	runtimeconfig.KeyAppCPUCeilingFloorMillicores,
	runtimeconfig.KeyGuestVCPUMillicores,
	runtimeconfig.KeyGuestVCPUFromCPUApps,
}

// cpuShapeEngine is the part of the engine the CPU shape keys set.
type cpuShapeEngine interface {
	SetCPUShape(api.CPUShape)
	SetGuestVCPUFromCPUApps([]string) error
}

// cpuShapeConfig applies the CPU shape keys to the engine. Until a key's
// first applied value, the engine keeps the compiled behavior: no listed
// apps and the compiled shape. A value outside its bounds fails the apply,
// and the engine keeps the last value it applied.
type cpuShapeConfig struct {
	engine cpuShapeEngine

	mu    sync.Mutex
	shape api.CPUShape
}

// apply installs one key's value. handled is false for a key it does not own.
func (c *cpuShapeConfig) apply(key string, value json.RawMessage) (handled bool, err error) {
	if key == runtimeconfig.KeyGuestVCPUFromCPUApps {
		list, err := runtimeconfig.String(value)
		if err != nil {
			return true, fmt.Errorf("%s must be a string: %w", key, err)
		}
		return true, c.engine.SetGuestVCPUFromCPUApps(strings.Split(list, ","))
	}
	valid := map[string]func(int) bool{
		runtimeconfig.KeyAppCPUBaselineMillicoresPerGB: api.ValidAppCPUBaselineMillicoresPerGB,
		runtimeconfig.KeyAppCPUCeilingFloorMillicores:  api.ValidAppCPUCeilingFloorMillicores,
		runtimeconfig.KeyGuestVCPUMillicores:           api.ValidGuestVCPUMillicores,
	}[key]
	if valid == nil {
		return false, nil
	}
	v, err := runtimeconfig.Int(value)
	if err != nil {
		return true, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if !valid(v) {
		return true, fmt.Errorf("%s: %d is outside its bounds", key, v)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	switch key {
	case runtimeconfig.KeyAppCPUBaselineMillicoresPerGB:
		c.shape.BaselineMillicoresPerGB = v
	case runtimeconfig.KeyAppCPUCeilingFloorMillicores:
		c.shape.CeilingFloorMillicores = v
	case runtimeconfig.KeyGuestVCPUMillicores:
		c.shape.VCPUMillicores = v
	}
	c.engine.SetCPUShape(c.shape)
	return true, nil
}
