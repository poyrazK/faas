//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

func isObjectStorageBindingProbeCommand(req apptaskproto.Request) bool {
	return len(req.Command) > 0 && req.Command[0] == api.AppTaskObjectStorageBindingProbeCommand
}

func executeObjectStorageBindingProbeCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	if req.CommandShell || len(req.Command) != 2 {
		return appTaskInfraFailure("object_storage_binding_probe_failed", "invalid platform object-storage binding probe request", 1), nil
	}
	env := BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv)
	report := bindingprobe.ObjectStorage(ctx, req.Command[1], func(key string) string { return appTaskEnvValue(env, key) })
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	exitCode := 0
	if report.Passed() {
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exitCode}, nil
	}
	exitCode = 1
	return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: "object_storage_binding_probe_failed", FailureMessage: report.Error}, nil
}
