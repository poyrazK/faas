package api

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// CLI auth device-code flow (spec §2.2). The CLI mints a code
// anonymously, the user pastes it into the browser at /cli-auth, the
// dashboard binds it to an account (creating one if needed), and the
// CLI polls for the plaintext API key. The shapes here are the wire
// contract; both `cmd/apid` and `cmd/faas` import them so the two
// sides never disagree.

// EventCliAuthAutoCreated is the structured-log event name emitted
// when POST /cli-auth creates a fresh account row (UX §2.2
// "signup ≡ login" — review finding F2). Operators grep or
// metric-filter on this exact event name to spot enumeration-pattern
// abuse. The handler emits it via slog with
// `slog.String("event", api.EventCliAuthAutoCreated)`.
const EventCliAuthAutoCreated = "cli_auth_auto_created"

// CliAuthStatus is the lifecycle state of a /v1/cli-auth/code row.
type CliAuthStatus string

const (
	CliAuthStatusPending  CliAuthStatus = "pending"
	CliAuthStatusConsumed CliAuthStatus = "consumed"
	CliAuthStatusExpired  CliAuthStatus = "expired"
)

// CliAuthCodeResponse is what POST /v1/cli-auth/code returns. The CLI
// prints the URL and accepts the code as a fallback for browser-less
// machines (paste-the-code-into-the-same-terminal mode).
type CliAuthCodeResponse struct {
	Code      string `json:"code"`
	URL       string `json:"url"`
	ExpiresAt string `json:"expires_at"` // RFC3339
	// DeviceCode is set when the request asked for one (Device): 32 random
	// bytes, hex, that only this CLI holds. Exchange then accepts only it,
	// never Code, so guessing the short code a user approves yields nothing
	// (bug hunt 2026-10-28 H67).
	DeviceCode string `json:"device_code,omitempty"`
	// Loopback is true when the server took the request's LoopbackPort: URL
	// then opens a one-click approval page that sends a fresh device code to
	// http://127.0.0.1:<port>/callback, and DeviceCode no longer exchanges
	// once the code is approved that way (ADR-20261111170000). A CLI that
	// sees false falls back to the typed-code flow.
	Loopback bool `json:"loopback,omitempty"`
	// Verifier is the secret (64 hex characters) whose SHA-256 the CLI sent
	// as the request's CodeChallenge. It stays in the CLI's memory and goes
	// only to the exchange; empty when the server did not take a challenge.
	Verifier string `json:"-"`
}

// NewCliAuthVerifier returns a random 32-byte code verifier and its SHA-256
// challenge, both hex.
func NewCliAuthVerifier() (verifier, challenge string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("code verifier: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(raw), hex.EncodeToString(sum[:]), nil
}

// CliAuthLoopbackCallbackPath is the path the CLI serves on 127.0.0.1 for
// the one-click login. The browser arrives with ?code=<the short code>&
// device_code=<64 hex>.
const CliAuthLoopbackCallbackPath = "/callback"

// CliAuthExchangeRequest is the CLI's poll body: DeviceCode for a code
// minted with one, else Code (CLIs that predate device codes).
type CliAuthExchangeRequest struct {
	Code       string `json:"code,omitempty"`
	DeviceCode string `json:"device_code,omitempty"`
	// CodeVerifier is the 32-byte secret (64 hex characters) whose SHA-256
	// the CLI sent as CliAuthCodeRequest.CodeChallenge. A code minted with
	// a challenge exchanges only together with it.
	CodeVerifier string `json:"code_verifier,omitempty"`
}

// CliAuthExchangeResponse is the only response the CLI writes to disk.
// Plaintext is shown exactly once and never persisted server-side.
type CliAuthExchangeResponse struct {
	Plaintext string          `json:"plaintext"`
	KeyID     string          `json:"key_id"`
	ExpiresAt string          `json:"expires_at"`
	Account   AccountResponse `json:"account"`
}

// CliAuthCodeRequest selects an immutable, limited browser authorization.
// Empty Purpose preserves the normal CLI login contract.
type CliAuthCodeRequest struct {
	Purpose string `json:"purpose,omitempty"`
	// Scopes, with purpose "mcp" only, adds MCPCodeScopes to apps:read.
	Scopes []string `json:"scopes,omitempty"`
	// Device asks for a CLI login code bound to a device code
	// (CliAuthCodeResponse.DeviceCode).
	Device bool `json:"device,omitempty"`
	// LoopbackPort, with Device, asks for a one-click login: the CLI listens
	// on 127.0.0.1:LoopbackPort (1024-65535) and receives the device code
	// from the approving browser instead of the user typing the code.
	LoopbackPort int `json:"loopback_port,omitempty"`
	// CodeChallenge, with Device, is the SHA-256 (64 hex characters) of a
	// secret verifier the CLI keeps in memory. The device code then
	// exchanges only together with that verifier, so a device code seen in
	// a browser's history or a local server's log is useless alone.
	CodeChallenge string `json:"code_challenge,omitempty"`
}

const CliAuthPurposeMCPRead = "mcp-read"
const CliAuthPurposeMCPWrite = "mcp-write"

// CliAuthPurposeMCP mints a code for apps:read plus the requested scopes
// (ADR-20261026195952). The scope set is part of the code and its stored
// hash, so neither the browser nor exchange can widen it.
const CliAuthPurposeMCP = "mcp"

// MCPGrantableScopes are the scopes an MCP consent may add to apps:read, in
// the bit order of the code's scope hex field. postgres:manage brings
// postgres:read with it.
var MCPGrantableScopes = []string{ScopeDeployWrite, ScopeSecretsRequest, ScopeStorageManage, ScopeManagedPostgresManage, ScopeEnvWrite, ScopeUsageRead, ScopeDataRead}

// ScopeConnectApproval, on an MCP sign-in code only, is the person's Connect
// consent standing in for per-request approval of the connection's deploys,
// promotions, rollbacks, restarts and undo (ADR-20261127160000). No key
// carries it: the exchange records it in api_key_connect_grants instead. It
// is not in MCPGrantableScopes, so organization agent policies neither list
// nor cap it; an organization's approval floor still applies.
const ScopeConnectApproval = "approvals:connect"

// MCPCodeScopes are the scope bits of an MCP sign-in code, in bit order:
// MCPGrantableScopes, then ScopeConnectApproval at bit 7. A new grantable
// scope takes bit 8, so a code keeps its meaning across releases.
var MCPCodeScopes = []string{ScopeDeployWrite, ScopeSecretsRequest, ScopeStorageManage, ScopeManagedPostgresManage, ScopeEnvWrite, ScopeUsageRead, ScopeDataRead, ScopeConnectApproval}

// MintScopedMCPAuthCode mints a browser code for apps:read plus scopes, each
// one of MCPCodeScopes.
func (c *Client) MintScopedMCPAuthCode(ctx context.Context, scopes []string) (CliAuthCodeResponse, error) {
	var out CliAuthCodeResponse
	err := c.do(ctx, "POST", "/v1/cli-auth/code", CliAuthCodeRequest{Purpose: CliAuthPurposeMCP, Scopes: scopes}, &out)
	return out, err
}

// MintMCPAuthCode uses the existing dashboard sign-in and account-bound consent.
func (c *Client) MintMCPAuthCode(ctx context.Context, writes bool) (CliAuthCodeResponse, error) {
	purpose := CliAuthPurposeMCPRead
	if writes {
		purpose = CliAuthPurposeMCPWrite
	}
	var out CliAuthCodeResponse
	err := c.do(ctx, "POST", "/v1/cli-auth/code", CliAuthCodeRequest{Purpose: purpose}, &out)
	return out, err
}
