// adr: 20261019195000
package reposcan

import (
	"reflect"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

// A built service's Compose healthcheck is carried like a prebuilt image's
// instead of being warned and dropped.
func TestComposeSourceBuildHealthcheck(t *testing.T) {
	for _, tc := range []struct {
		name, declaration string
		want              *api.ComposeHealthcheck
	}{
		{"absent", "", nil},
		{"command", "healthcheck: {test: [CMD, /app/check], retries: 2}", &api.ComposeHealthcheck{Test: []string{"CMD", "/app/check"}, Retries: 2}},
		{"timing only", "healthcheck: {interval: 5s}", &api.ComposeHealthcheck{IntervalNS: int64(5e9)}},
		{"disabled", "healthcheck: {disable: true}", &api.ComposeHealthcheck{Test: []string{"NONE"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "services:\n  api:\n    build: .\n    ports: [3000]\n    " + tc.declaration + "\n"
			result, err := scanCompose(t, body, "Dockerfile", "FROM node:22\n")
			if err != nil {
				t.Fatal(err)
			}
			if got := composeBuildWorkload(t, result, "api").ImageHealthcheck; !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("healthcheck = %#v; want %#v", got, tc.want)
			}
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "healthcheck") {
					t.Fatalf("unexpected warning %q", warning)
				}
			}
		})
	}
	if _, err := scanCompose(t, "services:\n  api:\n    build: .\n    healthcheck: {test: [CMD]}\n", "Dockerfile", "FROM node:22\n"); err == nil ||
		!strings.Contains(err.Error(), "healthcheck") {
		t.Fatalf("malformed built-service healthcheck accepted: %v", err)
	}
}
