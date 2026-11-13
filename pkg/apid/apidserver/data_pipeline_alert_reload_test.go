package apidserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestPipelineAlertConfigReloadsFileAndStopsWhenDisabled(t *testing.T) {
	t.Setenv("FAAS_ENVIRONMENT", "staging")
	t.Setenv("FAAS_APID_ROLE", "control-plane")
	path := filepath.Join(t.TempDir(), "apid.toml")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("[data_pipelines]\nenabled=true\naccounts=['aa000000-0000-4000-8000-000000000001']\n")
	now := time.Now()
	cache := pipelineAlertConfigCache{now: func() time.Time { return now }, load: func() (datapipeline.Config, error) { return loadPipelineAlertConfig(path) }}
	first, err := cache.current()
	if err != nil || len(first.Accounts) != 1 || first.Accounts[0] != "aa000000-0000-4000-8000-000000000001" {
		t.Fatal(first, err)
	}
	write("[data_pipelines]\nenabled=true\naccounts=['bb000000-0000-4000-8000-000000000002']\n")
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	second, err := cache.current()
	if err != nil || len(second.Accounts) != 1 || second.Accounts[0] != "bb000000-0000-4000-8000-000000000002" {
		t.Fatal(second, err)
	}
	write("[data_pipelines]\nenabled=false\n")
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	if _, err = cache.current(); err == nil {
		t.Fatal("disabled config kept the previous scope")
	}
	write("[data_pipelines\n")
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	if _, err = cache.current(); err == nil {
		t.Fatal("malformed config kept the previous scope")
	}
}
