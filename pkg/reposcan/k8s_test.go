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

// TestDetectK8s_MalformedDocumentDoesNotHideSiblings — a malformed document
// is reported with its stream index while later valid documents remain
// visible to the scanner.
func TestDetectK8s_MalformedDocumentDoesNotHideSiblings(t *testing.T) {
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
	if err != nil {
		t.Fatalf("detectK8s: %v", err)
	}
	got := names(seeds)
	sort.Strings(got)
	if !equalSet(got, []string{"after", "before"}) {
		t.Fatalf("seeds = %v, want {after,before}", got)
	}
	if len(warnings) != 1 || !contains(warnings[0], "document 2") {
		t.Fatalf("warnings = %v, want one warning identifying document 2", warnings)
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
