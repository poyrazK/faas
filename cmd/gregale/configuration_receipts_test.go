package main_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
	"github.com/onebox-faas/faas/pkg/api"
)

// production-us hunt #9: `app --ram 512` printed "Updated" while the running
// instance kept 1024 MB (and its billing) until a restart; CPU changes apply
// live, memory does not.
func TestAppSettingsReceiptWarnsThatMemoryNeedsARestart(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request api.UpdateAppRequest
		env     string
		want    bool
	}{
		{name: "ram", request: api.UpdateAppRequest{RAMMB: ptrTo(512)}, want: true},
		{name: "profile", request: api.UpdateAppRequest{ResourceProfile: ptrTo("small")}, want: true},
		{name: "cpu only", request: api.UpdateAppRequest{CPUMillicores: ptrTo(250)}},
		{name: "environment ram", request: api.UpdateAppRequest{RAMMB: ptrTo(512)}, env: "staging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, restore := main.CaptureStdout(t)
			defer restore()
			main.PrintAppSettingsReceipt("shop", tc.env, tc.request)
			got := strings.Contains(out.String(), "app shop restart")
			if got != tc.want {
				t.Fatalf("restart advice present = %v, want %v:\n%s", got, tc.want, out.String())
			}
		})
	}
}

func ptrTo[T any](v T) *T { return &v }
