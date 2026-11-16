package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// GA row156: locate a broken input file before submission, then inspect
// offline help through the returned argv from the same working directory.
func TestJSONInputLocationsIsolatedCLI(t *testing.T) {
	cli := os.Getenv("GREGALE_JOURNEY_TEST_CLI")
	if tgt != nil {
		cli = tgt.cli
	}
	if cli == "" {
		t.Skip("set GREGALE_JOURNEY_TEST_CLI or configure a journey target")
	}
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { requests.Add(1); w.WriteHeader(500) }))
	t.Cleanup(server.Close)
	tg := &target{api: server.URL, cli: cli, home: t.TempDir(), appsDomain: "apps.example.test"}
	dir := t.TempDir()
	path := filepath.Join(dir, "request.json")
	if err := os.WriteFile(path, []byte("{\r\n\t\"private\": }"), 0o600); err != nil {
		t.Fatal(err)
	}
	result := tg.exec(t, "fp_test_json-input-journey", dir, time.Minute, "invocation", "run", "demo", "--payload", "@request.json")
	var p struct {
		Code     string
		ExitCode int `json:"exit_code"`
		CLI      struct {
			Kind, Flag string
			Location   struct {
				Source, Path string
				Line, Column int
			}
			HelpArgv []string `json:"help_argv"`
		}
	}
	if result.exit != 1 || len(result.stdout) != 0 || json.Unmarshal(result.stderr, &p) != nil || p.Code != "invalid_request" || p.ExitCode != 1 || p.CLI.Kind != "invalid_json" || p.CLI.Flag != "--payload" || p.CLI.Location.Source != "file" || p.CLI.Location.Path != "request.json" || p.CLI.Location.Line != 2 || p.CLI.Location.Column != 13 {
		t.Fatalf("input location: exit=%d stdout=%s stderr=%s", result.exit, result.stdout, result.stderr)
	}
	if len(p.CLI.HelpArgv) < 3 || p.CLI.HelpArgv[0] != "gregale" {
		t.Fatalf("invalid help argv: %q", p.CLI.HelpArgv)
	}
	help := tg.exec(t, "fp_test_json-input-journey", dir, time.Minute, p.CLI.HelpArgv[1:]...)
	var document struct {
		SchemaVersion int `json:"schema_version"`
	}
	if help.exit != 0 || len(help.stderr) != 0 || json.Unmarshal(help.stdout, &document) != nil || document.SchemaVersion != 1 {
		t.Fatalf("input help: exit=%d stdout=%s stderr=%s", help.exit, help.stdout, help.stderr)
	}
	if requests.Load() != 0 {
		t.Fatalf("invalid JSON or help made %d requests", requests.Load())
	}
}
