// adr: 20261101215228

package main

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runtimeconfig"
)

type fakeCPUShapeEngine struct {
	shape api.CPUShape
	apps  []string
}

func (f *fakeCPUShapeEngine) SetCPUShape(s api.CPUShape) { f.shape = s }

func (f *fakeCPUShapeEngine) SetGuestVCPUFromCPUApps(apps []string) error {
	for _, a := range apps {
		if a == "bad" {
			return errors.New("bad entry")
		}
	}
	f.apps = apps
	return nil
}

func TestCPUShapeConfigAppliesTheKeys(t *testing.T) {
	engine := &fakeCPUShapeEngine{}
	c := &cpuShapeConfig{engine: engine}
	steps := []struct {
		key, value string
		handled    bool
		wantErr    bool
	}{
		{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, "500", true, false},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "500", true, false},
		{runtimeconfig.KeyGuestVCPUMillicores, "2000", true, false},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `"*,0b6f8c1e-2f6a-4c55-9d38-3c1f0c5e7a11"`, true, false},
		// Out of bounds or the wrong shape: refused, last value kept.
		{runtimeconfig.KeyAppCPUBaselineMillicoresPerGB, "100", true, true},
		{runtimeconfig.KeyAppCPUCeilingFloorMillicores, "750", true, true},
		{runtimeconfig.KeyGuestVCPUMillicores, `"1000"`, true, true},
		{runtimeconfig.KeyGuestVCPUFromCPUApps, `"bad"`, true, true},
		{runtimeconfig.KeyDataPlacement, "true", false, false},
	}
	for _, step := range steps {
		handled, err := c.apply(step.key, json.RawMessage(step.value))
		if handled != step.handled || (err != nil) != step.wantErr {
			t.Errorf("apply(%s, %s) = %v, %v; want handled %v, error %v", step.key, step.value, handled, err, step.handled, step.wantErr)
		}
	}
	want := api.CPUShape{BaselineMillicoresPerGB: 500, CeilingFloorMillicores: 500, VCPUMillicores: 2000}
	if engine.shape != want {
		t.Errorf("shape = %+v, want %+v", engine.shape, want)
	}
	if len(engine.apps) != 2 || engine.apps[0] != "*" {
		t.Errorf("apps = %v, want the two listed entries", engine.apps)
	}
}
