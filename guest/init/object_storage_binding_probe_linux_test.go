//go:build linux

// adr: 426 — reserved guest dispatch uses deployment-scoped binding environment.
package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func TestAppTaskObjectStorageProbeDispatchAndScopedEnvironment(t *testing.T) {
	var stdout strings.Builder
	result, err := executeAppTaskCommand(context.Background(), apptaskproto.Request{
		Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "GREGALE_TEST_STORAGE"},
	}, api.AppManifest{}, map[string]string{"GREGALE_TEST_STORAGE_ENDPOINT": "https://private.example"}, nil, &stdout, &strings.Builder{})
	if err != nil || result.Status != apptaskproto.StatusFailed || result.FailureCode != "object_storage_binding_probe_failed" {
		t.Fatalf("dispatch result=%+v err=%v", result, err)
	}
	var report api.ObjectStorageBindingProbeReport
	if err := json.Unmarshal([]byte(stdout.String()), &report); err != nil || report.Prefix != "GREGALE_TEST_STORAGE" || report.Environment.Status != "failed" || report.Connection.Status != "not_checked" || strings.Contains(stdout.String(), "private.example") {
		t.Fatalf("scoped report=%+v err=%v", report, err)
	}
}

func TestAppTaskObjectStorageProbeRejectsShellAndExtraArguments(t *testing.T) {
	for _, request := range []apptaskproto.Request{
		{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS"}, CommandShell: true},
		{Command: []string{api.AppTaskObjectStorageBindingProbeCommand, "ASSETS", "unexpected"}},
	} {
		result, err := executeAppTaskCommand(context.Background(), request, api.AppManifest{}, nil, nil, &strings.Builder{}, &strings.Builder{})
		if err != nil || result.Status != apptaskproto.StatusFailed || result.ExitCode == nil || *result.ExitCode != 1 {
			t.Fatalf("malformed request=%+v err=%v", result, err)
		}
	}
}
