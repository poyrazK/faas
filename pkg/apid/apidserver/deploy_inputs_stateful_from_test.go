package apidserver_test

import (
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// TestScanDockerfileRefusesDatastoreBase: a Dockerfile whose final stage runs
// a datastore image is refused like `deploy --image redis`. The 2026-11-15
// agent eval deployed Redis with a one-line `FROM redis` Dockerfile after the
// image deploy was refused.
func TestScanDockerfileRefusesDatastoreBase(t *testing.T) {
	cases := []struct {
		name, dockerfile, want string
	}{
		{"one-line redis", "FROM redis:7-alpine\nCMD [\"redis-server\", \"--save\", \"\"]\n", "Upstash"},
		{"pinned postgres", "FROM docker.io/library/postgres:16@sha256:" + strings.Repeat("a", 64) + "\n", "Neon"},
		{"datastore build stage only", "FROM postgres:16 AS client\nFROM node:22-slim\nCOPY --from=client /usr/bin/psql /usr/bin/psql\n", ""},
		{"stateless app", "FROM node:22-slim\nWORKDIR /app\nCOPY . .\n", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reason := apidserver.ScanDockerfileForStatefulShape([]byte(tc.dockerfile))
			if tc.want == "" {
				if reason != "" {
					t.Fatalf("refused a stateless Dockerfile: %q", reason)
				}
				return
			}
			if !strings.Contains(reason, "datastore") || !strings.Contains(reason, tc.want) {
				t.Fatalf("reason = %q, want a datastore refusal naming %s", reason, tc.want)
			}
		})
	}
}
