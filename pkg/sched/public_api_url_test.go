// adr: 20261018101500 — amendment 2026-10-31: workloads learn the installation's API URL.
package sched_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/sched"
)

func TestEngineHandsWorkloadsThePublicAPIURL(t *testing.T) {
	base := []fcvm.APIEnvEntry{{Key: api.PlatformAppIDEnv, Value: "app"}}
	if got := new(sched.Engine).XWithPublicAPIURL(base); len(got) != 1 {
		t.Fatalf("unset URL added %v", got)
	}
	engine := new(sched.Engine).WithPublicAPIURL(" https://api.example.test/ ")
	got := engine.XWithPublicAPIURL(base)
	if len(got) != 2 || got[1] != (fcvm.APIEnvEntry{Key: api.PublicAPIURLEnv, Value: "https://api.example.test"}) {
		t.Fatalf("env = %v, want GREGALE_API_URL appended", got)
	}
	own := append(base, fcvm.APIEnvEntry{Key: api.PublicAPIURLEnv, Value: "https://mine.example"})
	if got := engine.XWithPublicAPIURL(own); len(got) != 2 || got[1].Value != "https://mine.example" {
		t.Fatalf("app's own GREGALE_API_URL replaced: %v", got)
	}
}
