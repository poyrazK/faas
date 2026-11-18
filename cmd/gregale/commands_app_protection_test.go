package main_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 20261116120000
func TestRenderAppProtection(t *testing.T) {
	item := func(name, state, reason string) api.AppProtectionItemStatus {
		return api.AppProtectionItemStatus{Item: name, Name: "baseline " + name, State: state, Reason: reason}
	}
	for _, tc := range []struct {
		name string
		resp api.AppProtectionResponse
		want []string
	}{
		{"none", api.AppProtectionResponse{State: api.AppProtectionStateNone},
			[]string{"Production: not protected (gregale app protection on demo)"}},
		{"off", api.AppProtectionResponse{State: api.AppProtectionStateOff},
			[]string{"not protected (gregale app protection on demo)"}},
		{"protected", api.AppProtectionResponse{State: api.AppProtectionStateOn, Protected: true,
			Items: []api.AppProtectionItemStatus{item("health_check", api.AppProtectionItemActive, "")}},
			[]string{"Production: protected ✓", "health check", "active", "baseline health_check"}},
		{"pending", api.AppProtectionResponse{State: api.AppProtectionStateOn,
			Items: []api.AppProtectionItemStatus{item("latency_slo", api.AppProtectionItemPending, "")}},
			[]string{"being set up (waits for a live production deploy)"}},
		{"partly", api.AppProtectionResponse{State: api.AppProtectionStateOn, Items: []api.AppProtectionItemStatus{
			item("availability_gate", api.AppProtectionItemSkipped, "plan"),
			item("health_alert", api.AppProtectionItemRemoved, ""),
		}}, []string{"partly protected — rollback gate skipped, health alert removed (gregale app protection demo)", "(plan)"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			main.RenderAppProtection(&out, "demo", tc.resp)
			for _, want := range tc.want {
				if !strings.Contains(out.String(), want) {
					t.Fatalf("output %q lacks %q", out.String(), want)
				}
			}
		})
	}
}
