package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Release builds bind the verified capability to the exact embedded Action
// commit. Source builds keep the older fallback pin and do not advertise it.
var githubActionRegionSupportSHA string

func requireGithubActionRegion(ctx context.Context, sha string, explicitPin bool) error {
	if sha == githubActionDefaultSHA && sha == githubActionRegionSupportSHA {
		return nil
	}
	if !explicitPin {
		return errors.New("the embedded deploy Action does not support region selection; use --pin-action or --pinned-sha to verify a newer released Action")
	}
	return verifyGithubActionRegion(ctx, http.DefaultClient, "https://raw.githubusercontent.com", sha)
}

func verifyGithubActionRegion(ctx context.Context, client *http.Client, baseURL, sha string) error {
	sha, err := normalizeGithubActionSHA(sha)
	if err != nil {
		return fmt.Errorf("verify regional Action pin: %w", err)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	root := baseURL + "/" + githubActionRepo + "/" + sha + "/" + githubActionPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, root+"/src/capabilities.json", nil)
	if err != nil {
		return fmt.Errorf("regional Action capabilities request: %w", err)
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("read regional Action capabilities: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("deploy Action %s has no verified regional capability (HTTP %d)", sha, response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 4097))
	if err != nil {
		return fmt.Errorf("read regional Action capabilities: %w", err)
	}
	var capabilities struct {
		SchemaVersion int  `json:"schema_version"`
		ComputeRegion bool `json:"compute_region"`
	}
	if len(data) > 4096 || json.Unmarshal(data, &capabilities) != nil || capabilities.SchemaVersion != 1 || !capabilities.ComputeRegion {
		return fmt.Errorf("deploy Action %s does not declare a supported regional deployment contract", sha)
	}
	// Source commits lack the release's vendored CLI. Verify both files at the
	// same immutable commit before emitting a customer workflow. The Action
	// checks the CLI's --region support again before authentication or mutation.
	req, err = http.NewRequestWithContext(ctx, http.MethodHead, root+"/bin/gregale", nil)
	if err != nil {
		return fmt.Errorf("regional Action bundle request: %w", err)
	}
	bundle, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("verify regional Action bundle: %w", err)
	}
	defer func() { _ = bundle.Body.Close() }()
	if bundle.StatusCode != http.StatusOK || bundle.ContentLength <= 0 {
		return fmt.Errorf("deploy Action %s has no released CLI bundle; choose a released Action commit", sha)
	}
	return nil
}
