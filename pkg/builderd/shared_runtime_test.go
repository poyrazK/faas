package builderd

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// spec: §4.6
func TestSharedRuntimeBuildLog(t *testing.T) {
	tests := []struct {
		name string
		v    *api.SharedRuntimeVerdict
		want string
	}{
		{"not classified", nil, ""},
		{"eligible", &api.SharedRuntimeVerdict{Eligible: true}, "shared runtime: eligible (the app layer would need only /app)\n"},
		{"ineligible", &api.SharedRuntimeVerdict{Reason: "system_packages"}, "shared runtime: not eligible (system_packages)\n"},
	}
	for _, tt := range tests {
		if got := sharedRuntimeBuildLog(tt.v); got != tt.want {
			t.Errorf("%s: %q, want %q", tt.name, got, tt.want)
		}
	}
}
