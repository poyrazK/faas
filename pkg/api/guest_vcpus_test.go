// adr: 20261101160231

package api_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestGuestVCPUsForCPUMillicores(t *testing.T) {
	for _, tc := range []struct{ millicores, want int }{
		{0, 1}, {250, 1}, {500, 1}, {api.DefaultAppCPUMillicores, 1}, {1001, 2}, {2000, 2}, {2500, 3},
	} {
		if got := api.GuestVCPUsForCPUMillicores(tc.millicores); got != tc.want {
			t.Errorf("GuestVCPUsForCPUMillicores(%d) = %d, want %d", tc.millicores, got, tc.want)
		}
	}
}
