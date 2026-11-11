package apidserver

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/datapipeline"
)

func TestPipelineAlertConfigRefreshReplacesScopeAndFailsClosed(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	current := datapipeline.Config{Enabled: true, Accounts: []string{"old"}}
	var loadErr error
	calls := 0
	cache := pipelineAlertConfigCache{now: func() time.Time { return now }, load: func() (datapipeline.Config, error) { calls++; return current, loadErr }}
	got, err := cache.current()
	if err != nil || got.Accounts[0] != "old" {
		t.Fatal(got, err)
	}
	current.Accounts = []string{"new"}
	if got, _ = cache.current(); got.Accounts[0] != "old" || calls != 1 {
		t.Fatal("cache did not bound reloads", got, calls)
	}
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	got, err = cache.current()
	if err != nil || got.Accounts[0] != "new" {
		t.Fatal("new scope not loaded", got, err)
	}
	loadErr = errors.New("invalid configuration")
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	if _, err = cache.current(); err == nil {
		t.Fatal("failed reload used the old account scope")
	}
	loadErr = nil
	now = now.Add(api.DataPipelineAlertWorkerInterval)
	if _, err = cache.current(); err != nil {
		t.Fatal("config did not recover", err)
	}
}

func TestPipelineAlertConfigRefreshCoalescesConcurrentReads(t *testing.T) {
	now := time.Now()
	calls := 0
	cache := pipelineAlertConfigCache{now: func() time.Time { return now }, load: func() (datapipeline.Config, error) {
		calls++
		return datapipeline.Config{Accounts: []string{"account"}}, nil
	}}
	var group sync.WaitGroup
	for range 32 {
		group.Go(func() {
			if value, err := cache.current(); err != nil || len(value.Accounts) != 1 {
				t.Errorf("config refresh: %+v, %v", value, err)
			}
		})
	}
	group.Wait()
	if calls != 1 {
		t.Fatalf("concurrent queues loaded the config %d times", calls)
	}
}
