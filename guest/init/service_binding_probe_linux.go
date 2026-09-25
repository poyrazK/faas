//go:build linux

package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apptaskproto"
)

const serviceBindingProbeTimeout = 5 * time.Second

type serviceBindingProbeResolver interface {
	LookupIPAddr(context.Context, string) ([]net.IPAddr, error)
}

func isServiceBindingProbeCommand(req apptaskproto.Request) bool {
	return len(req.Command) > 0 && req.Command[0] == api.AppTaskServiceBindingProbeCommand
}

func executeServiceBindingProbeCommand(ctx context.Context, req apptaskproto.Request, manifest api.AppManifest, secrets, apiEnv map[string]string, stdout io.Writer) (apptaskproto.Result, error) {
	service := ""
	report := api.ServiceBindingProbeReport{
		DNS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		TLS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ServiceBindingProbeCheck{Status: "not_checked"},
		Routing:       api.ServiceBindingProbeCheck{Status: "not_checked"},
	}
	if !req.CommandShell && len(req.Command) == 2 {
		service = strings.TrimSpace(req.Command[1])
	}
	report.Service = service
	if service != "" {
		report.URL = "https://" + service + ".internal"
	}
	if req.CommandShell || len(req.Command) != 2 {
		report.Error = "invalid platform service-binding probe request"
		return writeServiceBindingProbeReport(stdout, report)
	}
	names, err := api.NormalizeServiceBindingTargets([]string{service})
	if err != nil || len(names) != 1 || names[0] != service {
		report.Error = "service name is invalid"
		return writeServiceBindingProbeReport(stdout, report)
	}
	if got := appTaskEnvValue(BuildEnvWithSecrets(os.Environ(), manifest, secrets, apiEnv), api.ServiceBindingHTTPSEnvKey(service)); got != report.URL {
		report.Authorization = api.ServiceBindingProbeCheck{Status: "failed", Detail: "service has no matching HTTPS binding in this deployment"}
		report.Error = "service is not bound to this deployment"
		return writeServiceBindingProbeReport(stdout, report)
	}
	trust, err := prepareServiceProxyTrust("/", "/")
	if err != nil || trust.bundle == "" {
		detail := "service CA trust bundle is unavailable; HTTPS verification cannot continue"
		if err != nil {
			detail = boundedProbeDetail("prepare service CA trust: " + err.Error())
		}
		report = runServiceBindingProbe(ctx, service, nil, net.DefaultResolver, nil)
		if report.DNS.Status == "passed" {
			report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: detail}
			report.Error = "private service trust is not available in this task guest"
		}
		return writeServiceBindingProbeReport(stdout, report)
	}
	caBundle, err := os.ReadFile(trust.bundle)
	if err != nil {
		report = runServiceBindingProbe(ctx, service, nil, net.DefaultResolver, nil)
		if report.DNS.Status == "passed" {
			report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "service CA trust bundle could not be read"}
			report.Error = "private service trust could not be read in this task guest"
		}
		return writeServiceBindingProbeReport(stdout, report)
	}
	report = runServiceBindingProbe(ctx, service, caBundle, net.DefaultResolver, nil)
	return writeServiceBindingProbeReport(stdout, report)
}

