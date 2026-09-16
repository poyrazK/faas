package reposcan

import (
	"io/fs"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

// TestDetectK8s_Deployment — a stateless Deployment becomes a
// single workload with class=http, command from containers[0],
// env-keys from containers[0].env[].name, ports from
// containers[0].ports[].containerPort.
func TestDetectK8s_Deployment(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: api
spec:
  template:
    spec:
      containers:
        - name: api
          image: ghcr.io/acme/api:latest
          command: ["bundle", "exec", "rails", "s"]
          env:
            - {name: RAILS_ENV, value: production}
            - {name: DATABASE_URL, value: postgres://x}
          ports:
            - {containerPort: 8080}
`
	fsys := fstest.MapFS{
		"k8s":                     &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/api.deployment.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectK8s(fsys)
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("seeds = %v, want 1", names(seeds))
	}
	if seeds[0].name != "api" {
		t.Errorf("name = %q, want api", seeds[0].name)
	}
	if seeds[0].class != ClassHTTP {
		t.Errorf("class = %q, want http", seeds[0].class)
	}
	if len(seeds[0].command) != 4 || seeds[0].command[0] != "bundle" {
		t.Errorf("command = %v", seeds[0].command)
	}
	if seeds[0].commandShell {
		t.Error("Kubernetes command/args must retain exec-form argument boundaries")
	}
	wantPorts := []int{8080}
	if !equalSet(intToStr(wantPorts), intToStr(seeds[0].ports)) {
		t.Errorf("ports = %v, want %v", seeds[0].ports, wantPorts)
	}
	wantEnv := []string{"RAILS_ENV", "DATABASE_URL"}
	if !equalSet(wantEnv, seeds[0].envKeys) {
		t.Errorf("envKeys = %v, want %v (sorted)", seeds[0].envKeys, wantEnv)
	}
}

// TestDetectK8s_CronJob — CronJob yields class=job + the
// declared schedule.
func TestDetectK8s_CronJob(t *testing.T) {
	t.Parallel()
	body := `apiVersion: batch/v1
kind: CronJob
metadata:
  name: nightly
spec:
  schedule: "*/5 * * * *"
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: nightly
              image: ghcr.io/acme/nightly
`
	fsys := fstest.MapFS{
		"k8s":                      &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/nightly.cronjob.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectK8s(fsys)
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	if len(seeds) != 1 {
		t.Fatalf("seeds = %v, want 1", names(seeds))
	}
	if seeds[0].name != "nightly" || seeds[0].class != ClassJob || seeds[0].schedule != "*/5 * * * *" {
		t.Errorf("seed = (%s %s %s), want (nightly job */5 * * * *)",
			seeds[0].name, seeds[0].class, seeds[0].schedule)
	}
}

func TestDetectK8s_CronJobPreservesExecutionAndSuspend(t *testing.T) {
	t.Parallel()
	body := `apiVersion: batch/v1
kind: CronJob
metadata: {name: cleanup}
spec:
  schedule: "0 * * * *"
  suspend: true
  jobTemplate:
    spec:
      template:
        spec:
          containers:
            - name: cleanup
              image: ghcr.io/example/cleanup:v1
              command: ["node"]
              args: ["cleanup.js"]
              env: [{name: CLEANUP_MODE, value: hourly}]
`
	seeds, _, _, err := detectK8s(fstest.MapFS{
		"k8s":              &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/cleanup.yaml": &fstest.MapFile{Data: []byte(body)},
	})
	if err != nil || len(seeds) != 1 {
		t.Fatalf("detectK8s: seeds=%#v err=%v", seeds, err)
	}
	seed := seeds[0]
	if strings.Join(seed.command, " ") != "node cleanup.js" || seed.image != "ghcr.io/example/cleanup:v1" {
		t.Errorf("execution = command=%v image=%q", seed.command, seed.image)
	}
	if len(seed.envKeys) != 1 || seed.envKeys[0] != "CLEANUP_MODE" {
		t.Errorf("envKeys = %v", seed.envKeys)
	}
	if len(seed.schedules) != 1 || seed.schedules[0].Enabled {
		t.Errorf("schedules = %#v, want one disabled schedule", seed.schedules)
	}
}

func TestDetectK8s_DeploymentDatastoresAreManaged(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec: {template: {spec: {containers: [{name: api, image: ghcr.io/example/api:v1}]}}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: cache}
spec: {template: {spec: {containers: [{name: cache, image: redis:7}]}}}
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: database}
spec: {template: {spec: {containers: [{name: database, image: postgres:16}]}}}
`
	seeds, managed, _, err := detectK8s(fstest.MapFS{
		"k8s":             &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/images.yaml": &fstest.MapFile{Data: []byte(body)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seeds) != 1 || seeds[0].name != "api" || seeds[0].image != "ghcr.io/example/api:v1" {
		t.Fatalf("workloads = %#v, want only image-backed api", seeds)
	}
	if len(managed) != 2 || managed[0].Name != "cache" || managed[1].Name != "database" {
		t.Fatalf("managed = %#v, want cache and database", managed)
	}
}

// TestDetectK8s_StatefulSetRefused — StatefulSet must surface a
// warning (and NOT a workload). The stateless contract is the
// same one compose enforces via the datastore denylist — a
// k8s-managed StatefulSet is also surfaced.
func TestDetectK8s_StatefulSetRefused(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: StatefulSet
metadata:
  name: pg
spec:
  template:
    spec:
      containers:
        - {name: pg, image: postgres:15}
`
	fsys := fstest.MapFS{
		"k8s":                     &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/pg.statefulset.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, warnings, err := detectK8s(fsys)
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty (StatefulSet refused)", names(seeds))
	}
	if len(warnings) != 1 {
		t.Fatalf("warnings = %v, want 1", warnings)
	}
	if !contains(warnings[0], "StatefulSet") || !contains(warnings[0], "refusing") {
		t.Errorf("warning %q should mention StatefulSet and refusing", warnings[0])
	}
}

// TestDetectK8s_MultiDocumentYAML — the document-separator form
// `---` is honored, so one file with two Deployments produces two
// workloads.
func TestDetectK8s_MultiDocumentYAML(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: a
spec:
  template: {spec: {containers: [{name: a, image: img-a}]}}
---
apiVersion: apps/v1
kind: Deployment
metadata:
  name: b
spec:
  template: {spec: {containers: [{name: b, image: img-b}]}}
`
	fsys := fstest.MapFS{
		"k8s":            &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/multi.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectK8s(fsys)
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	got := names(seeds)
	sort.Strings(got)
	if !equalSet(got, []string{"a", "b"}) {
		t.Errorf("seeds = %v, want {a,b}", got)
	}
}

// TestDetectK8s_MultiDocumentYAMLMarkers — YAML permits comments and
// trailing whitespace on document markers, and explicit end markers.
func TestDetectK8s_MultiDocumentYAMLMarkers(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata: {name: a}
spec: {template: {spec: {containers: [{name: a, image: img-a}]}}}
--- # worker resource
apiVersion: apps/v1
kind: Deployment
metadata: {name: b}
spec: {template: {spec: {containers: [{name: b, image: img-b}]}}}
---TRAILING_MARKER
apiVersion: apps/v1
kind: Deployment
metadata: {name: c}
spec: {template: {spec: {containers: [{name: c, image: img-c}]}}}
...
`
	body = strings.ReplaceAll(body, "---TRAILING_MARKER", "---   ")
	fsys := fstest.MapFS{
		"k8s":              &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/markers.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, warnings, err := detectK8s(fsys)
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	got := names(seeds)
	sort.Strings(got)
	if !equalSet(got, []string{"a", "b", "c"}) {
		t.Fatalf("seeds = %v, want {a,b,c}", got)
	}
}

// TestDetectK8s_MalformedDocumentFailsClosed — a partially decoded manifest
// cannot produce an applicable topology.
func TestDetectK8s_MalformedDocumentFailsClosed(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata: {name: before}
spec: {template: {spec: {containers: [{name: before, image: img}]}}}
--- # malformed
apiVersion: [broken
---
apiVersion: apps/v1
kind: Deployment
metadata: {name: after}
spec: {template: {spec: {containers: [{name: after, image: img}]}}}
`
	fsys := fstest.MapFS{
		"k8s":                &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/malformed.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, warnings, err := detectK8s(fsys)
	if err == nil || !contains(err.Error(), "document 2") {
		t.Fatalf("err = %v, want parse error identifying document 2", err)
	}
	if len(seeds) != 0 || len(warnings) != 0 {
		t.Fatalf("partial result escaped: seeds=%v warnings=%v", names(seeds), warnings)
	}
}

func TestDetectK8s_MissingContainerImageFailsClosed(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"deployment": `apiVersion: apps/v1
kind: Deployment
metadata: {name: api}
spec: {template: {spec: {containers: [{name: api}]}}}
`,
		"cronjob": `apiVersion: batch/v1
kind: CronJob
metadata: {name: cleanup}
spec:
  schedule: "0 * * * *"
  jobTemplate: {spec: {template: {spec: {containers: []}}}}
`,
	} {
		name, body := name, body
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			seeds, managed, warnings, err := detectK8s(fstest.MapFS{
				"k8s":                 &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
				"k8s/incomplete.yaml": &fstest.MapFile{Data: []byte(body)},
			})
			if err == nil || !contains(err.Error(), "first container image") {
				t.Fatalf("err = %v, want missing first container image error", err)
			}
			if len(seeds) != 0 || len(managed) != 0 || len(warnings) != 0 {
				t.Fatalf("partial result escaped: seeds=%v managed=%v warnings=%v", names(seeds), managed, warnings)
			}
		})
	}
}

// TestDetectK8s_RootDirAlternatives — kubernetes/, deploy/,
// manifests/ are all accepted. Order: first present wins for
// the entire walk (one detector per fsys, by spec, not by file).
func TestDetectK8s_RootDirAlternatives(t *testing.T) {
	t.Parallel()
	body := `apiVersion: apps/v1
kind: Deployment
metadata:
  name: web
spec:
  template: {spec: {containers: [{name: web, image: img}]}}
`
	for _, dir := range k8sRootDirs {
		fsys := fstest.MapFS{}
		fsys[dir] = &fstest.MapFile{Mode: 0o755 | fs.ModeDir}
		fsys[dir+"/web.yaml"] = &fstest.MapFile{Data: []byte(body)}
		seeds, _, _, err := detectK8s(fsys)
		if err != nil {
			t.Errorf("dir=%s: %v", dir, err)
			continue
		}
		if got := names(seeds); !equalSet(got, []string{"web"}) {
			t.Errorf("dir=%s: seeds = %v, want {web}", dir, got)
		}
	}
}

// helpers used by k8s tests only
func intToStr(xs []int) []string {
	out := make([]string, len(xs))
	for i, x := range xs {
		out[i] = intStr(x)
	}
	sort.Strings(out)
	return out
}
func intStr(x int) string {
	if x == 0 {
		return "0"
	}
	neg := false
	if x < 0 {
		neg = true
		x = -x
	}
	var buf [20]byte
	i := len(buf)
	for x > 0 {
		i--
		buf[i] = byte('0' + x%10)
		x /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
