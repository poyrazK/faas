package main_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	main "github.com/onebox-faas/faas/cmd/gregale"
)

func TestDataAPISyncInitGeneratesRecoverableWorkflowWithoutOverwriting(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "data-api.json")
	if code := main.Run([]string{"data-api", "sync", "notes-data", "--init", "--role", "notes_api", "--config", path}); code != 0 {
		t.Fatalf("init exit %d", code)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Migrate      struct{ Command []string }
		Coordination struct {
			Baseline      string
			Pause, Resume struct{ Command []string }
			Authorization struct{ Directory string }
		}
	}
	if err = json.Unmarshal(raw, &config); err != nil {
		t.Fatal(err)
	}
	if config.Coordination.Baseline != "schema.before.json" || config.Coordination.Authorization.Directory != "client" || !strings.Contains(strings.Join(config.Coordination.Pause.Command, " "), "pause notes_api") || !strings.Contains(strings.Join(config.Coordination.Resume.Command, " "), "resume notes_api") {
		t.Fatalf("missing safe workflow: %s", raw)
	}
	if code := main.Run([]string{"data-api", "sync", "notes-data", "--init", "--role", "notes_api", "--config", path}); code == 0 {
		t.Fatal("overwrote existing workflow")
	}
	after, _ := os.ReadFile(path)
	if string(after) != string(raw) {
		t.Fatal("existing workflow changed")
	}
}

func TestDataAPIReportingCannotInvalidateAnEnabledSharedBudget(t *testing.T) {
	var reads, writes atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writes.Add(1)
			w.WriteHeader(204)
			return
		}
		reads.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"secrets":[{"key":"DATA_API_SHARED_BUDGET","scope":"default"}],"count":1}`))
	}))
	defer server.Close()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("FAAS_API", server.URL)
	t.Setenv("FAAS_TOKEN", "test-token")
	if code := main.Run([]string{"data-api", "configure", "notes-data", "--usage-reporting", "off", "--no-restart"}); code == 0 {
		t.Fatal("reporting removal must preserve the shared budget's dependency")
	}
	if reads.Load() != 1 || writes.Load() != 0 {
		t.Fatalf("reads=%d writes=%d; configuration should remain unchanged", reads.Load(), writes.Load())
	}
}

func TestOnboardingRejectsFloatingSDKVersionsBeforeCreatingProject(t *testing.T) {
	for _, version := range []string{"latest", "^0.1.0", "0.1", "file:./other", "$(touch unexpected)"} {
		path := filepath.Join(t.TempDir(), "project")
		if code := main.Run([]string{"data-api", "onboard", "--path", path, "--sdk-version", version}); code == 0 {
			t.Fatalf("accepted %q", version)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("invalid input created a project")
		}
	}
}
