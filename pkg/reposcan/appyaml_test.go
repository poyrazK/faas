package reposcan

import (
	"testing"
	"testing/fstest"
)

func TestDetectAppYaml_ServicesWorkersAndJobs(t *testing.T) {
	t.Parallel()
	body := `services:
  api:
    command:
      - bundle
      - exec
      - rails
      - s
    env:
      RAILS_ENV: production
    ports:
      - 8080
workers:
  indexer:
    command: bundle exec sidekiq
jobs:
  - name: hourly
    schedule: "0 * * * *"
  - name: nightly
    schedule: "0 3 * * *"
`
	fsys := fstest.MapFS{
		"app.yaml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectAppYaml(fsys)
	if err != nil {
		t.Fatalf("detectAppYaml: %v", err)
	}
	byName := map[string]workloadSeed{}
	for _, s := range seeds {
		byName[s.name] = s
	}
	if s := byName["api"]; s.class != ClassHTTP || len(s.command) != 4 || s.ports[0] != 8080 || s.commandShell {
		t.Errorf("api seed wrong: cls=%s, command=%v, ports=%v", s.class, s.command, s.ports)
	}
	if s := byName["indexer"]; s.class != ClassWorker || !s.commandShell {
		t.Errorf("indexer class/shell = %q/%v, want worker/true", s.class, s.commandShell)
	}
	for _, n := range []string{"hourly", "nightly"} {
		s, ok := byName[n]
		if !ok || s.class != ClassJob || s.schedule == "" {
			t.Errorf("job %q wrong: %v %s %q", n, ok, s.class, s.schedule)
		}
	}
}

func TestDetectAppYaml_AbsentFile(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")}}
	seeds, _, _, err := detectAppYaml(fsys)
	if err != nil {
		t.Fatalf("detectAppYaml: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty", names(seeds))
	}
}
