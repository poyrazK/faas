package reposcan

import (
	"io/fs"
	"strings"
	"testing"
	"testing/fstest"
)

func TestScan_MalformedComposeVariantsCannotFallBackToRoot(t *testing.T) {
	t.Parallel()
	for _, filename := range composeFileNames {
		filename := filename
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			fsys := fstest.MapFS{
				filename:       &fstest.MapFile{Data: []byte("services:\n  api:\n    build: [\n")},
				"package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node index.js"}}`)},
			}
			result, err := Scan(fsys)
			if err == nil || !strings.Contains(err.Error(), filename) {
				t.Fatalf("Scan err=%v, want parse failure naming %s", err, filename)
			}
			if len(result.Workloads) != 0 {
				t.Fatalf("fallback workloads = %#v, want none", result.Workloads)
			}
		})
	}
}

func TestScan_RootFloorUsesFrameworkProfile(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		files     fstest.MapFS
		wantClass Class
		wantCmd   string
		wantShell bool
	}{
		{
			name: "node app",
			files: fstest.MapFS{
				"package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"node handler.js"}}`)},
			},
			wantClass: ClassHTTP, wantCmd: "npm run start", wantShell: true,
		},
		{
			name: "go app",
			files: fstest.MapFS{
				"go.mod":  &fstest.MapFile{Data: []byte("module example.com/app\n\ngo 1.24\n")},
				"main.go": &fstest.MapFile{Data: []byte("package main\n")},
			},
			wantClass: ClassHTTP, wantCmd: "go run .", wantShell: true,
		},
		{
			name: "function-shaped node source",
			files: fstest.MapFS{
				"gregale.yaml": &fstest.MapFile{Data: []byte("function:\n  runtime: node22\n  handler: handler.handler\n")},
				"package.json": &fstest.MapFile{Data: []byte(`{"name":"function-gregale"}`)},
				"handler.js":   &fstest.MapFile{Data: []byte("export function handler() {}\n")},
			},
			wantClass: ClassHTTP,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := Scan(tt.files)
			if err != nil {
				t.Fatal(err)
			}
			if len(result.Workloads) != 1 {
				t.Fatalf("workloads = %#v, want one root workload", result.Workloads)
			}
			workload := result.Workloads[0]
			if workload.Class != tt.wantClass {
				t.Errorf("class = %q, want %q", workload.Class, tt.wantClass)
			}
			if got := strings.Join(workload.Command, " "); got != tt.wantCmd {
				t.Errorf("command = %q, want %q", got, tt.wantCmd)
			}
			if workload.CommandShell != tt.wantShell {
				t.Errorf("command_shell = %t, want %t", workload.CommandShell, tt.wantShell)
			}
		})
	}
}

