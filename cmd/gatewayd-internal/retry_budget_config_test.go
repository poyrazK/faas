package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRetryBudgetRedisURL(t *testing.T) {
	path := filepath.Join(t.TempDir(), "redis-url")
	if err := os.WriteFile(path, []byte("rediss://redis.example.test:6380/0\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	lookup := func(values map[string]string) func(string) string {
		return func(key string) string { return values[key] }
	}
	if got, err := retryBudgetRedisURL(lookup(nil)); err != nil || got != "" {
		t.Fatalf("unset = %q, %v", got, err)
	}
	if got, err := retryBudgetRedisURL(lookup(map[string]string{"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE": path})); err != nil || got != "rediss://redis.example.test:6380/0" {
		t.Fatalf("credential = %q, %v", got, err)
	}
	if _, err := retryBudgetRedisURL(lookup(map[string]string{
		"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL":      "redis://old.example.test",
		"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE": path,
	})); err == nil {
		t.Fatal("conflicting URL sources accepted")
	}
	if _, err := retryBudgetRedisURL(lookup(map[string]string{"FAAS_GATEWAY_RETRY_BUDGET_REDIS_URL_FILE": path + ".missing"})); err == nil || strings.Contains(err.Error(), "rediss://") {
		t.Fatalf("missing credential error = %v", err)
	}
}
