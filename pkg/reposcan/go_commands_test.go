package reposcan

import (
	"reflect"
	"testing"
	"testing/fstest"
)

func goModuleFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

const goMain = "// Command x.\npackage main\n\nfunc main() {}\n"

// ADR-20261025134512: each cmd/ main package of a single Go module with no
// command at its root becomes a service built from the module root.
func TestScanGoModuleCommands(t *testing.T) {
	type got struct{ Name, RootDir, GoCommand string }
	for _, tc := range []struct {
		name  string
		files map[string]string
		want  []got
	}{
		{"several commands", map[string]string{
			"go.mod": "module example.com/shop\n", "internal/db/db.go": "package db\n",
			"cmd/api/main.go": goMain, "cmd/worker/main.go": goMain, "cmd/worker/main_test.go": "package main\n",
			"cmd/tools/README.md": "not a command\n", "cmd/lib/lib.go": "package lib\n",
		}, []got{{"api", ".", "api"}, {"worker", ".", "worker"}}},
		// Railpack already builds a lone command or a root main package; those
		// keep the one app (and its name) they deploy as today.
		{"one command", map[string]string{"go.mod": "module example.com/shop\n", "cmd/api/main.go": goMain},
			[]got{{"", "", ""}}},
		{"main package at the root", map[string]string{
			"go.mod": "module example.com/shop\n", "main.go": goMain, "cmd/api/main.go": goMain, "cmd/worker/main.go": goMain,
		}, []got{{"", "", ""}}},
		// A root Dockerfile decides the build for the whole module.
		{"root Dockerfile", map[string]string{
			"go.mod": "module example.com/shop\n", "Dockerfile": "FROM scratch\n", "cmd/api/main.go": goMain, "cmd/worker/main.go": goMain,
		}, []got{{"", "", ""}}},
		// A command with its own module is a convention member already.
		{"command with its own module", map[string]string{
			"go.mod": "module example.com/shop\n", "cmd/api/main.go": goMain,
			"cmd/worker/go.mod": "module example.com/worker\n", "cmd/worker/main.go": goMain,
		}, []got{{"worker", "cmd/worker", ""}}},
		{"workspace module", map[string]string{
			"go.mod": "module example.com/shop\n", "go.work": "go 1.24\n", "cmd/api/main.go": goMain, "cmd/worker/main.go": goMain,
		}, []got{{"", "", ""}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, err := Scan(goModuleFS(tc.files))
			if err != nil {
				t.Fatal(err)
			}
			var workloads []got
			for _, w := range result.Workloads {
				name := w.Name
				if w.RootDir == "" {
					name = "" // the root floor app, named after the repository
				}
				workloads = append(workloads, got{name, w.RootDir, w.GoCommand})
			}
			if !reflect.DeepEqual(workloads, tc.want) {
				t.Fatalf("workloads = %+v, want %+v", workloads, tc.want)
			}
		})
	}
}

// A Compose service that builds a command with its own Dockerfile keeps the
// name; the module command of the same name is not added beside it.
func TestScanGoModuleCommandsYieldToDeclaredServices(t *testing.T) {
	result, err := Scan(goModuleFS(map[string]string{
		"go.mod": "module example.com/shop\n", "cmd/api/main.go": goMain, "cmd/worker/main.go": goMain,
		"compose.yaml":   "services:\n  api:\n    build:\n      context: .\n      dockerfile: api.Dockerfile\n",
		"api.Dockerfile": "FROM scratch\n",
	}))
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]Workload{}
	for _, w := range result.Workloads {
		if _, dup := byName[w.Name]; dup {
			t.Fatalf("two workloads named %q: %+v", w.Name, result.Workloads)
		}
		byName[w.Name] = w
	}
	if api := byName["api"]; api.Dockerfile == "" || api.GoCommand != "" {
		t.Fatalf("api = %+v, want the Compose Dockerfile build", api)
	}
	if worker := byName["worker"]; worker.GoCommand != "worker" || worker.RootDir != "." {
		t.Fatalf("worker = %+v, want cmd/worker from the module root", worker)
	}
	if plan := PlanWorkload(byName["worker"]); plan.GoCommand != "worker" {
		t.Fatalf("plan go_command = %q", plan.GoCommand)
	}
}
