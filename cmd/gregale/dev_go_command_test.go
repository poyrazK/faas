package main_test

import (
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/cmd/gregale"
)

func writeGoModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// ADR-20261026120412: `gregale dev --all` runs each command of a Go module
// as its own developer loop, building that command from the module root.
func TestDevAllRunsGoModuleCommands(t *testing.T) {
	root := writeGoModule(t, map[string]string{
		"go.mod":             "module example.com/shop\n\ngo 1.24\n",
		"cmd/api/main.go":    "package main\n\nfunc main() {}\n",
		"cmd/worker/main.go": "package main\n\nfunc main() {}\n",
		"internal/db/db.go":  "package db\n",
	})
	sources, err := main.DiscoverProjectSources(root)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, source := range sources {
		got = append(got, source.Name+"="+source.RootDir+"@"+source.GoCommand)
	}
	if want := []string{"api=.@api", "worker=.@worker"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("sources = %v, want %v", got, want)
	}

	apps, err := main.DiscoverDevAllApps(root, main.DiscoverProjectSources, func(string) bool { return true })
	if err != nil {
		t.Fatal(err)
	}
	if len(apps) != 2 || apps[0].Project != "api" || apps[1].Project != "worker" {
		t.Fatalf("apps = %+v", apps)
	}
	args := main.DevAllChildArgs(apps[1], false)
	if i := slices.Index(args, "--go-command"); i < 0 || args[i+1] != "worker" || args[2] != root {
		t.Fatalf("child args = %v", args)
	}
	if stop := main.DevAllChildArgs(apps[1], true); slices.Contains(stop, "--go-command") {
		t.Fatalf("stop args = %v", stop)
	}
	if label := main.DevAllSourceLabel(apps[1]); label != "cmd/worker" {
		t.Fatalf("label = %q", label)
	}
}

func TestCheckDevGoCommand(t *testing.T) {
	root := writeGoModule(t, map[string]string{
		"go.mod":          "module example.com/shop\n\ngo 1.24\n",
		"cmd/api/main.go": "package main\n\nfunc main() {}\n",
		"cmd/lib/lib.go":  "package lib\n",
	})
	if err := main.CheckDevGoCommand(root, false, "api"); err != nil {
		t.Fatalf("cmd/api: %v", err)
	}
	if err := main.CheckDevGoCommand(root, false, ""); err != nil {
		t.Fatalf("no command: %v", err)
	}
	for _, command := range []string{"lib", "missing"} {
		if err := main.CheckDevGoCommand(root, false, command); err == nil || !strings.Contains(err.Error(), "no main package") {
			t.Fatalf("cmd/%s: %v", command, err)
		}
	}
	if err := main.CheckDevGoCommand(root, true, "api"); err == nil {
		t.Fatal("a function accepted a Go command")
	}
	if req := main.DevGoCommandRequest("api"); req.GoCommand != "api" {
		t.Fatalf("request go command = %q", req.GoCommand)
	}
}