// TestScan_ComposeK8sFixture is the §4 Phase 2 acceptance gate
// fixture. Per docs/repo_decomposition_implementation.md §4.260:
// compose (4 services, 2 datastores) + k8s CronJob → exactly 3
// workloads, 2 managed entries, correct schedule, deterministic
// order. Scanner never reads outside the tarball root.
//
// The fixture:
//
//	compose.yaml:
//	  api        — build, ports 8080, class=unknown
//	  worker     — build, command bundle exec sidekiq
//	  db         — image: postgres:15       → Managed (postgres / DATABASE_URL)
//	  cache      — image: redis:7           → Managed (redis / REDIS_URL)
//
//	k8s/cronjob.yaml:
//	  nightly    — CronJob, schedule "*/5 * * * *", class=job
//
// Expected:
//
//	Workloads (3, sorted by Name): api, nightly, worker
//	  api      TierCompose, class=unknown, ports=[8080], env=RAILS_ENV+DATABASE_URL
//	  nightly  TierCompose, class=job, schedule="*/5 * * * *"
//	  worker   TierCompose, class=unknown, command=bundle exec sidekiq
//
//	Managed (2, sorted by Name): cache, db
//	  cache    postgres, REDIS_URL          (image matches redis denylist)
//	  db       postgres, DATABASE_URL       (image matches postgres denylist)
//
//	Warnings: none. (No prebuilt-without-build, no broken YAML.)
//
// Plus the scanner-level invariants:
//   - Result.Tier == TierCompose (highest seen)
//   - deterministic across repeated runs (golden Reproducible runs)
func TestScan_ComposeK8sFixture(t *testing.T) {
	t.Parallel()
	fsys := composeK8sFixture(t)

	r, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	// Result.Tier
	if r.Tier != TierCompose {
		t.Errorf("Result.Tier = %s, want compose", r.Tier)
	}

	// Workloads: exactly 3, sorted.
	if len(r.Workloads) != 3 {
		t.Fatalf("Workloads count = %d, want 3 (api, nightly, worker)", len(r.Workloads))
	}
	wantOrder := []string{"api", "nightly", "worker"}
	for i, want := range wantOrder {
		if r.Workloads[i].Name != want {
			t.Errorf("Workloads[%d].Name = %q, want %q (sorted by Name)",
				i, r.Workloads[i].Name, want)
		}
	}

	// Per-workload assertions.
	for _, w := range r.Workloads {
		switch w.Name {
		case "api":
			if w.Class != ClassHTTP {
				t.Errorf("api Class = %q, want http (compose publishes a port)", w.Class)
			}
			if len(w.Ports) != 1 || w.Ports[0] != 8080 {
				t.Errorf("api Ports = %v, want [8080]", w.Ports)
			}
			if !equalSet(w.EnvKeys, []string{"RAILS_ENV", "DATABASE_URL"}) {
				t.Errorf("api EnvKeys = %v, want {RAILS_ENV, DATABASE_URL}", w.EnvKeys)
			}
			if w.Source != "compose.yaml: api" {
				t.Errorf("api Source = %q", w.Source)
			}
		case "nightly":
			if w.Class != ClassJob {
				t.Errorf("nightly Class = %q, want job", w.Class)
			}
			if w.Schedule != "*/5 * * * *" {
				t.Errorf("nightly Schedule = %q, want '*/5 * * * *'", w.Schedule)
			}
			if w.Source != "k8s/nightly.cronjob.yaml: nightly" &&
				w.Source != "k8s/cronjob.yaml: nightly" {
				t.Errorf("nightly Source = %q, want k8s/...: nightly", w.Source)
			}
		case "worker":
			if w.Class != ClassWorker {
				t.Errorf("worker Class = %q, want worker (compose publishes no port)", w.Class)
			}
			if len(w.Command) != 1 || w.Command[0] != "bundle exec sidekiq" {
				t.Errorf("worker Command = %v, want [bundle exec sidekiq]", w.Command)
			}
		}
		if w.Tier != TierCompose {
			t.Errorf("%s Tier = %s, want compose", w.Name, w.Tier)
		}
	}

	// Managed: exactly 2, sorted.
	if len(r.Managed) != 2 {
		t.Fatalf("Managed count = %d, want 2 (db, cache)", len(r.Managed))
	}
	wantManagedOrder := []string{"cache", "db"}
	for i, want := range wantManagedOrder {
		if r.Managed[i].Name != want {
			t.Errorf("Managed[%d].Name = %q, want %q (sorted by Name)",
				i, r.Managed[i].Name, want)
		}
	}
	if r.Managed[0].Kind != "redis" || r.Managed[0].EnvHint != "REDIS_URL" {
		t.Errorf("cache managed = (kind=%s, hint=%s); want redis/REDIS_URL",
			r.Managed[0].Kind, r.Managed[0].EnvHint)
	}
	if r.Managed[1].Kind != "postgres" || r.Managed[1].EnvHint != "DATABASE_URL" {
		t.Errorf("db managed = (kind=%s, hint=%s); want postgres/DATABASE_URL",
			r.Managed[1].Kind, r.Managed[1].EnvHint)
	}

	// Warnings: none (the fixture is well-formed).
	if len(r.Warnings) != 0 {
		t.Errorf("Warnings = %v, want none", r.Warnings)
	}
}

