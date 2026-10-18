//go:build linux

package main

import (
	"context"
	"io"
	"net"
	"os"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

func executeManagedServiceBindingProbe(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	spec, valid := api.ParseManagedServiceProbeCommand(req.Command, req.CommandShell)
	report := api.ServiceBindingProbeReport{DNS: api.ServiceBindingProbeCheck{Status: "not_checked"}, TLS: api.ServiceBindingProbeCheck{Status: "not_checked"}, Authorization: api.ServiceBindingProbeCheck{Status: "not_checked"}, Routing: api.ServiceBindingProbeCheck{Status: "not_checked"}}
	if !valid {
		report.Error = "invalid managed service probe context"
		return writeServiceBindingProbeReport(stdout, report)
	}
	report.ManagedService, report.Service, report.TargetDeploymentID, report.URL = &spec, spec.Service, spec.TargetDeploymentID, "https://"+spec.Service+".internal"
	actualURL := appTaskEnvValue(BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv), spec.Binding)
	if apiEnv[api.PlatformDeploymentIDEnv] != spec.CallerDeploymentID || apiEnv[spec.Binding] != report.URL || actualURL != report.URL {
		report.Authorization = api.ServiceBindingProbeCheck{Status: "failed"}
		report.Error = "frozen service binding or caller deployment was not staged in this task guest"
		return writeServiceBindingProbeReport(stdout, report)
	}
	trust, err := prepareServiceProxyTrust("/", "/")
	if err != nil || trust.bundle == "" {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed"}
		report.Error = "private service trust is unavailable"
		return writeServiceBindingProbeReport(stdout, report)
	}
	caBundle, err := os.ReadFile(trust.bundle)
	if err != nil {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed"}
		report.Error = "private service trust could not be read"
		return writeServiceBindingProbeReport(stdout, report)
	}
	report = runServiceBindingContextProbe(ctx, spec.Service, spec.TargetDeploymentID, &spec, caBundle, net.DefaultResolver, nil)
	return writeServiceBindingProbeReport(stdout, report)
}
