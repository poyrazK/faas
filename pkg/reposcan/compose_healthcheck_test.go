// adr: 682
package reposcan

import (
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestComposeImageHealthcheckDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		want              *api.ComposeHealthcheck
	}{
		{"absent", "", nil},
		{"empty", "healthcheck: {}", &api.ComposeHealthcheck{}},
		{"null test inherits", "healthcheck: {test: null}", &api.ComposeHealthcheck{}},
		{"shell", `healthcheck: {test: 'curl -f http://localhost:$${PORT}/ready'}`, &api.ComposeHealthcheck{Test: []string{"CMD-SHELL", "curl -f http://localhost:${PORT}/ready"}}},
		{"exec", `healthcheck: {test: [CMD, /check, "argument with spaces"]}`, &api.ComposeHealthcheck{Test: []string{"CMD", "/check", "argument with spaces"}}},
		{"none", `healthcheck: {test: [NONE]}`, &api.ComposeHealthcheck{Test: []string{"NONE"}}},
		{"disabled", `healthcheck: {disable: true, test: [CMD, /check]}`, &api.ComposeHealthcheck{Test: []string{"NONE"}}},
		{"partial", `healthcheck: {interval: 1.5s, timeout: 250ms, start_period: 750ms, start_interval: 50ms, retries: 2}`, &api.ComposeHealthcheck{IntervalNS: int64(1500 * time.Millisecond), TimeoutNS: int64(250 * time.Millisecond), StartPeriodNS: int64(750 * time.Millisecond), StartIntervalNS: int64(50 * time.Millisecond), Retries: 2}},
		{"empty test inherits", `healthcheck: {test: [], disable: false, interval: 0s, retries: 0}`, &api.ComposeHealthcheck{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    ports: [80]\n    " + tc.declaration + "\n"
			result, err := Scan(fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(body)}})
			if err != nil || len(result.Workloads) != 1 {
				t.Fatalf("scan = %+v, %v", result, err)
			}
			if got := result.Workloads[0].ImageHealthcheck; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("healthcheck = %#v; want %#v", got, tc.want)
			}
		})
	}
}

func TestComposeImageHealthcheckRejectsMalformedDeclaration(t *testing.T) {
	for _, declaration := range []string{
		`healthcheck: false`, `healthcheck: {test: 3}`, `healthcheck: {test: [CMD, 3]}`,
		`healthcheck: {test: [bad, /check]}`, `healthcheck: {test: [CMD]}`,
		`healthcheck: {test: [CMD-SHELL, a, b]}`, `healthcheck: {test: [NONE, a]}`,
		`healthcheck: {test: ""}`, `healthcheck: {test: [CMD, ""]}`,
		`healthcheck: {disable: yes}`, `healthcheck: {retries: -1}`, `healthcheck: {retries: 1.5}`,
		`healthcheck: {timeout: -1s}`, `healthcheck: {interval: 1us}`,
		`healthcheck: {start_period: 10000000000000h}`, `healthcheck: {interval: 30}`,
		`healthcheck: {start_interval: never}`, `healthcheck: {retriez: 3}`,
		`healthcheck: {test: [CMD, "a\0b"]}`,
	} {
		t.Run(declaration, func(t *testing.T) {
			body := "services:\n  web:\n    image: nginx:1.27\n    " + declaration + "\n"
			_, err := Scan(fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(body)}})
			if err == nil || !strings.Contains(err.Error(), "healthcheck") {
				t.Fatalf("invalid declaration accepted: %v", err)
			}
		})
	}
}
