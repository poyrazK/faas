//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const serviceBindingSmokeTimeout = 45 * time.Second

type serviceBindingSmokeResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func isServiceBindingSmokeCommand(req apptaskproto.Request) bool {
	return len(req.Command) > 0 && req.Command[0] == api.AppTaskServiceBindingSmokeCommand
}

func executeServiceBindingSmokeCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	report := api.ServiceBindingSmokeReport{ExpectedStatus: "2xx"}
	if req.CommandShell || len(req.Command) != 5 {
		report.Error = "invalid platform service-binding smoke request"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	service := strings.TrimSpace(req.Command[1])
	targetID, err := uuid.Parse(strings.TrimSpace(req.Command[2]))
	requestURI, pathErr := api.NormalizeServiceBindingSmokePath(req.Command[3])
	expectedStatus, statusErr := strconv.Atoi(strings.TrimSpace(req.Command[4]))
	report.Service = service
	report.URL = "https://" + service + ".internal"
	if targetID != uuid.Nil {
		report.TargetDeploymentID = targetID.String()
	}
	if statusErr == nil && expectedStatus > 0 {
		report.ExpectedStatus = strconv.Itoa(expectedStatus)
	}
	if err != nil || targetID == uuid.Nil {
		report.Error = "target deployment ID is invalid"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	names, err := api.NormalizeServiceBindingTargets([]string{service})
	if err != nil || len(names) != 1 || names[0] != service {
		report.Error = "service name is invalid"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	if pathErr != nil {
		report.Error = "service path is invalid"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	parsedPath, err := url.ParseRequestURI(requestURI)
	if err != nil {
		report.Error = "service path is invalid"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	report.Path = parsedPath.EscapedPath()
	if report.Path == "" {
		report.Path = "/"
	}
	if statusErr != nil || expectedStatus != 0 && (expectedStatus < 200 || expectedStatus > 599) {
		report.Error = "expected status must be zero or an HTTP status code from 200 to 599"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	env := BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv)
	if appTaskEnvValue(env, api.ServiceBindingHTTPSEnvKey(service)) != report.URL {
		report.Error = "service is not bound to this deployment"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	trust, err := prepareServiceProxyTrust("/", "/")
	if err != nil || trust.bundle == "" {
		report.Error = "private service CA trust is unavailable in this task guest"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	caBundle, err := os.ReadFile(trust.bundle)
	if err != nil {
		report.Error = "private service CA trust could not be read in this task guest"
		return writeServiceBindingSmokeReport(stdout, report)
	}
	report = runServiceBindingSmoke(ctx, service, targetID.String(), requestURI, expectedStatus, caBundle, net.DefaultResolver, nil)
	return writeServiceBindingSmokeReport(stdout, report)
}

func runServiceBindingSmoke(ctx context.Context, service, targetDeploymentID, requestURI string, expectedStatus int, caBundle []byte, resolver serviceBindingSmokeResolver, dialContext func(context.Context, string, string) (net.Conn, error)) api.ServiceBindingSmokeReport {
	report := api.ServiceBindingSmokeReport{
		Service:            service,
		TargetDeploymentID: targetDeploymentID,
		URL:                "https://" + service + ".internal",
		ExpectedStatus:     "2xx",
	}
	if expectedStatus > 0 {
		report.ExpectedStatus = strconv.Itoa(expectedStatus)
	}
	requestURI, err := api.NormalizeServiceBindingSmokePath(requestURI)
	if err != nil {
		report.Error = "service path is invalid"
		return report
	}
	parsedPath, err := url.ParseRequestURI(requestURI)
	if err != nil {
		report.Error = "service path is invalid"
		return report
	}
	report.Path = parsedPath.EscapedPath()
	if report.Path == "" {
		report.Path = "/"
	}
	parsedService, err := api.NormalizeServiceBindingTargets([]string{service})
	if err != nil || len(parsedService) != 1 || parsedService[0] != service {
		report.Error = "service name is invalid"
		return report
	}
	parsedTargetID, err := uuid.Parse(targetDeploymentID)
	if err != nil || parsedTargetID == uuid.Nil {
		report.Error = "target deployment ID is invalid"
		return report
	}
	if expectedStatus != 0 && (expectedStatus < 200 || expectedStatus > 599) {
		report.Error = "expected status must be zero or an HTTP status code from 200 to 599"
		return report
	}
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	host := service + ".internal"
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		report.Error = "service alias DNS lookup failed in the caller network"
		return report
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(caBundle) {
		report.Error = "service CA trust bundle contains no usable certificates"
		return report
	}
	requestURL := (&url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     parsedPath.Path,
		RawPath:  parsedPath.RawPath,
		RawQuery: parsedPath.RawQuery,
	}).String()
	transport := &http.Transport{
		Proxy: nil, // A smoke test must use the private service alias, not ambient proxy settings.
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS13,
			RootCAs:    roots,
			ServerName: host,
		},
		DisableKeepAlives: true,
	}
	if dialContext != nil {
		transport.DialContext = dialContext
	} else {
		transport.DialContext = dialResolvedServiceAlias(ips)
	}
	client := &http.Client{
		Transport: transport,
		Timeout:   serviceBindingSmokeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		report.Error = "could not construct the service smoke request"
		return report
	}
	request.Header.Set(api.TargetDeploymentHeader, parsedTargetID.String())
	started := time.Now()
	response, err := client.Do(request)
	report.ElapsedMillis = time.Since(started).Milliseconds()
	if err != nil {
		report.Error = "HTTPS service request failed; check private trust, target availability, and the selected route"
		return report
	}
	defer func() { _ = response.Body.Close() }()
	if response.TLS == nil {
		report.Error = "service response did not use TLS"
		return report
	}
	report.HTTPStatus = response.StatusCode
	if expectedStatus > 0 {
		report.Passed = response.StatusCode == expectedStatus
	} else {
		report.Passed = response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices
	}
	if !report.Passed {
		report.Error = fmt.Sprintf("unexpected HTTP status %d (expected %s)", response.StatusCode, report.ExpectedStatus)
	}
	return report
}

func writeServiceBindingSmokeReport(stdout io.Writer, report api.ServiceBindingSmokeReport) (apptaskproto.Result, error) {
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	if report.Passed {
		exitCode := 0
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exitCode}, nil
	}
	exitCode := 1
	message := report.Error
	if message == "" {
		message = "service smoke request did not match the expected response"
	}
	return apptaskproto.Result{
		Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: "service_binding_smoke_failed", FailureMessage: boundedProbeDetail(message),
	}, nil
}
