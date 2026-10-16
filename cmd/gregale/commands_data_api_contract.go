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

type dataAPIServingContract struct {
	RefreshProtocol    int    `json:"refresh_protocol"`
	RefreshPaused      *bool  `json:"refresh_paused"`
	RefreshVerified    bool   `json:"refresh_verified"`
	RefreshFingerprint string `json:"refresh_fingerprint"`
	Version            int    `json:"version"`
	Ready              bool   `json:"ready"`
	Fingerprint        string `json:"fingerprint"`
}

func readDataAPIServingContract(ctx context.Context, healthURL, token, refreshToken string) (dataAPIServingContract, error) {
	var contract dataAPIServingContract
	u, err := url.Parse(healthURL)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "/healthz" || !validDataAPIAccessToken(token) {
		return contract, errors.New("invalid serving contract URL or application JWT")
	}
	u.Path = "/__gregale/schema"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return contract, errors.New("invalid serving contract request")
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if refreshToken != "" {
		req.Header.Set("X-Gregale-Refresh-Token", refreshToken)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Cache-Control", "no-cache")
	client := &http.Client{Timeout: 5 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return contract, ctx.Err()
		}
		return contract, errors.New("serving contract request failed; check app reachability")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return contract, fmt.Errorf("serving contract returned HTTP %d; verify the application JWT and deploy the current Data API runtime", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, (8<<10)+1))
	if err != nil {
		if ctx.Err() != nil {
			return contract, ctx.Err()
		}
		return contract, errors.New("could not read serving contract")
	}

	if len(body) > 8<<10 || json.Unmarshal(body, &contract) != nil || contract.Version != 1 || !contract.Ready || len(contract.Fingerprint) != 64 || strings.Trim(contract.Fingerprint, "0123456789abcdef") != "" {
		return contract, errors.New("invalid serving contract response")
	}
	return contract, nil
}

func verifyDataAPIServingContract(ctx context.Context, healthURL, token, expected string, coordination ...*dataAPIRefreshVerification) error {
	refreshToken := ""
	if len(coordination) > 0 {
		refreshToken = coordination[0].token
	}
	contract, err := readDataAPIServingContract(ctx, healthURL, token, refreshToken)
	if err != nil {
		return err
	}
	if expected != "" && contract.Fingerprint != expected {
		return errors.New("serving schema differs from generated types; coordinate schema changes, fresh-refresh the runtime and rerun sync")
	}
	if len(coordination) > 0 {
		check := coordination[0]
		if contract.RefreshProtocol != 1 || contract.RefreshPaused == nil || !dataAPIApprovalFingerprint(contract.RefreshFingerprint) {
			return errors.New("coordinated refresh unavailable; verify barrier installation and deploy an enabled current runtime before sync")
		}
		if *contract.RefreshPaused != check.paused {
			return errors.New("refresh barrier phase differs; run data-api status before choosing normal sync or --resume-only")
		}
		if check.paused && !contract.RefreshVerified {
			return errors.New("refresh token does not own this pause; supply the original token and inspect data-api status before recovery")
		}
		if !check.paused && contract.RefreshFingerprint != expected {
			return errors.New("registered contract differs from the expected serving schema; inspect data-api status before sync")
		}
	}
	return nil
}
