package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261029120000: a Compose service without ports is an HTTP service
// when another service calls it, and a worker otherwise. depends_on alone is
// only start order: it makes a service that listens (expose:) an HTTP
// service, and leaves a worker with a warning.
func TestComposeCalledServicesServeRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		compose string
		project string
		want    map[string]Class
		warning string
	}{
		{
			name: "depends_on from a public service on a listener",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], depends_on: [api]}
  api: {image: ghcr.io/x/api:1, expose: ["8000"]}
`,
			want: map[string]Class{"web": ClassHTTP, "api": ClassHTTP},
		},
		{
			name: "depends_on alone leaves a worker and warns",
			compose: `services:
  gateway: {image: ghcr.io/x/gateway:1, ports: ["80:80"], depends_on: [worker]}
  worker: {image: ghcr.io/x/worker:1, command: /app/worker --queue jobs}
`,
			want:    map[string]Class{"worker": ClassWorker},
			warning: `service "worker" publishes no ports and runs as a background worker, though "gateway" lists it in depends_on`,
		},
		{
			name: "a chain of internal services",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], depends_on: [api]}
  api: {image: ghcr.io/x/api:1, expose: ["8000"], depends_on: [auth]}
  auth: {image: ghcr.io/x/auth:1, expose: ["9000"]}
`,
			want: map[string]Class{"api": ClassHTTP, "auth": ClassHTTP},
		},
		{
			name: "an address in another service's environment",
			compose: `services:
  web:
    image: ghcr.io/x/web:1
    ports: ["80:3000"]
    environment: {API_URL: "http://api:8000/v1"}
  api: {image: ghcr.io/x/api:1}
`,
			want: map[string]Class{"api": ClassHTTP},
		},
		{
			name: "an address in a worker's environment",
			compose: `services:
  worker:
    image: ghcr.io/x/worker:1
    environment: ["CALLBACK=wss://api:8000/events"]
  api: {image: ghcr.io/x/api:1}
`,
			want: map[string]Class{"worker": ClassWorker, "api": ClassHTTP},
		},
		{
			name: "depends_on from .gregale/project.yaml",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"]}
  api: {image: ghcr.io/x/api:1, expose: ["8000"]}
`,
			project: "services:\n  web:\n    depends_on: [api]\n",
			want:    map[string]Class{"api": ClassHTTP},
		},
		{
			name: "background workers nothing calls",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], depends_on: [redis]}
  worker:
    image: ghcr.io/x/web:1
    command: celery -A app worker
    expose: ["5555"]
    environment: {SELF: "http://worker:5555", BROKER: "redis://redis:6379/0"}
  beat: {image: ghcr.io/x/web:1, command: celery -A app beat, depends_on: [worker]}
  redis: {image: redis:7}
`,
			want: map[string]Class{"worker": ClassWorker, "beat": ClassWorker},
		},
		{
			name: "expose alone is not a call",
			compose: `services:
  cache: {image: ghcr.io/x/cache:1, expose: ["6379"]}
`,
			want: map[string]Class{"cache": ClassWorker},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{"compose.yaml": &fstest.MapFile{Data: []byte(tc.compose)}}
			if tc.project != "" {
				fsys[ProjectConfigPath] = &fstest.MapFile{Data: []byte(tc.project)}
			}
			result, err := Scan(fsys)
			if err != nil {
				t.Fatal(err)
			}
			got := map[string]Class{}
			for _, w := range result.Workloads {
				got[w.Name] = w.Class
				if w.composeDefaultWorker {
					t.Fatalf("%s keeps the scan-internal default-worker mark", w.Name)
				}
			}
			for name, want := range tc.want {
				if got[name] != want {
					t.Errorf("%s class = %q, want %q (all: %v)", name, got[name], want, got)
				}
			}
			warned := false
			for _, warning := range result.Warnings {
				if strings.Contains(warning, "runs as a background worker") {
					if tc.warning == "" || !strings.Contains(warning, tc.warning) {
						t.Errorf("unexpected warning %q", warning)
					}
					warned = true
				}
			}
			if tc.warning != "" && !warned {
				t.Errorf("warnings = %v, want one containing %q", result.Warnings, tc.warning)
			}
		})
	}
}
