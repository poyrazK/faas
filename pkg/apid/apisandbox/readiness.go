// Package apisandbox verifies the public endpoints of a staged API sandbox.
package apisandbox

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/outbound/routepolicy"
)

const (
	OpenAPIPath    = "/openapi.json"
	probeTimeout   = 10 * time.Second
	maxOpenAPISize = 1 << 20
)

// ValidSmokePath accepts an omitted check or a canonical path on the sandbox host.
func ValidSmokePath(path string) bool {
	return path == "" || (len(path) <= 512 && routepolicy.CanonicalPath(path))
}

// Check uses sandbox-scoped credentials to verify that both public responses
// come from deploymentID. A failed result indicates whether to retry.
func Check(ctx context.Context, client *http.Client, base, token, deploymentID, smokePath string) (*api.APISandboxVerification, *api.APISandboxFailure, bool) {
	if !ValidSmokePath(smokePath) {
		return nil, &api.APISandboxFailure{Code: "smoke_path_invalid", Detail: "The smoke route must be a path on the sandbox host."}, false
	}
	if client == nil {
		client = http.DefaultClient
	}
	prober := *client
	prober.Timeout = probeTimeout
	prober.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

	docStatus, failure, retry := probe(ctx, prober, base, OpenAPIPath, token, deploymentID, true)
	if failure != nil {
		return nil, failure, retry
	}
	var smokeStatus int
	switch smokePath {
	case "":
	case OpenAPIPath:
		smokeStatus = docStatus
	default:
		smokeStatus, failure, retry = probe(ctx, prober, base, smokePath, token, deploymentID, false)
		if failure != nil {
			return nil, failure, retry
		}
	}
	return &api.APISandboxVerification{
		DeploymentID: deploymentID, CheckedAt: time.Now().UTC(), OpenAPIStatus: docStatus, SmokeStatus: smokeStatus,
	}, nil, false
}

func probe(ctx context.Context, client http.Client, base, path, token, deploymentID string, openAPI bool) (int, *api.APISandboxFailure, bool) {
	check, detail := "smoke", "GET "+path
	if openAPI {
		check = "openapi"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+path, nil)
	if err != nil {
		return 0, &api.APISandboxFailure{Code: check + "_request", Detail: detail + " could not be requested."}, false
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Cache-Control", "no-cache")
	if openAPI {
		req.Header.Set("Accept", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, &api.APISandboxFailure{Code: check + "_unreachable", Detail: detail + " could not be reached through the public sandbox URL."}, true
	}
	defer resp.Body.Close()
	status := resp.StatusCode
	if !slices.Equal(resp.Header.Values(api.RevisionHeader), []string{deploymentID}) {
		return status, &api.APISandboxFailure{Code: check + "_revision", Detail: detail + " did not return evidence of the selected deployment.", HTTPStatus: status}, true
	}
	if status < 200 || status >= 300 {
		return status, &api.APISandboxFailure{Code: check + "_http_status", Detail: detail + " must return a 2xx response directly.", HTTPStatus: status}, status >= 500 || status == http.StatusTooManyRequests
	}
	if openAPI {
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxOpenAPISize+1))
		switch {
		case err != nil:
			return status, &api.APISandboxFailure{Code: "openapi_incomplete", Detail: "The OpenAPI response did not finish downloading.", HTTPStatus: status}, true
		case len(body) > maxOpenAPISize:
			return status, &api.APISandboxFailure{Code: "openapi_too_large", Detail: "GET /openapi.json exceeded the 1 MiB verification limit.", HTTPStatus: status}, false
		case !validSandboxOpenAPI(body):
			return status, &api.APISandboxFailure{Code: "openapi_invalid", Detail: "GET /openapi.json must return an OpenAPI 3.0 or 3.1 JSON document with info and paths.", HTTPStatus: status}, false
		}
	}
	return status, nil, false
}

func validSandboxOpenAPI(body []byte) bool {
	var doc struct {
		OpenAPI string `json:"openapi"`
		Info    struct {
			Title   string `json:"title"`
			Version string `json:"version"`
		} `json:"info"`
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if json.Unmarshal(body, &doc) != nil || doc.Info.Title == "" || doc.Info.Version == "" || doc.Paths == nil {
		return false
	}
	_, patch, ok := strings.Cut(doc.OpenAPI, ".")
	if !ok || !strings.HasPrefix(doc.OpenAPI, "3.") {
		return false
	}
	minor, patch, ok := strings.Cut(patch, ".")
	return ok && (minor == "0" || minor == "1") && patch != "" && strings.Trim(patch, "0123456789") == ""
}
