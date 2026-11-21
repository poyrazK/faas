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

// Payload inspection is an offline CLI journey: even a configured API and an
// account's saved login must not be needed to explain a local source archive.
func TestDeploySourceFiles(t *testing.T) {
	configured := needs(t, tierSmoke, false)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	tg := &target{api: server.URL, cli: configured.cli, appsDomain: configured.appsDomain, home: t.TempDir()}
	dir := t.TempDir()
	for name, body := range map[string]string{
		"package.json": "{}", "server.js": "source-body-never-printed",
		".gregaleignore": "private.txt\n", "private.txt": "private-body-never-printed",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	s := newStory(t)
	s.check("inspect_without_login", func(t *testing.T) {
		result := tg.execIn(t, "", dir, "", time.Minute, "deploy", "--dry-run", "--explain-files", "--worktree", "--json")
		if result.exit != 0 {
			t.Fatalf("file inspection: %s", result.stderr)
		}
		var report struct {
			SourceMode          string `json:"source_mode"`
			AccountLimitChecked bool   `json:"account_limit_checked"`
			Files               []struct {
				Path     string `json:"path"`
				Included bool   `json:"included"`
				Bytes    int64  `json:"bytes"`
				RuleFile string `json:"rule_file"`
				RuleLine int    `json:"rule_line"`
			} `json:"files"`
		}
		if err := json.Unmarshal(result.stdout, &report); err != nil {
			t.Fatal(err)
		}
		included, excluded := false, false
		for _, row := range report.Files {
			included = included || row.Path == "server.js" && row.Included && row.Bytes == int64(len("source-body-never-printed"))
			excluded = excluded || row.Path == "private.txt" && !row.Included && row.RuleFile == ".gregaleignore" && row.RuleLine == 1
		}
		if !included || !excluded || report.SourceMode != "worktree" || report.AccountLimitChecked {
			t.Fatalf("unexpected payload explanation: %s", result.stdout)
		}
		if strings.Contains(string(result.stdout)+string(result.stderr), "body-never-printed") {
			t.Fatal("inspection disclosed file contents")
		}
	})
	s.check("text_output", func(t *testing.T) {
		result := tg.execInEnv(t, "", dir, "", []string{"FAAS_JSON=0"}, time.Minute, "deploy", "--dry-run", "--explain-files", "--worktree")
		if result.exit != 0 || !strings.Contains(string(result.stdout), "exclude") || !strings.Contains(string(result.stdout), ".gregaleignore:1") {
			t.Fatalf("text explanation: %s\n%s", result.stdout, result.stderr)
		}
	})
	if calls.Load() != 0 {
		t.Fatalf("offline inspection made %d API calls", calls.Load())
	}
}
