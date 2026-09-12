package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const maxDeployIdempotencyKeyLength = 255

// deployIdempotencyIntent is the local deploy input that determines whether
// two invocations represent the same deployment. The source digest is the
// content identity for local/tarball deploys; image and source-ref fields
// cover the two paths where the CLI does not have source bytes.
type deployIdempotencyIntent struct {
	Slug           string `json:"slug"`
	Shape          shape  `json:"shape"`
	Runtime        string `json:"runtime,omitempty"`
	Handler        string `json:"handler,omitempty"`
	Image          string `json:"image,omitempty"`
	Repo           string `json:"repo,omitempty"`
	Ref            string `json:"ref,omitempty"`
	SourceSHA256   string `json:"source_sha256,omitempty"`
	SourceRoot     string `json:"source_root,omitempty"`
	Profile        string `json:"profile,omitempty"`
	Dockerfile     bool   `json:"dockerfile,omitempty"`
	RequireAuthn   *bool  `json:"require_authn,omitempty"`
	AppProtocol    string `json:"app_protocol,omitempty"`
	Reason         string `json:"reason,omitempty"`
	Tag            string `json:"tag,omitempty"`
	DeployedBy     string `json:"deployed_by,omitempty"`
	PRNumber       int    `json:"pr_number,omitempty"`
	TrafficPercent int    `json:"traffic_percent,omitempty"`
	CanaryPreset   string `json:"canary_preset,omitempty"`
	CanaryStages   string `json:"canary_stages,omitempty"`
	RollbackOn5xx  *bool  `json:"rollback_on_5xx,omitempty"`
	NoTriggers     bool   `json:"no_triggers,omitempty"`
	ProjectSlug    string `json:"project_slug,omitempty"`
	DeployOnly     string `json:"deploy_only,omitempty"`
	DeployExclude  string `json:"deploy_exclude,omitempty"`
}

// validateDeployIdempotencyKey validates the user-facing logical key before
// it is hashed into transport-scoped wire keys. Rejecting controls prevents
// malformed HTTP headers while still allowing the normal printable key set.
func validateDeployIdempotencyKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil
	}
	if len(key) > maxDeployIdempotencyKeyLength {
		return fmt.Errorf("must be at most %d characters (got %d)", maxDeployIdempotencyKeyLength, len(key))
	}
	for _, r := range key {
		if r < 0x21 || r == 0x7f {
			return errors.New("must contain printable characters only")
		}
	}
	return nil
}

// deployIdempotencyKey returns a stable logical key. An explicit key is
// intentionally treated as a logical key and scoped below per transport so a
// source-ref deploy cannot collide with a multipart deploy in apid's
// account-wide replay cache.
func deployIdempotencyKey(explicit string, intent deployIdempotencyIntent) (string, error) {
	explicit = strings.TrimSpace(explicit)
	if err := validateDeployIdempotencyKey(explicit); err != nil {
		return "", err
	}
	if explicit != "" {
		return explicit, nil
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return "", fmt.Errorf("encode deploy intent: %w", err)
	}
	digest := sha256.Sum256(payload)
	return "gregale-deploy-" + hex.EncodeToString(digest[:]), nil
}

// deployOperationIdempotencyKey scopes a logical deploy key to one wire
// operation. The server's replay cache is keyed by account + key, not by
// route, so each resumable chunk and terminal operation must have its own
// deterministic key while retries of that operation reuse it.
func deployOperationIdempotencyKey(base, operation string) string {
	base = strings.TrimSpace(base)
	operation = strings.TrimSpace(operation)
	if base == "" || operation == "" {
		return ""
	}
	digest := sha256.Sum256([]byte(base + "\x00" + operation))
	return "gregale-" + operation + "-" + hex.EncodeToString(digest[:])
}
