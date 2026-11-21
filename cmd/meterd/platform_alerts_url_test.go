package main

import "testing"

func TestDashboardBaseURL(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	if got := dashboardBaseURL(env(map[string]string{"FAAS_PLATFORM_DOMAIN": "gregale.dev"}), "gregale.app"); got != "https://gregale.dev" {
		t.Fatalf("platform domain: got %q", got)
	}
	if got := dashboardBaseURL(env(nil), "gregale.app"); got != "https://gregale.app" {
		t.Fatalf("shared layout: got %q", got)
	}
	if got := dashboardBaseURL(env(nil), ""); got != "" {
		t.Fatalf("no domain: got %q", got)
	}
}
