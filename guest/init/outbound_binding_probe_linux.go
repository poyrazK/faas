//go:build linux

package main

import (
	"context"
	"encoding/json"
	"io"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

func isOutboundBindingProbeCommand(req apptaskproto.Request) bool {
	return len(req.Command) > 0 && req.Command[0] == api.AppTaskOutboundBindingProbeCommand
}
func executeOutboundBindingProbeCommand(ctx context.Context, req apptaskproto.Request, stdout io.Writer) (apptaskproto.Result, error) {
	var spec api.OutboundBindingProbeSpec
	if req.CommandShell || len(req.Command) != 3 || json.Unmarshal([]byte(req.Command[2]), &spec) != nil || !spec.Valid() || spec.IntegrationID != req.Command[1] {
		return appTaskInfraFailure("outbound_binding_probe_failed", "invalid platform outbound binding probe request", 1), nil //nolint:nilerr // invalid requests are terminal protocol results, as in app_task_linux.go
	}
	report := bindingprobe.Outbound(ctx, spec)
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	exit := 0
	if report.Passed() {
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exit}, nil
	}
	exit = 1
	return apptaskproto.Result{Status: apptaskproto.StatusFailed, ExitCode: &exit, FailureCode: "outbound_binding_probe_failed", FailureMessage: report.Error}, nil
}
