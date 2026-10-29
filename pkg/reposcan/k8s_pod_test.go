package reposcan

import (
	"slices"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

func k8sPodFS(body string) fstest.MapFS {
	return fstest.MapFS{"k8s/app.yaml": &fstest.MapFile{Data: []byte(body)}}
}

func k8sOn() ScanOptions { return ScanOptions{KubernetesManifests: true} }

const k8sFullDeployment = `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec:
  template:
    spec:
      terminationGracePeriodSeconds: 45
      securityContext: {runAsUser: 1000, runAsGroup: 2000, fsGroup: 3000}
      nodeSelector: {pool: fast}
      initContainers: [{name: migrate, image: acme/api:1}]
      containers:
        - name: api
          image: acme/api:1
          command: [/bin/server]
          args: [--port, "8080"]
          workingDir: /srv
          ports: [{containerPort: 8080}]
          resources:
            requests: {memory: 256Mi, cpu: 250m}
            limits: {memory: 512Mi, cpu: "1", ephemeral-storage: 1Gi}
          readinessProbe:
            exec: {command: [/bin/check, --ready]}
            periodSeconds: 5
            timeoutSeconds: 2
            failureThreshold: 4
            initialDelaySeconds: 3
          startupProbe:
            httpGet: {path: /healthz, port: 8080}
            periodSeconds: 10
            failureThreshold: 30
          livenessProbe:
            httpGet: {path: /healthz, port: 8080}
          securityContext: {runAsUser: 1001, capabilities: {drop: [ALL]}}
        - name: proxy
          image: envoyproxy/envoy:v1
`

// TestDetectK8s_CommandArgsJoinedWithoutCapability pins today's behaviour
// before ADR-20261029093621 §3 changes it behind the capability: with the
// capability off, command and args are joined into one command and nothing
// else is read from the pod spec.
func TestDetectK8s_CommandArgsJoinedWithoutCapability(t *testing.T) {
	t.Parallel()
	seeds, _, warnings, err := detectK8s(k8sPodFS(k8sFullDeployment))
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("seeds = %v, want 1", names(seeds))
	}
	s := seeds[0]
	if want := []string{"/bin/server", "--port", "8080"}; !slices.Equal(s.command, want) {
		t.Errorf("command = %q, want %q", s.command, want)
	}
	if s.imageProcess != nil || s.resources != nil || s.healthcheck != nil {
		t.Errorf("pod spec mapped without the capability: process=%+v resources=%+v healthcheck=%+v", s.imageProcess, s.resources, s.healthcheck)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none without the capability", warnings)
	}
}

