// adr: 430 — a bounded safe-method canary exercises normal outbound admission.
package bindingprobe

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound"
)

const outboundIdentityEndpoint = "http://127.0.0.1:2773/oidc/token"

func Outbound(ctx context.Context, spec api.OutboundBindingProbeSpec) api.OutboundBindingProbeReport {
	transport := &http.Transport{}
	if base, ok := http.DefaultTransport.(*http.Transport); ok {
		transport = base.Clone()
	}
	transport.Proxy = nil
	client := &http.Client{Transport: transport}
	defer transport.CloseIdleConnections()
	return outboundProbe(ctx, spec, client, outboundIdentityEndpoint)
}

func outboundProbe(ctx context.Context, spec api.OutboundBindingProbeSpec, client *http.Client, identityEndpoint string) api.OutboundBindingProbeReport {
	check := api.OutboundBindingProbeCheck{Status: "not_checked"}
	report := api.OutboundBindingProbeReport{IntegrationID: spec.IntegrationID, Configuration: check, Identity: check, Gateway: check, Response: check}
	if !spec.Valid() {
		report.Configuration.Status, report.Error = "failed", "invalid outbound probe configuration"
		return report
	}
	report.Configuration.Status = "passed"
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	clone := *client
	clone.Jar = nil
	clone.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	token, ok := outboundProbeIdentity(ctx, &clone, identityEndpoint, spec.IntegrationID)
	if !ok {
		report.Identity.Status, report.Error = "failed", "outbound workload identity unavailable"
		return report
	}
	report.Identity.Status = "passed"
	req, err := http.NewRequestWithContext(ctx, spec.Policy.Method, strings.TrimRight(spec.GatewayURL, "/")+"/i/"+url.PathEscape(spec.IntegrationID)+spec.Policy.Path, nil)
	if err != nil {
		report.Gateway.Status, report.Error = "failed", "outbound gateway request invalid"
		return report
	}
	req.Header.Set(outbound.WorkloadIdentityHeader, token)
	req.Header.Set("Cache-Control", "no-cache, no-store")
	resp, err := clone.Do(req)
	if err != nil {
		report.Gateway.Status, report.Error = "failed", "outbound gateway request failed"
		return report
	}
	// No provider headers, body, credentials, token or transport errors enter the report.
	_ = resp.Body.Close()
	report.Gateway.Status = "passed"
	if resp.StatusCode != spec.Policy.ExpectedStatus {
		report.Response.Status, report.Response.Detail, report.Error = "failed", fmt.Sprintf("http_%d", resp.StatusCode), "outbound response did not match the configured success status"
		return report
	}
	report.Response.Status = "passed"
	return report
}

func outboundProbeIdentity(ctx context.Context, client *http.Client, endpoint, integration string) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"?audience="+url.QueryEscape("gregale:outbound:"+integration), nil)
	if err != nil {
		return "", false
	}
	response, err := client.Do(req)
	if err != nil {
		return "", false
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return "", false
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8193))
	if err != nil || len(raw) > 8192 {
		return "", false
	}
	var result struct {
		AccessToken string `json:"access_token"`
		TokenType   string `json:"token_type"`
		ExpiresIn   int64  `json:"expires_in"`
		Error       string `json:"error"`
	}
	if json.Unmarshal(raw, &result) != nil || result.Error != "" || result.TokenType != "Bearer" || result.ExpiresIn <= 0 || result.AccessToken == "" || len(result.AccessToken) > 8192 || strings.ContainsAny(result.AccessToken, "\r\n") {
		return "", false
	}
	return result.AccessToken, true
}
