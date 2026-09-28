package api

import (
	"strings"
	"testing"
	"time"
)

func TestAfterRestoreHookValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		hook AfterRestoreHook
		mode string
		want string
	}{
		{name: "request", hook: AfterRestoreHook{Path: "/internal/restore"}},
		{name: "service", hook: AfterRestoreHook{Path: "/restore", TimeoutMS: 1200}, mode: ExecutionModeService},
		{name: "relative", hook: AfterRestoreHook{Path: "restore"}, want: "absolute path"},
		{name: "redirect-like", hook: AfterRestoreHook{Path: "//example.com/restore"}, want: "absolute path"},
		{name: "query", hook: AfterRestoreHook{Path: "/restore?next=/"}, want: "absolute path"},
		{name: "percent-escape", hook: AfterRestoreHook{Path: "/restore%2F"}, want: "absolute path"},
		{name: "control", hook: AfterRestoreHook{Path: "/restore\t"}, want: "control characters"},
		{name: "timeout", hook: AfterRestoreHook{Path: "/restore", TimeoutMS: AfterRestoreHookMaxTimeoutMS + 1}, want: "timeout_ms"},
		{name: "worker", hook: AfterRestoreHook{Path: "/restore"}, mode: ExecutionModeWorker, want: "request or service"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"/app"}, ExecutionMode: tc.mode, AfterRestore: &tc.hook}
			err := m.ValidatePlan(PlanScale)
			if tc.want == "" && err != nil {
				t.Fatalf("unexpected validation error: %v", err)
			}
			if tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("validation error = %v, want %q", err, tc.want)
			}
		})
	}
	if got := (&AfterRestoreHook{Path: "/restore"}).EffectiveTimeout(); got != 500*time.Millisecond {
		t.Fatalf("default timeout = %s", got)
	}
}

func TestBeforeCheckpointHookValidation(t *testing.T) {
	for _, tc := range []struct {
		name, mode, path, want string
		timeout                int
	}{
		{name: "request", path: "/checkpoint"},
		{name: "service", mode: ExecutionModeService, path: "/checkpoint", timeout: 1200},
		{name: "worker", mode: ExecutionModeWorker, path: "/checkpoint", want: "request or service"},
		{name: "remote", path: "http://example.com/checkpoint", want: "absolute path"},
		{name: "timeout", path: "/checkpoint", timeout: 2001, want: "timeout_ms"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := AppManifest{Entrypoint: []string{"/app"}, ExecutionMode: tc.mode,
				BeforeCheckpoint: &BeforeCheckpointHook{Path: tc.path, TimeoutMS: tc.timeout}}
			err := m.ValidatePlan(PlanPro)
			if tc.want == "" && err != nil || tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)) {
				t.Fatalf("ValidatePlan() = %v, want %q", err, tc.want)
			}
		})
	}
	if got := (&BeforeCheckpointHook{Path: "/checkpoint"}).EffectiveTimeout(); got != 500*time.Millisecond {
		t.Fatalf("default timeout = %v", got)
	}
}