// TestDetectK8s_PodSpecMapping pins ADR-20261029093621 §3 with the
// capability on.
func TestDetectK8s_PodSpecMapping(t *testing.T) {
	t.Parallel()
	seeds, _, warnings, err := detectK8sWith(k8sPodFS(k8sFullDeployment), k8sOn())
	if err != nil {
		t.Fatalf("detectK8sWith: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("seeds = %v, want 1", names(seeds))
	}
	s := seeds[0]

	t.Run("process", func(t *testing.T) {
		if s.imageProcess == nil || s.imageProcess.Entrypoint == nil {
			t.Fatalf("process = %+v, want an entrypoint", s.imageProcess)
		}
		if got := *s.imageProcess.Entrypoint; !slices.Equal(got, []string{"/bin/server"}) {
			t.Errorf("entrypoint = %q, want the container command", got)
		}
		if want := []string{"--port", "8080"}; !slices.Equal(s.command, want) {
			t.Errorf("command = %q, want the container args %q", s.command, want)
		}
		if s.imageProcess.WorkingDir != "/srv" {
			t.Errorf("working dir = %q", s.imageProcess.WorkingDir)
		}
		if s.imageProcess.User != "1001:2000" {
			t.Errorf("user = %q, want container runAsUser with pod runAsGroup", s.imageProcess.User)
		}
		if g := s.imageProcess.StopGracePeriodS; g == nil || *g != 45 {
			t.Errorf("stop grace = %v, want 45", g)
		}
	})

	t.Run("resources", func(t *testing.T) {
		want := &api.ComposeResources{MemoryMB: 512, CPUMillicores: 1000}
		if s.resources == nil || *s.resources != *want {
			t.Errorf("resources = %+v, want limits %+v", s.resources, want)
		}
	})

	t.Run("healthcheck", func(t *testing.T) {
		h := s.healthcheck
		if h == nil {
			t.Fatal("healthcheck = nil, want the readiness exec probe")
		}
		if want := []string{"CMD", "/bin/check", "--ready"}; !slices.Equal(h.Test, want) {
			t.Errorf("test = %q, want %q", h.Test, want)
		}
		if h.IntervalNS != int64(5*time.Second) || h.TimeoutNS != int64(2*time.Second) || h.Retries != 4 {
			t.Errorf("timing = %+v", h)
		}
		// The startupProbe's budget (30 × 10 s) outlasts the readiness delay.
		if h.StartPeriodNS != int64(300*time.Second) {
			t.Errorf("start period = %s, want 5m0s from startupProbe", time.Duration(h.StartPeriodNS))
		}
	})

	t.Run("reported", func(t *testing.T) {
		for _, want := range []string{
			"reposcan: k8s/app.yaml: api: initContainers ignored: init containers are not run",
			"api: nodeSelector ignored: the scheduler places instances",
			"api: containers[proxy] ignored: run sidecars as companions",
			"api: securityContext.fsGroup ignored: each service runs in its own microVM",
			"api: securityContext.capabilities ignored",
			"api: livenessProbe ignored: an instance whose healthcheck fails after it was ready is replaced",
			"api: startupProbe.httpGet ignored: HTTP and gRPC readiness",
			"api: resources.limits.ephemeral-storage ignored",
		} {
			if !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, want) }) {
				t.Errorf("warnings = %q\nmissing %q", warnings, want)
			}
		}
		for _, w := range warnings {
			if strings.Contains(w, "terminationGracePeriodSeconds") || strings.Contains(w, "workingDir") {
				t.Errorf("applied field reported: %q", w)
			}
		}
	})
}

func TestK8sResources(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		resources   map[string]any
		want        *api.ComposeResources
		wantWarning string
		wantErr     string
	}{
		{"none", nil, nil, "", ""},
		{"binary and milli", map[string]any{"limits": map[string]any{"memory": "1.5Gi", "cpu": "500m"}},
			&api.ComposeResources{MemoryMB: 1536, CPUMillicores: 500}, "", ""},
		{"decimal megabytes round up to MiB", map[string]any{"limits": map[string]any{"memory": "500M"}},
			&api.ComposeResources{MemoryMB: 477}, "", ""},
		{"plain bytes", map[string]any{"limits": map[string]any{"memory": 268435456}},
			&api.ComposeResources{MemoryMB: 256}, "", ""},
		{"requests when no limit", map[string]any{"requests": map[string]any{"memory": "128Mi"}},
			&api.ComposeResources{MemoryMB: 128}, "resources.requests.memory used as the app's memory", ""},
		{"cpu rounds up to a share", map[string]any{"limits": map[string]any{"cpu": "300m"}},
			&api.ComposeResources{CPUMillicores: 500}, "resources.limits.cpu 0.3 rounded up to 0.5", ""},
		{"cpu over one is lowered", map[string]any{"limits": map[string]any{"cpu": 2}},
			&api.ComposeResources{CPUMillicores: 1000}, "lowered to 1", ""},
		{"bad quantity", map[string]any{"limits": map[string]any{"memory": "lots"}}, nil, "", "resources.limits.memory"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var raw any
			if tt.resources != nil {
				raw = tt.resources
			}
			got, warnings, err := k8sResources(raw)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one naming %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("k8sResources: %v", err)
			}
			if (got == nil) != (tt.want == nil) || (got != nil && *got != *tt.want) {
				t.Errorf("resources = %+v, want %+v", got, tt.want)
			}
			if tt.wantWarning != "" && !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, tt.wantWarning) }) {
				t.Errorf("warnings = %q, want one containing %q", warnings, tt.wantWarning)
			}
		})
	}
}

