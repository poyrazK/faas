// adr: 650 — compare the serving startup catalog with private type export.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

var dataAPIAccessTokenShape = regexp.MustCompile(`^[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`)

var dataAPITokenEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func validDataAPIAccessToken(token string) bool {
	return len(token) <= 16<<10 && dataAPIAccessTokenShape.MatchString(token)
}

func verifyDataAPIServingContract(ctx context.Context, healthURL, token, expected string) error {
	u, err := url.Parse(healthURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/healthz" || !validDataAPIAccessToken(token) {
		return errors.New("invalid serving contract URL or application JWT")
	}
	u.Path = "/__gregale/schema"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return errors.New("invalid serving contract request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("serving contract request failed; check app reachability")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("serving contract returned HTTP %d; verify the application JWT and deploy the current Data API runtime", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (8<<10)+1))
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return errors.New("could not read serving contract")
	}
	var contract struct {
		Version     int    `json:"version"`
		Ready       bool   `json:"ready"`
		Fingerprint string `json:"fingerprint"`
	}
	if len(body) > 8<<10 || json.Unmarshal(body, &contract) != nil || contract.Version != 1 || !contract.Ready || len(contract.Fingerprint) != 64 || strings.Trim(contract.Fingerprint, "0123456789abcdef") != "" {
		return errors.New("invalid serving contract response")
	}
	if contract.Fingerprint != expected {
		return errors.New("serving schema differs from generated types; coordinate schema changes, fresh-refresh the runtime and rerun sync")
	}
	return nil
}
