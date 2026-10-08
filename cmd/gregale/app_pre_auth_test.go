package main

import (
	"reflect"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
)

func TestCLIPreAuthPatch(t *testing.T) {
	stored := &api.PreAuthRateLimitConfig{
		Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 10, Burst: 20,
		Routes: []api.PreAuthRouteLimit{{Method: "POST", Path: "/login", RequestsPerSecond: 1, Burst: 2}},
	}

	got, err := cliPreAuthPatch(stored, api.PreAuthRateLimitEnforce, 0, 0, true, false, false)
	want := &api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 10, Burst: 20, Routes: stored.Routes}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("enforce keeps rates and routes: got %+v, %v", got, err)
	}
	got.Routes[0].Path = "/changed"
	if stored.Routes[0].Path != "/login" {
		t.Fatal("patch aliased the stored route slice")
	}

	if got, err := cliPreAuthPatch(stored, "", 4, 8, false, true, true); err != nil || got.Mode != api.PreAuthRateLimitObserve || got.RequestsPerSecond != 4 || got.Burst != 8 {
		t.Fatalf("rate-only change = %+v, %v", got, err)
	}
	if got, err := cliPreAuthPatch(nil, api.PreAuthRateLimitObserve, 5, 10, true, true, true); err != nil || got.RequestsPerSecond != 5 {
		t.Fatalf("new config = %+v, %v", got, err)
	}
	if got, err := cliPreAuthPatch(nil, api.PreAuthRateLimitOff, 0, 0, true, false, false); err != nil || got.Mode != api.PreAuthRateLimitOff {
		t.Fatalf("off without rates = %+v, %v", got, err)
	}

	for name, call := range map[string]func() (*api.PreAuthRateLimitConfig, error){
		"unknown mode": func() (*api.PreAuthRateLimitConfig, error) {
			return cliPreAuthPatch(stored, "block", 0, 0, true, false, false)
		},
		"rates without config": func() (*api.PreAuthRateLimitConfig, error) { return cliPreAuthPatch(nil, "", 5, 10, false, true, true) },
		"enforce without rates": func() (*api.PreAuthRateLimitConfig, error) {
			return cliPreAuthPatch(nil, api.PreAuthRateLimitEnforce, 0, 0, true, false, false)
		},
	} {
		if _, err := call(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestPreAuthSummary(t *testing.T) {
	for _, tc := range []struct {
		config *api.PreAuthRateLimitConfig
		want   string
	}{
		{nil, "off"},
		{&api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitOff, RequestsPerSecond: 5, Burst: 5}, "off"},
		{&api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitObserve, RequestsPerSecond: 10, Burst: 20}, "observe (10 rps, burst 20 per source)"},
		{&api.PreAuthRateLimitConfig{Mode: api.PreAuthRateLimitEnforce, RequestsPerSecond: 2, Burst: 4,
			Routes: []api.PreAuthRouteLimit{{}}}, "enforce (2 rps, burst 4 per source), 1 route overrides"},
	} {
		if got := preAuthSummary(tc.config); got != tc.want {
			t.Errorf("preAuthSummary(%+v) = %q, want %q", tc.config, got, tc.want)
		}
	}
}