func TestK8sHealthcheck(t *testing.T) {
	t.Parallel()
	exec := func(extra map[string]any) map[string]any {
		probe := map[string]any{"exec": map[string]any{"command": []any{"check"}}}
		for k, v := range extra {
			probe[k] = v
		}
		return probe
	}
	tests := []struct {
		name        string
		container   map[string]any
		wantTest    []string
		wantStart   time.Duration
		wantWarning string
		wantErr     bool
	}{
		{name: "no probes", container: map[string]any{}},
		{name: "readiness defaults are Kubernetes defaults", container: map[string]any{"readinessProbe": exec(nil)},
			wantTest: []string{"CMD", "check"}},
		{name: "startup exec alone", container: map[string]any{"startupProbe": exec(map[string]any{"failureThreshold": 6, "periodSeconds": 5})},
			wantTest: []string{"CMD", "check"}, wantStart: 30 * time.Second},
		{name: "http readiness is reported", container: map[string]any{"readinessProbe": map[string]any{"httpGet": map[string]any{"path": "/ready"}}},
			wantWarning: "readinessProbe.httpGet ignored"},
		{name: "tcp readiness is reported", container: map[string]any{"readinessProbe": map[string]any{"tcpSocket": map[string]any{"port": 80}}},
			wantWarning: "readinessProbe.tcpSocket ignored: the platform already waits for the serving port"},
		{name: "http readiness, exec startup", container: map[string]any{
			"readinessProbe": map[string]any{"grpc": map[string]any{"port": 9000}},
			"startupProbe":   exec(nil),
		}, wantTest: []string{"CMD", "check"}, wantStart: 30 * time.Second, wantWarning: "readinessProbe.grpc ignored"},
		{name: "bad threshold", container: map[string]any{"readinessProbe": exec(map[string]any{"failureThreshold": 0})}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, warnings, err := k8sHealthcheck(tt.container)
			if tt.wantErr {
				if err == nil {
					t.Fatal("err = nil, want a validation error")
				}
				return
			}
			if err != nil {
				t.Fatalf("k8sHealthcheck: %v", err)
			}
			if tt.wantTest == nil {
				if got != nil {
					t.Errorf("healthcheck = %+v, want none", got)
				}
			} else {
				if got == nil || !slices.Equal(got.Test, tt.wantTest) {
					t.Fatalf("healthcheck = %+v, want test %q", got, tt.wantTest)
				}
				if got.IntervalNS == 0 || got.TimeoutNS == 0 || got.Retries == 0 {
					t.Errorf("timing not set from Kubernetes defaults: %+v", got)
				}
				if time.Duration(got.StartPeriodNS) != tt.wantStart {
					t.Errorf("start period = %s, want %s", time.Duration(got.StartPeriodNS), tt.wantStart)
				}
			}
			if tt.wantWarning != "" && !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, tt.wantWarning) }) {
				t.Errorf("warnings = %q, want one containing %q", warnings, tt.wantWarning)
			}
		})
	}
}