func runServiceBindingProbe(ctx context.Context, service string, caBundle []byte, resolver serviceBindingProbeResolver, dialContext func(context.Context, string, string) (net.Conn, error)) api.ServiceBindingProbeReport {
	report := api.ServiceBindingProbeReport{
		Service:       service,
		URL:           "https://" + service + ".internal",
		DNS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		TLS:           api.ServiceBindingProbeCheck{Status: "not_checked"},
		Authorization: api.ServiceBindingProbeCheck{Status: "not_checked"},
		Routing:       api.ServiceBindingProbeCheck{Status: "not_checked"},
	}
	host := service + ".internal"
	if resolver == nil {
		resolver = net.DefaultResolver
	}
	ips, err := resolver.LookupIPAddr(ctx, host)
	if err != nil || len(ips) == 0 {
		detail := "service alias did not resolve in the caller network"
		if err != nil {
			detail = boundedProbeDetail("service alias lookup failed: " + err.Error())
		}
		report.DNS = api.ServiceBindingProbeCheck{Status: "failed", Detail: detail}
		report.Error = "service alias DNS lookup failed"
		return report
	}
	report.DNS = api.ServiceBindingProbeCheck{Status: "passed", Detail: fmt.Sprintf("resolved to %d address(es)", len(ips))}

	roots := x509.NewCertPool()
	if ok := roots.AppendCertsFromPEM(caBundle); !ok {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "service CA trust bundle contains no usable certificates"}
		report.Error = "TLS certificate verification is not configured"
		return report
	}
	requestURL, err := url.Parse(report.URL + api.ServiceBindingProbePath)
	if err != nil {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "probe URL is invalid"}
		report.Error = "could not construct the HTTPS probe request"
		return report
	}
	transport := &http.Transport{
		Proxy: nil, // Never allow ambient proxy settings to bypass the private route.
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
		Timeout:   serviceBindingProbeTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	defer transport.CloseIdleConnections()
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, requestURL.String(), nil)
	if err != nil {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "probe request could not be created"}
		report.Error = "could not construct the HTTPS probe request"
		return report
	}
	request.Header.Set(api.ServiceBindingProbeRequestHeader, api.ServiceBindingProbeVersion)
	response, err := client.Do(request)
	if err != nil {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: boundedProbeDetail("HTTPS request failed: " + err.Error())}
		report.Error = "TLS handshake or HTTPS connection failed"
		return report
	}
	defer response.Body.Close()
	report.HTTPStatus = response.StatusCode
	if response.TLS == nil {
		report.TLS = api.ServiceBindingProbeCheck{Status: "failed", Detail: "response did not use TLS"}
		report.Error = "HTTPS probe did not complete a TLS connection"
		return report
	}
	report.TLS = api.ServiceBindingProbeCheck{Status: "passed", Detail: tlsVersionName(response.TLS.Version) + "; certificate verified"}

	stage := response.Header.Get(api.ServiceBindingProbeStageHeader)
	marker := response.Header.Get(api.ServiceBindingProbeResponseHeader)
	if response.StatusCode == http.StatusNoContent && marker == api.ServiceBindingProbeVersion && stage == "complete" {
		report.Authorization = api.ServiceBindingProbeCheck{Status: "passed", Detail: "caller binding and target policy allowed the request"}
		report.Routing = api.ServiceBindingProbeCheck{Status: "passed", Detail: "healthy endpoint is registered; target was not woken"}
		return report
	}
	switch stage {
	case "identity", "binding", "authorization":
		report.Authorization = api.ServiceBindingProbeCheck{Status: "failed", Detail: "caller identity or service authorization was rejected"}
		report.Routing = api.ServiceBindingProbeCheck{Status: "not_checked"}
	case "discovery":
		report.Authorization = api.ServiceBindingProbeCheck{Status: "not_checked", Detail: "service binding passed, but target authorization was not reached"}
		report.Routing = api.ServiceBindingProbeCheck{Status: "failed", Detail: "service target was not resolved"}
	case "routing":
		report.Authorization = api.ServiceBindingProbeCheck{Status: "passed", Detail: "caller binding and target policy allowed the request"}
		report.Routing = api.ServiceBindingProbeCheck{Status: "failed", Detail: "no healthy endpoint is currently registered or the registry is unavailable"}
	default:
		report.Authorization = api.ServiceBindingProbeCheck{Status: "not_checked"}
		report.Routing = api.ServiceBindingProbeCheck{Status: "not_checked"}
	}
	report.Error = fmt.Sprintf("gateway did not confirm the private route (HTTP %d, stage %s)", response.StatusCode, stage)
	return report
}

func dialResolvedServiceAlias(ips []net.IPAddr) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	return func(ctx context.Context, network, address string) (net.Conn, error) {
		_, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		var dialErr error
		for _, ip := range ips {
			conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.String(), port))
			if err == nil {
				return conn, nil
			}
			dialErr = err
		}
		if dialErr == nil {
			dialErr = errors.New("service alias resolved to no usable addresses")
		}
		return nil, dialErr
	}
}

func writeServiceBindingProbeReport(stdout io.Writer, report api.ServiceBindingProbeReport) (apptaskproto.Result, error) {
	if err := json.NewEncoder(stdout).Encode(report); err != nil {
		return apptaskproto.Result{}, err
	}
	if report.Passed() {
		exitCode := 0
		return apptaskproto.Result{Status: apptaskproto.StatusSucceeded, ExitCode: &exitCode}, nil
	}
	exitCode := 1
	message := report.Error
	if message == "" {
		message = "one or more service-binding probe checks failed"
	}
	return apptaskproto.Result{
		Status: apptaskproto.StatusFailed, ExitCode: &exitCode,
		FailureCode: "service_binding_probe_failed", FailureMessage: boundedProbeDetail(message),
	}, nil
}

func appTaskEnvValue(env []string, key string) string {
	value := ""
	for _, item := range env {
		name, current, ok := strings.Cut(item, "=")
		if ok && name == key {
			value = current
		}
	}
	return value
}

func tlsVersionName(version uint16) string {
	switch version {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	default:
		return "TLS"
	}
}

func boundedProbeDetail(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	if len(detail) > 240 {
		return detail[:240]
	}
	return detail
}
