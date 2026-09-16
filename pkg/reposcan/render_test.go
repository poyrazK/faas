package reposcan

import (
	"sort"
	"testing"
	"testing/fstest"
)

func TestDetectRender_WebWorkerAndCronJobs(t *testing.T) {
	t.Parallel()
	body := `services:
  - name: api
    type: web
  - name: indexer
    type: worker
  - name: night-batch
    type: worker
cronJobs:
  - name: hourly
    schedule: "0 * * * *"
  - name: nightly
    schedule: "0 3 * * *"
`
	fsys := fstest.MapFS{
		"render.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectRender(fsys)
	if err != nil {
		t.Fatalf("detectRender: %v", err)
	}
	byName := map[string]workloadSeed{}
	for _, s := range seeds {
		byName[s.name] = s
	}
	want := map[string]Class{
		"api":         ClassHTTP,
		"indexer":     ClassWorker,
		"night-batch": ClassWorker,
		"hourly":      ClassJob,
		"nightly":     ClassJob,
	}
	if len(byName) != len(want) {
		got := make([]string, 0, len(byName))
		for k := range byName {
			got = append(got, k)
		}
		sort.Strings(got)
		t.Errorf("seed count = %d (%v), want %d (%v)", len(byName), got, len(want), keysSorted(want))
	}
	for n, wantClass := range want {
		s, ok := byName[n]
		if !ok {
			t.Errorf("seed %q missing", n)
			continue
		}
		if s.class != wantClass {
			t.Errorf("seed %q class = %q, want %q", n, s.class, wantClass)
		}
	}
	if byName["hourly"].schedule != "0 * * * *" {
		t.Errorf("hourly schedule = %q", byName["hourly"].schedule)
	}
}

func TestDetectRender_AbsentFile(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")}}
	seeds, _, _, err := detectRender(fsys)
	if err != nil {
		t.Fatalf("detectRender: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty", names(seeds))
	}
}

func TestDetectRender_CurrentBlueprintSchema(t *testing.T) {
	t.Parallel()
	body := `services:
  - type: web
    name: api
    runtime: node
    startCommand: node handler.js
    envVars:
      - key: NODE_ENV
        value: production
  - type: worker
    name: jobs
    startCommand: node worker.js
  - type: cron
    name: cleanup
    schedule: "0 * * * *"
    startCommand: node cleanup.js
  - type: pserv
    name: internal
    startCommand: node internal.js
  - type: keyvalue
    name: cache
databases:
  - name: app-db
`
	seeds, managed, warnings, err := detectRender(fstest.MapFS{
		"render.yaml": &fstest.MapFile{Data: []byte(body)},
	})
	if err != nil || len(warnings) != 0 {
		t.Fatalf("detectRender: err=%v warnings=%v", err, warnings)
	}
	byName := map[string]workloadSeed{}
	for _, seed := range seeds {
		byName[seed.name] = seed
	}
	if got := byName["api"]; got.class != ClassHTTP || len(got.command) != 1 || got.command[0] != "node handler.js" || len(got.envKeys) != 1 || got.envKeys[0] != "NODE_ENV" {
		t.Errorf("api = %#v", got)
	}
	if got := byName["jobs"]; got.class != ClassWorker || len(got.command) != 1 {
		t.Errorf("jobs = %#v", got)
	}
	if got := byName["cleanup"]; got.class != ClassJob || got.schedule != "0 * * * *" || len(got.command) != 1 {
		t.Errorf("cleanup = %#v", got)
	}
	if got := byName["internal"]; got.class != ClassServer {
		t.Errorf("internal = %#v, want private server classification", got)
	}
	if len(managed) != 2 || managed[0].Name != "app-db" || managed[1].Name != "cache" {
		t.Errorf("managed = %#v", managed)
	}
}

func keysSorted(m map[string]Class) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