func TestDetectK8s_PodSpecProcessEdges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		pod         string
		check       func(t *testing.T, s workloadSeed)
		wantWarning string
		wantErr     string
	}{
		{
			name: "default container annotation picks the app",
			pod: `  template:
    metadata: {annotations: {kubectl.kubernetes.io/default-container: web}}
    spec:
      containers:
        - {name: proxy, image: envoyproxy/envoy:v1}
        - {name: web, image: acme/web:2, ports: [{containerPort: 3000}]}
`,
			check: func(t *testing.T, s workloadSeed) {
				if s.image != "acme/web:2" || !slices.Equal(s.ports, []int{3000}) {
					t.Errorf("image=%q ports=%v, want the annotated container", s.image, s.ports)
				}
			},
			wantWarning: "containers[proxy] ignored",
		},
		{
			name: "annotation naming a missing container fails",
			pod: `  template:
    metadata: {annotations: {kubectl.kubernetes.io/default-container: nope}}
    spec: {containers: [{name: web, image: acme/web:2}]}
`,
			wantErr: `names container "nope"`,
		},
		{
			name: "args alone stay the command",
			pod:  "  template: {spec: {containers: [{name: web, image: acme/web:2, args: [serve]}]}}\n",
			check: func(t *testing.T, s workloadSeed) {
				if !slices.Equal(s.command, []string{"serve"}) || s.imageProcess != nil {
					t.Errorf("command=%q process=%+v, want args as the command and the image entrypoint kept", s.command, s.imageProcess)
				}
			},
		},
		{
			name: "zero grace is raised",
			pod:  "  template: {spec: {terminationGracePeriodSeconds: 0, containers: [{name: web, image: acme/web:2}]}}\n",
			check: func(t *testing.T, s workloadSeed) {
				if s.imageProcess == nil || *s.imageProcess.StopGracePeriodS != 1 {
					t.Errorf("process = %+v, want a 1 s grace", s.imageProcess)
				}
			},
			wantWarning: "terminationGracePeriodSeconds 0 raised to 1",
		},
		{
			name: "long grace is lowered",
			pod:  "  template: {spec: {terminationGracePeriodSeconds: 3600, containers: [{name: web, image: acme/web:2}]}}\n",
			check: func(t *testing.T, s workloadSeed) {
				if s.imageProcess == nil || *s.imageProcess.StopGracePeriodS != 300 {
					t.Errorf("process = %+v, want the 5 min ceiling", s.imageProcess)
				}
			},
			wantWarning: "terminationGracePeriodSeconds 3600 lowered to 300",
		},
		{
			name:        "group without user is reported",
			pod:         "  template: {spec: {securityContext: {runAsGroup: 10}, containers: [{name: web, image: acme/web:2}]}}\n",
			wantWarning: "securityContext.runAsGroup ignored: a group applies only with runAsUser",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := "apiVersion: apps/v1\nkind: Deployment\nmetadata: {name: web}\nspec:\n" + tt.pod
			seeds, _, warnings, err := detectK8sWith(k8sPodFS(body), k8sOn())
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("detectK8sWith: %v", err)
			}
			if len(seeds) != 1 {
				t.Fatalf("seeds = %v, want 1", names(seeds))
			}
			if tt.check != nil {
				tt.check(t, seeds[0])
			}
			if tt.wantWarning != "" && !slices.ContainsFunc(warnings, func(w string) bool { return strings.Contains(w, tt.wantWarning) }) {
				t.Errorf("warnings = %q, want one containing %q", warnings, tt.wantWarning)
			}
		})
	}
}

// TestDetectK8s_CronJobPodSpecMapping — a CronJob's pod template lives under
// spec.jobTemplate and maps the same way.
func TestDetectK8s_CronJobPodSpecMapping(t *testing.T) {
	t.Parallel()
	body := `apiVersion: batch/v1
kind: CronJob
metadata: {name: nightly}
spec:
  schedule: "0 3 * * *"
  jobTemplate:
    spec:
      template:
        spec:
          restartPolicy: OnFailure
          containers:
            - name: nightly
              image: acme/jobs:1
              command: [/bin/nightly]
              resources: {limits: {memory: 1Gi}}
`
	seeds, _, warnings, err := detectK8sWith(k8sPodFS(body), k8sOn())
	if err != nil {
		t.Fatalf("detectK8sWith: %v", err)
	}
	if len(seeds) != 1 || seeds[0].resources == nil || seeds[0].resources.MemoryMB != 1024 {
		t.Fatalf("seeds = %+v, want one job sized 1024 MB", seeds)
	}
	if p := seeds[0].imageProcess; p == nil || p.Entrypoint == nil || !slices.Equal(*p.Entrypoint, []string{"/bin/nightly"}) {
		t.Errorf("process = %+v, want the command as entrypoint", p)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none (restartPolicy is harmless)", warnings)
	}
}

// TestScanWith_KubernetesManifestsReachesWorkloads — the option flows from
// ScanWithOptions through merge into the planned workload.
func TestScanWith_KubernetesManifestsReachesWorkloads(t *testing.T) {
	t.Parallel()
	for _, on := range []bool{false, true} {
		result, err := ScanWithOptions(k8sPodFS(k8sFullDeployment), ScanOptions{KubernetesManifests: on})
		if err != nil {
			t.Fatalf("ScanWithOptions(%v): %v", on, err)
		}
		if len(result.Workloads) != 1 {
			t.Fatalf("ScanWithOptions(%v) workloads = %d, want 1", on, len(result.Workloads))
		}
		w := result.Workloads[0]
		if mapped := w.Resources != nil && w.ImageHealthcheck != nil && w.ImageProcess != nil; mapped != on {
			t.Errorf("ScanWithOptions(%v): resources=%+v healthcheck=%+v process=%+v", on, w.Resources, w.ImageHealthcheck, w.ImageProcess)
		}
	}
}
