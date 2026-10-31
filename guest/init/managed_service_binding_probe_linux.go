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
	transport := spec.ProbeTransport()
	report.ManagedService, report.Service, report.TargetDeploymentID, report.URL = &spec, spec.Service, spec.TargetDeploymentID, api.ServiceBindingCanonicalURL(spec.Service, transport)
	if transport == api.ServiceBindingTransportHTTP {
		report.Transport = transport
	}
	actualURL := appTaskEnvValue(BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv), spec.Binding)
	// The staged canonical URL is transport-specific, so it also pins the
	// caller's transport to the spec's.
	if apiEnv[api.PlatformDeploymentIDEnv] != spec.CallerDeploymentID || apiEnv[spec.Binding] != report.URL || actualURL != report.URL {
		report.Authorization = api.ServiceBindingProbeCheck{Status: "failed"}
		report.Error = "frozen service binding or caller deployment was not staged in this task guest"
		return writeServiceBindingProbeReport(stdout, report)
	}
	if transport == api.ServiceBindingTransportHTTP {
		// ADR-20261019200059: an HTTP edge is probed over the plaintext proxy.
		return writeServiceBindingProbeReport(stdout, runServiceBindingHTTPProbe(ctx, spec.Service, spec.TargetDeploymentID, &spec, net.DefaultResolver, nil))
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
