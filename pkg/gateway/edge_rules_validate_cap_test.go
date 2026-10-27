package gateway

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// adr: 091 — kind=validate holds the body in memory, so an unset cap is the
// 25 MiB platform cap (spec §4.1.2), not the plan's spooled-body cap.
func TestValidateBodyCap(t *testing.T) {
	const mib = 1 << 20
	cases := []struct {
		name    string
		rule    int
		plan    int64
		wantCap int
	}{
		{"unset on scale", 0, 250 * mib, api.MaxRequestBodyBytes},
		{"unset on free", 0, 10 * mib, 10 * mib},
		{"rule below plan", 2048, 250 * mib, 2048},
		{"rule above plan", 20 * mib, 10 * mib, 10 * mib},
		{"rule above platform", 100 * mib, 250 * mib, api.MaxRequestBodyBytes},
		{"negative", -1, 250 * mib, api.MaxRequestBodyBytes},
	}
	for _, tc := range cases {
		if got := validateBodyCap(tc.rule, tc.plan); got != tc.wantCap {
			t.Errorf("%s: validateBodyCap(%d, %d) = %d, want %d", tc.name, tc.rule, tc.plan, got, tc.wantCap)
		}
	}
}