// TestScan_DeterministicReRuns — repeated Scans yield identical
// Results. This is what makes the Phase 3 confirm table
// reproducible in golden tests.
func TestScan_DeterministicReRuns(t *testing.T) {
	t.Parallel()
	fsys := composeK8sFixture(t)
	r1, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan #1: %v", err)
	}
	r2, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan #2: %v", err)
	}
	if len(r1.Workloads) != len(r2.Workloads) {
		t.Fatalf("workload count drift: %d vs %d", len(r1.Workloads), len(r2.Workloads))
	}
	for i := range r1.Workloads {
		if r1.Workloads[i].Name != r2.Workloads[i].Name {
			t.Errorf("Workloads[%d] name drift: %q vs %q", i,
				r1.Workloads[i].Name, r2.Workloads[i].Name)
		}
	}
	if len(r1.Managed) != len(r2.Managed) {
		t.Fatalf("managed count drift")
	}
	for i := range r1.Managed {
		if r1.Managed[i].Name != r2.Managed[i].Name {
			t.Errorf("Managed[%d] name drift", i)
		}
	}
}

// TestScan_NoComposeFixture — Phase 2 Gate sub-clause "Scanner
// never reads a file outside the tarball root." The Compose+K8s
// fixture lives under k8s/cronjob.yaml — fsys safety requires
// every path fs.ReadFile is asked to read to come from MapFS's
// own validated entries; we never reach for "../".
func TestScan_NoComposeFixture_NoPathsOutsideRoot(t *testing.T) {
	t.Parallel()
	fsys := composeK8sFixture(t)
	// The fixture ONLY registers top-level + k8s/ paths. If Scan()
	// ever asked for "../something" the MapFS would return an
	// ErrInvalid that contains ".."; readFirstValidFile would
	// reject the path earlier as fs.ValidPath failure. Either
	// way the test surfaces an error.
	r, err := Scan(fsys)
	if err != nil {
		t.Errorf("Scan returned err: %v", err)
	}
	// Indirect invariant: every workload's source is a top-level
	// or k8s/ relative path, never one with "../" in it.
	for _, w := range r.Workloads {
		if containsSegment(w.Source, "../") {
			t.Errorf("Workload source contains '..': %q", w.Source)
		}
	}
	for _, m := range r.Managed {
		if containsSegment(m.Source, "../") {
			t.Errorf("Managed source contains '..': %q", m.Source)
		}
	}
}

// TestScan_RootsOnly — a bare fixtures/compose_k8s/ fixture
// with no ./services/. The compose contents are at root and
// k8s contents are at root/k8s/. The fixture's `api` workload
// has build.context=./api; the RootDir MUST normalize to "api"
// (NOT empty root) so merge-by-(RootDir, Name) doesn't confuse
// it with a Tier-4 "app" floor. The `worker` workload has
// build.context=./worker; RootDir="worker". The `nightly`
// CronJob has no build context declaration; its RootDir is "".
func TestScan_RootsOnly(t *testing.T) {
	t.Parallel()
	r, err := Scan(composeK8sFixture(t))
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := map[string]string{
		"api":     "api",
		"worker":  "worker",
		"nightly": "",
	}
	got := map[string]string{}
	for _, w := range r.Workloads {
		got[w.Name] = w.RootDir
	}
	for name, wantRd := range want {
		if gotRD := got[name]; gotRD != wantRd {
			t.Errorf("%s RootDir = %q, want %q", name, gotRD, wantRd)
		}
	}
}

