package reposcan

import (
	"testing"
	"testing/fstest"
)

func TestDetectFly_AppAndProcesses(t *testing.T) {
	t.Parallel()
	body := `app = "my-fly-app"

[processes]
web = "node server.js"
worker = "node worker.js"
empty = ""

[http_service]
processes = ["web"]
`
	fsys := fstest.MapFS{
		"fly.toml": &fstest.MapFile{Data: []byte(body)},
	}
	seeds, _, _, err := detectFly(fsys)
	if err != nil {
		t.Fatalf("detectFly: %v", err)
	}
	byName := map[string]workloadSeed{}
	for _, s := range seeds {
		byName[s.name] = s
	}
	app, ok := byName["my-fly-app"]
	if !ok || app.class != ClassHTTP {
		t.Errorf("app seed = (%v, %s); want my-fly-app/http", ok, app.class)
	}
	if worker, ok := byName["worker"]; !ok || worker.class != ClassWorker || len(worker.command) != 1 || worker.command[0] != "node worker.js" {
		t.Errorf("worker process missing; seeds = %v", names(seeds))
	}
	if web, ok := byName["web"]; !ok || web.class != ClassHTTP || len(web.command) != 1 || web.command[0] != "node server.js" {
		t.Errorf("web process = %#v, want HTTP command", web)
	}
	if empty, ok := byName["empty"]; !ok || empty.class != ClassWorker || len(empty.command) != 0 {
		t.Errorf("empty process = %#v, want retained worker with no command", empty)
	}
}

func TestDetectFly_AbsentFile(t *testing.T) {
	t.Parallel()
	fsys := fstest.MapFS{"Dockerfile": &fstest.MapFile{Data: []byte("FROM scratch")}}
	seeds, _, _, err := detectFly(fsys)
	if err != nil {
		t.Fatalf("detectFly: %v", err)
	}
	if len(seeds) != 0 {
		t.Errorf("seeds = %v, want empty", names(seeds))
	}
}
