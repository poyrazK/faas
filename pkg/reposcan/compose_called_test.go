package reposcan

import (
	"strings"
	"testing"
	"testing/fstest"
)

// ADR-20261029120000: a Compose service without ports is an HTTP service
// when another service calls it, and a worker otherwise. depends_on alone is
// only start order: it makes a service the repository shows listening
// (expose:, or its own image's Dockerfile EXPOSE or HTTP framework,
// amendment 1) an HTTP service, and leaves a worker with a warning.
func TestComposeCalledServicesServeRequests(t *testing.T) {
	for _, tc := range []struct {
		name    string
		compose string
		project string
		files   map[string]string
		want    map[string]Class
		warning string
	}{
		{
			name: "depends_on onto a service whose Dockerfile EXPOSEs a port",
			compose: `services:
  web: {build: ./web, ports: ["80:3000"], depends_on: [api]}
  api: {build: ./api}
`,
			files: map[string]string{
				"web/Dockerfile": "FROM node:22\nEXPOSE 3000\n",
				"api/Dockerfile": "FROM node:22\nEXPOSE 8000\nCMD [\"node\", \"server.js\"]\n",
			},
			want: map[string]Class{"api": ClassHTTP},
		},
		{
			name: "depends_on onto an HTTP framework built without a Dockerfile",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], depends_on: [api]}
  api: {build: ./api}
`,
			files: map[string]string{"api/package.json": `{"dependencies": {"express": "^4.19.0"}}`},
			want:  map[string]Class{"api": ClassHTTP},
		},
		{
			name: "a command override runs something other than the image",
			compose: `services:
  web: {build: ., ports: ["80:8000"], depends_on: [worker]}
  worker: {build: ., command: celery -A app worker}
`,
			files:   map[string]string{"Dockerfile": "FROM python:3.12\nEXPOSE 8000\nCMD [\"gunicorn\", \"app:app\"]\n"},
			want:    map[string]Class{"web": ClassHTTP, "worker": ClassWorker},
			warning: `service "worker" publishes no ports and runs as a background worker, though "web" lists it in depends_on`,
		},
		{
			name: "a Dockerfile that exposes nothing",
			compose: `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"], depends_on: [consumer]}
  consumer: {build: ./consumer}
`,
			files:   map[string]string{"consumer/Dockerfile": "FROM golang:1.26\nCMD [\"/consumer\"]\n"},
			want:    map[string]Class{"consumer": ClassWorker},
			warning: `service "consumer" publishes no ports`,
		},
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
			name: "a gRPC host:port target that listens",
			compose: `services:
  frontend:
    build: ./src/frontend
    ports: ["8080:8080"]
    environment: {PRODUCT_CATALOG_SERVICE_ADDR: "productcatalogservice:3550"}
  productcatalogservice: {build: ./src/productcatalogservice}
`,
			files: map[string]string{
				"src/frontend/Dockerfile":              "FROM golang:1.26\nEXPOSE 8080\n",
				"src/productcatalogservice/Dockerfile": "FROM golang:1.26\nEXPOSE 3550\nCMD [\"/server\"]\n",
			},
			want: map[string]Class{"productcatalogservice": ClassHTTP},
		},
		{
			name: "a host:port target that shows no listener stays a worker",
			compose: `services:
  web:
    image: ghcr.io/x/web:1
    ports: ["80:3000"]
    environment: {QUEUE_ADDR: "consumer:7000"}
  consumer: {build: ./consumer}
`,
			files: map[string]string{"consumer/Dockerfile": "FROM golang:1.26\nCMD [\"/consumer\"]\n"},
			want:  map[string]Class{"consumer": ClassWorker},
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
			for name, body := range tc.files {
				fsys[name] = &fstest.MapFile{Data: []byte(body)}
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

// Retail-store probe 2026-11-17: a Compose service with only expose: and no
// caller was planned as a worker without a word, then every boot failed with
// execution_mode_conflict. It stays a worker (ADR-20261029120000), but the
// plan now says why it will fail.
func TestComposeUncalledExposedServiceIsWarned(t *testing.T) {
	compose := `services:
  web: {image: ghcr.io/x/web:1, ports: ["80:3000"]}
  probe: {image: nginx:1.27, expose: ["80"]}
  queue: {image: ghcr.io/x/consumer:1}
`
	result, err := Scan(fstest.MapFS{"compose.yaml": {Data: []byte(compose)}})
	if err != nil {
		t.Fatal(err)
	}
	var probe, queue Class
	for _, w := range result.Workloads {
		switch w.Name {
		case "probe":
			probe = w.Class
		case "queue":
			queue = w.Class
		}
	}
	if probe != ClassWorker || queue != ClassWorker {
		t.Fatalf("classes probe=%q queue=%q, want both workers", probe, queue)
	}
	var found []string
	for _, warning := range result.Warnings {
		if strings.Contains(warning, "execution_mode_conflict") {
			found = append(found, warning)
		}
	}
	if len(found) != 1 || !strings.Contains(found[0], `service "probe" declares expose: [80]`) || !strings.Contains(found[0], "http://probe:80") {
		t.Fatalf("warnings = %v, want one for probe only (queue listens on nothing declared)", result.Warnings)
	}
}
