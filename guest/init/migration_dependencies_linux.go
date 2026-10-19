//go:build linux

package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
	"github.com/onebox-faas/faas/pkg/bindingprobe"
)

func executeMigrationDependencies(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	checks, valid := api.ParseMigrationDependenciesCommand(req.Command, req.CommandShell)
	if !valid {
		return appTaskInfraFailure("migration_dependencies_failed", "invalid migration dependency checks", 1), nil
	}
	env := BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv)
	getenv := func(key string) string { return appTaskEnvValue(env, key) }
	report := api.MigrationDependencyReport{Checks: []api.MigrationVerificationCheck{}}
	add := func(name string, passed bool, detail string) {
		status := "failed"
		if passed {
			status = "passed"
		}
		report.Checks = append(report.Checks, api.MigrationVerificationCheck{Name: name, Status: status, Detail: detail})
	}
	if checks.PostgresEnvKey != "" {
		p := api.PostgresBindingProbeReport{}
		if value := getenv(checks.PostgresEnvKey); value != "" {
			p = runPostgresBindingProbe(ctx, p, value, nil)
		}
		ok := p.Connection.Status == "passed" && p.Query.Status == "passed"
		detail := "Authenticated connection and read-only SELECT 1 succeeded."
		if !ok {
			detail = "Check the database variable, network access, TLS settings and credentials."
		}
		add("postgres", ok, detail)
	}
	if checks.ObjectStoragePrefix != "" {
		p := bindingprobe.ObjectStorage(ctx, checks.ObjectStoragePrefix, getenv)
		detail := "Authenticated read-only storage listing succeeded."
		if !p.Passed() {
			detail = "Check the storage prefix variables, endpoint, bucket and read permission."
		}
		add("object_storage", p.Passed(), detail)
	}
	if checks.HTTPEnvKey != "" {
		ok := migrationHTTPDependency(ctx, getenv(checks.HTTPEnvKey))
		detail := "External API returned a successful response."
		if !ok {
			detail = "Check the API URL variable, TLS, network access and endpoint response."
		}
		add("external_api", ok, detail)
	}
	for _, connection := range checks.ServiceConnections {
		ok := migrationServiceConnectionMatches(connection, getenv)
		if ok {
			var probe bytes.Buffer
			command := apptaskproto.Request{Command: []string{api.AppTaskServiceBindingTargetProbeCommand, connection.Service, connection.DeploymentID}}
			result, err := executeServiceBindingProbeCommand(ctx, command, manifest, secrets, apiEnv, &probe)
			var report api.ServiceBindingProbeReport
			ok = err == nil && result.Status == apptaskproto.StatusSucceeded && json.Unmarshal(probe.Bytes(), &report) == nil && report.Passed() && report.TargetDeploymentID == connection.DeploymentID
		}
		detail := "The runtime variable matches its declared private HTTPS service; the caller reached the selected deployment through its authorized route."
		if !ok {
			detail = "Check the runtime variable, declared service binding, target deployment, private TLS trust and caller permissions."
		}
		add("service/"+connection.EnvKey, ok, detail)
	}
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	exit := 0
	status := apptaskproto.StatusSucceeded
	for _, c := range report.Checks {
		if c.Status != "passed" {
			exit = 1
			status = apptaskproto.StatusFailed
		}
	}
	result := apptaskproto.Result{Status: status, ExitCode: &exit}
	if exit != 0 {
		result.FailureCode = "migration_dependencies_failed"
		result.FailureMessage = "A dependency check failed; inspect the saved verification report."
	}
	return result, nil
}

func migrationServiceConnectionMatches(connection api.MigrationServiceConnectionCheck, getenv func(string) string) bool {
	return getenv(api.ServiceBindingHTTPSEnvKey(connection.Service)) == "https://"+connection.Service+".internal" &&
		getenv(connection.EnvKey) == "https://"+connection.Service+".internal"
}

func migrationHTTPDependency(ctx context.Context, value string) bool {
	u, err := url.Parse(value)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.Fragment != "" {
		return false
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return false
	}
	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}