// TestScan_SortedByNameCaseInsensitive — sort.SliceStable in
// scan.go uses strings.ToLower ordering.
func TestScan_SortedByNameCaseInsensitive(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  Zebra:
    build: .
  apple:
    build: .
  Banana:
    build: .
`)},
	}
	r, err := Scan(fsys)
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}
	want := []string{"apple", "Banana", "Zebra"}
	for i, w := range want {
		if r.Workloads[i].Name != w {
			got := make([]string, len(r.Workloads))
			for j, ww := range r.Workloads {
				got[j] = ww.Name
			}
			t.Errorf("Sorted names = %v, want %v", got, want)
			break
		}
	}
	_ = 0 // placeholder; reserved
}

func TestHashWorkloadSourceScopesContentAndBuildMetadata(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{
		"services/api/server.js": &fstest.MapFile{Data: []byte("v1")},
		"README.md":              &fstest.MapFile{Data: []byte("outside-v1")},
	}
	workload := Workload{RootDir: "services/api", Dockerfile: "Dockerfile", Command: []string{"node", "server.js"}}
	base, err := hashWorkloadSource(fsys, workload)
	if err != nil {
		t.Fatal(err)
	}
	fsys["README.md"] = &fstest.MapFile{Data: []byte("outside-v2")}
	outside, err := hashWorkloadSource(fsys, workload)
	if err != nil || outside != base {
		t.Fatalf("outside-root edit changed digest: %q -> %q, %v", base, outside, err)
	}
	fsys["services/api/server.js"] = &fstest.MapFile{Data: []byte("v2")}
	inside, err := hashWorkloadSource(fsys, workload)
	if err != nil || inside == base {
		t.Fatalf("inside-root edit did not change digest: %q -> %q, %v", base, inside, err)
	}
	workload.Dockerfile = "Dockerfile.production"
	metadata, err := hashWorkloadSource(fsys, workload)
	if err != nil || metadata == inside {
		t.Fatalf("Dockerfile selection did not change digest: %q -> %q, %v", inside, metadata, err)
	}

	root := Workload{Name: "app"}
	rootBefore, err := hashWorkloadSource(fsys, root)
	if err != nil {
		t.Fatal(err)
	}
	fsys["README.md"] = &fstest.MapFile{Data: []byte("outside-v3")}
	rootAfter, err := hashWorkloadSource(fsys, root)
	if err != nil || rootAfter == rootBefore {
		t.Fatalf("root workload omitted repository edit: %q -> %q, %v", rootBefore, rootAfter, err)
	}
}

// composeK8sFixture is the canonical §4 fixture. Centralized so
// the reproduce test and the gate test share the same input.
func composeK8sFixture(t *testing.T) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"compose.yaml": &fstest.MapFile{Data: []byte(`services:
  api:
    build:
      context: ./api
      dockerfile: Dockerfile.api
    command:
      - bundle
      - exec
      - rails
      - s
    ports:
      - "8080:80"
    environment:
      RAILS_ENV: production
      DATABASE_URL: postgres://x
  worker:
    build: ./worker
    command: bundle exec sidekiq
    environment:
      REDIS_URL: redis://cache
      LOG_LEVEL: info
  db:
    image: postgres:15-alpine
  cache:
    image: redis:7-alpine
`)},
		"api/Dockerfile.api":  &fstest.MapFile{Data: []byte("FROM scratch\n")},
		"worker/package.json": &fstest.MapFile{Data: []byte(`{"scripts":{"start":"bundle exec sidekiq"}}`)},
		"k8s":                 &fstest.MapFile{Mode: 0o755 | fs.ModeDir},
		"k8s/nightly.cronjob.yaml": &fstest.MapFile{Data: []byte(`apiVersion: batch/v1
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
            - {name: nightly, image: ghcr.io/acme/nightly}
`)},
	}
}

func containsSegment(s, seg string) bool {
	for i := 0; i+len(seg) <= len(s); i++ {
		if s[i:i+len(seg)] == seg {
			return true
		}
	}
	return false
}
