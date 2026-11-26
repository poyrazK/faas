package main_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

// #2669: packaging ships a git archive, which contains only tracked files, so
// a .gitignore'd path is never deployed. doctor walks the checkout instead and
// reported listeners, binaries and env references from those ignored paths as
// blocking findings. The two must agree on the file set.
func TestDoctorSkipsGitignoredSources(t *testing.T) {
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":     "module example.com/demo\n\ngo 1.26\n",
		"main.go":    "package main\n\nimport \"net/http\"\n\nfunc main() {\n\t_ = http.ListenAndServe(\"0.0.0.0:8080\", nil)\n}\n",
		".gitignore": "ignored/\n",
		// A loopback-only listener that is not part of the shipped source.
		"ignored/nested/bad.go": "package nested\n\nimport \"net/http\"\n\nfunc bad() {\n\t_ = http.ListenAndServe(\"127.0.0.1:9999\", nil)\n}\n",
	}
	for name, body := range files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	main.WithResetJSONOutput(t, true)
	stdout, _, restore := main.SwapIO(t)
	main.CmdDoctor([]string{"--path", dir})
	restore()

	var report struct {
		Checks []struct {
			Name    string   `json:"name"`
			Status  string   `json:"status"`
			Sources []string `json:"sources"`
		} `json:"checks"`
	}
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, stdout.String())
	}
	for _, check := range report.Checks {
		if check.Name != "loopback-bind" {
			continue
		}
		if check.Status != "ok" {
			t.Errorf("loopback-bind status = %q, want ok; sources = %v", check.Status, check.Sources)
		}
		for _, source := range check.Sources {
			if strings.Contains(source, "ignored/") {
				t.Errorf("doctor reported a .gitignore'd source: %s", source)
			}
		}
		return
	}
	t.Fatalf("doctor JSON has no loopback-bind check: %s", stdout.String())
}
