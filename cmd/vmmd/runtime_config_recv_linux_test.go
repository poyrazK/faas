// adr: 045
// issue: 1278
package main

import (
	"context"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeConfigStoreStub struct {
	rows []state.AppEnv
}

func (s runtimeConfigStoreStub) ListAppEnv(context.Context, string, string) ([]state.AppEnv, error) {
	return s.rows, nil
}

func TestLoadRuntimeConfigReturnsDefaultScopeAndRevision(t *testing.T) {
	updated := time.Date(2026, time.September, 18, 20, 0, 0, 123, time.UTC)
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{rows: []state.AppEnv{
		{Scope: "default", Key: "FEATURE_FLAG", Value: "on", UpdatedAt: updated},
		{Scope: "staging", Key: "FEATURE_FLAG", Value: "off", UpdatedAt: updated.Add(time.Hour)},
	}}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env["FEATURE_FLAG"] != "on" {
		t.Fatalf("env = %+v, want default scope value", response.Env)
	}
	if response.Revision != updated.Format(time.RFC3339Nano) {
		t.Fatalf("revision = %q, want %q", response.Revision, updated.Format(time.RFC3339Nano))
	}
}

func TestLoadRuntimeConfigReturnsEmptyMapWithoutRows(t *testing.T) {
	response, err := loadRuntimeConfig(context.Background(), runtimeConfigStoreStub{}, "acct-1", "app-1")
	if err != nil {
		t.Fatal(err)
	}
	if response.Env == nil || len(response.Env) != 0 {
		t.Fatalf("env = %#v, want an empty map", response.Env)
	}
	if response.Revision != "" {
		t.Fatalf("revision = %q, want empty", response.Revision)
	}
}
