package reposcan_test

import (
	"testing"
	"testing/fstest"

	"github.com/onebox-faas/faas/pkg/reposcan"
)

func TestReadComposeEnv(t *testing.T) {
	fsys := fstest.MapFS{
		"compose.yaml": {Data: []byte(`x-gregale-env-import: true
services:
  api:
    build: ./api
    environment:
      LOG_LEVEL: info
      DEBUG: true
      FROM_HOST:
  worker:
    build: ./worker
    environment:
      - QUEUE=jobs
      - FROM_HOST
  debug:
    build: ./debug
    profiles: [debug]
    environment:
      ONLY_IN_PROFILE: "1"
`)},
		"compose.override.yaml": {Data: []byte(`services:
  api:
    environment:
      LOG_LEVEL: debug
`)},
	}
	env, err := reposcan.ReadComposeEnv(fsys)
	if err != nil {
		t.Fatal(err)
	}
	if !env.Import {
		t.Fatal("top-level opt-in not read")
	}
	api := env.Services["api"]
	if *api["LOG_LEVEL"] != "debug" || *api["DEBUG"] != "true" || api["FROM_HOST"] != nil {
		t.Fatalf("api environment = %v, want the override applied and source text kept", deref(api))
	}
	worker := env.Services["worker"]
	if *worker["QUEUE"] != "jobs" || worker["FROM_HOST"] != nil {
		t.Fatalf("worker environment = %v", deref(worker))
	}
	if _, ok := env.Services["debug"]; ok {
		t.Fatal("a service behind an inactive profile was read")
	}
	// COMPOSE_PROFILES in the root .env activates it, as it does for Scan.
	fsys[".env"] = &fstest.MapFile{Data: []byte("COMPOSE_PROFILES=debug\n")}
	if active, err := reposcan.ReadComposeEnv(fsys); err != nil || active.Services["debug"] == nil || *active.Services["debug"]["ONLY_IN_PROFILE"] != "1" {
		t.Fatalf("active profile environment = %+v, %v", active.Services["debug"], err)
	}

	// Without a merge the scalar's source text is kept.
	plain, err := reposcan.ReadComposeEnv(fstest.MapFS{"compose.yaml": {Data: []byte("services:\n  api:\n    build: .\n    environment:\n      RATIO: 1.0\n")}})
	if err != nil || plain.Import || *plain.Services["api"]["RATIO"] != "1.0" {
		t.Fatalf("unmerged environment = %+v, %v", plain, err)
	}

	none, err := reposcan.ReadComposeEnv(fstest.MapFS{"package.json": {Data: []byte("{}")}})
	if err != nil || none.Import || len(none.Services) != 0 {
		t.Fatalf("no Compose file = %+v, %v", none, err)
	}
}

func deref(entries map[string]*string) map[string]string {
	out := make(map[string]string, len(entries))
	for key, value := range entries {
		if value == nil {
			out[key] = "<host>"
			continue
		}
		out[key] = *value
	}
	return out
}
