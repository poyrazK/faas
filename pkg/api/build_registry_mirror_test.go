package api_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261110210000 — slice 3j-3: vmmd and the guest derive the same
// mirror ports from the build's registries, and the guest accepts only that
// plan.
func TestPlanAndParseBuildRegistryMirrors(t *testing.T) {
	plan := api.PlanBuildRegistryMirrors([]string{"ghcr.io", "registry.example.com:5000", "ghcr.io", "Bad Host", "a.io"})
	want := []api.BuildRegistryMirror{
		{Registry: "a.io", GuestPort: 15000, VsockPort: 1100},
		{Registry: "ghcr.io", GuestPort: 15001, VsockPort: 1101},
		{Registry: "registry.example.com:5000", GuestPort: 15002, VsockPort: 1102},
	}
	if !reflect.DeepEqual(plan, want) {
		t.Fatalf("plan = %+v", plan)
	}
	raw, _ := json.Marshal(plan)
	got, err := api.ParseBuildRegistryMirrors(string(raw))
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("parse = %+v, %v", got, err)
	}

	many := make([]string, 0, 12)
	for i := 0; i < 12; i++ {
		many = append(many, strings.Repeat("r", i+1)+".io")
	}
	if n := len(api.PlanBuildRegistryMirrors(many)); n != api.BuildRegistryMirrorMax {
		t.Fatalf("plan of 12 = %d mirrors", n)
	}

	for name, raw := range map[string]string{
		"not json":       `{`,
		"toml injection": `[{"registry":"a.io\"]\n[x]","guest_port":15000,"vsock_port":1100}]`,
		"duplicate":      `[{"registry":"a.io","guest_port":15000,"vsock_port":1100},{"registry":"a.io","guest_port":15001,"vsock_port":1101}]`,
		"wrong port":     `[{"registry":"a.io","guest_port":22,"vsock_port":1100}]`,
		"wrong vsock":    `[{"registry":"a.io","guest_port":15000,"vsock_port":1026}]`,
	} {
		if _, err := api.ParseBuildRegistryMirrors(raw); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}
