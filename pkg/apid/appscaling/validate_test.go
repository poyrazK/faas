package appscaling

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Prod hunt r53: `gregale app <slug> --ram 8` was saved; every later wake
// would cold-boot a guest that cannot start. ram_mb below api.AppMinRAMMB is
// refused when it is set, and an older app below it can still change other
// settings.
func TestValidateSettingsRefusesRAMBelowTheBootableMinimum(t *testing.T) {
	account := state.Account{Plan: "scale"}
	app := state.App{RAMMB: 256, MaxConcurrency: 1}
	for _, tc := range []struct {
		ram  int
		code string
	}{{8, api.CodeInvalidAppRAM}, {0, api.CodeInvalidAppRAM}, {api.AppMinRAMMB - 1, api.CodeInvalidAppRAM}, {api.AppMinRAMMB, ""}, {256, ""}} {
		ram := tc.ram
		prob := validateSettings(account, app, api.UpdateAppRequest{RAMMB: &ram})
		got := ""
		if prob != nil {
			got = prob.Code
		}
		if got != tc.code {
			t.Errorf("ram_mb %d: code %q, want %q", tc.ram, got, tc.code)
		}
	}
	old := state.App{RAMMB: 32, MaxConcurrency: 1}
	concurrency := 1
	if prob := validateSettings(account, old, api.UpdateAppRequest{MaxConcurrency: &concurrency}); prob != nil {
		t.Fatalf("an app below the minimum could not change another setting: %+v", prob)
	}
}
