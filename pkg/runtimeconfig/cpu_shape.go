// adr: 20261101215228

package runtimeconfig

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// CPUShapeKeys are the keys that decide an app's CPU ceiling and guest vCPU
// count (ADR-20261101215228, ADR-20261109172421). schedd applies them; its
// snapshot refresh replays their history to tell the vCPU count a snapshot
// was captured with (ADR-20261106214916).
var CPUShapeKeys = []string{
	KeyAppCPUBaselineMillicoresPerGB,
	KeyAppCPUCeilingFloorMillicores,
	KeyGuestVCPUMillicores,
	KeyGuestVCPUFromCPUApps,
	KeyCPUSizeApps,
}

// CPUShapeSettings is what the CPU shape keys set: the shape, and the
// entries of the guest_vcpu_from_cpu_apps and cpu_size_apps lists (app IDs
// or "*"; schedd checks them). The zero value is the compiled behavior: no
// listed apps and the compiled shape.
type CPUShapeSettings struct {
	Shape    api.CPUShape
	Apps     []string
	SizeApps []string
}

// Apply sets one key's value. handled is false for a key it does not own. A
// value of the wrong JSON type or outside its bounds is an error and leaves s
// unchanged.
func (s *CPUShapeSettings) Apply(key string, value json.RawMessage) (handled bool, err error) {
	if key == KeyGuestVCPUFromCPUApps || key == KeyCPUSizeApps {
		list, err := String(value)
		if err != nil {
			return true, fmt.Errorf("%s must be a string: %w", key, err)
		}
		if key == KeyCPUSizeApps {
			s.SizeApps = strings.Split(list, ",")
		} else {
			s.Apps = strings.Split(list, ",")
		}
		return true, nil
	}
	valid := map[string]func(int) bool{
		KeyAppCPUBaselineMillicoresPerGB: api.ValidAppCPUBaselineMillicoresPerGB,
		KeyAppCPUCeilingFloorMillicores:  api.ValidAppCPUCeilingFloorMillicores,
		KeyGuestVCPUMillicores:           api.ValidGuestVCPUMillicores,
	}[key]
	if valid == nil {
		return false, nil
	}
	v, err := Int(value)
	if err != nil {
		return true, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	if !valid(v) {
		return true, fmt.Errorf("%s: %d is outside its bounds", key, v)
	}
	s.set(key, v)
	return true, nil
}

// Reset returns one key to its compiled behavior, as when the key has no
// value. It is false for a key it does not own.
func (s *CPUShapeSettings) Reset(key string) bool {
	switch key {
	case KeyGuestVCPUFromCPUApps:
		s.Apps = nil
	case KeyCPUSizeApps:
		s.SizeApps = nil
	case KeyAppCPUBaselineMillicoresPerGB, KeyAppCPUCeilingFloorMillicores, KeyGuestVCPUMillicores:
		s.set(key, 0)
	default:
		return false
	}
	return true
}

func (s *CPUShapeSettings) set(key string, v int) {
	switch key {
	case KeyAppCPUBaselineMillicoresPerGB:
		s.Shape.BaselineMillicoresPerGB = v
	case KeyAppCPUCeilingFloorMillicores:
		s.Shape.CeilingFloorMillicores = v
	case KeyGuestVCPUMillicores:
		s.Shape.VCPUMillicores = v
	}
}
