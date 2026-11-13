package journeys

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The built CLI runs against a local metadata endpoint so its file behavior
// can be checked without changing any deployed account configuration.
func TestEnvPullPreview(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/apps/demo/secrets" || r.URL.Query().Get("scope") != "staging" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"count": 8, "quota": 25, "secrets": []map[string]string{{"key": "KEEP"}, {"key": "NEW_KEY"}},
		}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, appsDomain: configured.appsDomain, home: t.TempDir()}
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	original := "# keep this comment\r\nexport KEEP=private-local-value\r\nLOCAL_ONLY=private-local-only"
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	var previousChanges []struct {
		Key    string `json:"key"`
		Action string `json:"action"`
	}
	for _, step := range []string{"preview", "apply", "noop"} {
		t.Run(step, func(t *testing.T) {
			args := []string{"env", "pull", "--app", "demo", "--env", "staging", "-o", path, "--json"}
			if step == "preview" {
				args = append(args, "--dry-run")
			}
			res := tg.execIn(t, "fp_live_fixture", dir, "", time.Minute, args...)
			if res.exit != 0 {
				t.Fatalf("env pull failed: %s", res.stderr)
			}
			var receipt struct {
				DryRun        bool `json:"dry_run"`
				Written       bool `json:"written"`
				WriteRequired bool `json:"write_required"`
				Changes       []struct {
					Key    string `json:"key"`
					Action string `json:"action"`
				} `json:"changes"`
			}
			if err := json.Unmarshal(res.stdout, &receipt); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(string(res.stdout)+string(res.stderr), "private-local") {
				t.Fatal("CLI disclosed local values")
			}
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if step == "preview" {
				if !receipt.DryRun || receipt.Written || !receipt.WriteRequired || string(data) != original || !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("preview changed the local file or reported a write")
				}
				previousChanges = receipt.Changes
			} else {
				if string(data) != original+"\r\nNEW_KEY=\r\n" || receipt.DryRun || receipt.Written != (step == "apply") || receipt.WriteRequired != (step == "apply") {
					t.Fatal("apply/no-op disagreed with the planned file content")
				}
				if step == "apply" {
					if len(previousChanges) != len(receipt.Changes) {
						t.Fatal("preview and apply returned different key decisions")
					}
					for i := range previousChanges {
						if previousChanges[i] != receipt.Changes[i] {
							t.Fatal("preview and apply returned different key decisions")
						}
					}
				} else if !os.SameFile(before, after) || !before.ModTime().Equal(after.ModTime()) {
					t.Fatal("no-op pull rewrote the file")
				}
			}
			before = after
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 1 {
				t.Fatal("env pull left temporary output files")
			}
		})
	}
	if calls.Load() != 3 {
		t.Fatalf("unexpected remote request count: %d", calls.Load())
	}
}
