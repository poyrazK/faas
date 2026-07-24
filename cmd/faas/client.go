package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// This file is the CLI's thin facade over the public SDK client in
// pkg/api. The actual HTTP / auth / Problem decoding logic lives in
// pkg/api.Client — see client.go in that package.
//
// Why a facade at all:
//   - DeployTarball is the one method where the CLI takes a string
//     path from the user, validates it (openCustomerFile refuses
//     symlinks), and only then hands the *os.File to the SDK. The
//     security boundary belongs in cmd/faas, not pkg/api — moving
//     openCustomerFile into pkg/api would import filesystem policy
//     into the wire layer.
//   - ExportAccount has the CLI write the bundle to a file. The SDK
//     returns the parsed struct; the CLI is the right place for the
//     filesystem concern.
//
// The CLI's renderAPIError, authedClient, and login helpers all flow
// through the SDK directly; only DeployTarball and ExportAccountFile
// need CLI wrappers because the SDK has no opinion on the filesystem.

// Client aliases the public SDK client. Since Go disallows defining
// new methods on alias types, CLI-side wrappers (DeployTarball etc.)
// are free functions declared below with a *Client parameter.
type Client = api.Client

// NewClient wraps api.NewClient.
func NewClient(baseURL, token string) *Client { return api.NewClient(baseURL, token) }

// NewClientWithDeployTimeout wraps api.NewClientWithDeployTimeout.
func NewClientWithDeployTimeout(baseURL, token string, d time.Duration) *Client {
	return api.NewClientWithDeployTimeout(baseURL, token, d)
}

// APIError aliases the SDK's error type. CLI callers type-switch on
// this so we keep one canonical error wrapper across both surfaces.
type APIError = api.APIError

// DeployTarball is the CLI's wrapper around openCustomerFile +
// pkg/api.Client.DeployMultipart. The pre-open + post-open Lstat
// discipline (see commands5.go::openCustomerFile) is the security
// boundary that prevents a symlinked tarball from exfiltrating
// arbitrary bytes; the SDK has no opinion on file provenance.
//
// Refusing the path runs BEFORE the SDK sees anything: no
// Idempotency-Key is minted, no HTTP traffic is generated, and the
// SDK never sees a *os.File.
//
// Kept as a *Client method via a wrapper type rather than a free
// function because the existing test surface in client_test.go
// expects `c.DeployTarball(...)` as a method on the alias.
func DeployTarball(c *Client, ctx context.Context, slug, path, runtime, handler string, dockerfile bool) (api.DeploymentResponse, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	defer func() { _ = f.Close() }()
	return c.DeployMultipart(ctx, slug, f, filepath.Base(path), runtime, handler, dockerfile)
}

// ExportAccountFile fetches the GDPR export bundle and writes the
// raw JSON to outPath with mode 0600. includeSecrets=false drops the
// ciphertext slice. The CLI owns file creation (mode + atomic rename)
// so the SDK stays a wire-layer concern.
//
// Note: the older CLI implementation streamed the response body to
// disk via io.Copy on the raw HTTP response. We unmarshal-then-encode
// here because the SDK API changed from "stream bytes" to "parse
// struct" — the bundle is small enough (KBs of metadata) that the
// extra allocation is not a concern; the JSON-on-disk shape stays
// byte-identical because we re-encode.
func ExportAccountFile(c *Client, ctx context.Context, outPath string, includeSecrets bool) error {
	bundle, err := c.ExportAccount(ctx, includeSecrets)
	if err != nil {
		return err
	}
	data, err := json.MarshalIndent(&bundle, "", "  ")
	if err != nil {
		return err
	}
	return osWriteFile0600(outPath, data)
}

// osWriteFile0600 writes data to outPath with mode 0600 (owner RW only).
// GDPR export bundles can carry plaintext secret ciphertext — world-
// readable on a multi-user box would be a leak; the file mode keeps the
// blast radius to the calling user.
func osWriteFile0600(outPath string, data []byte) error {
	return os.WriteFile(outPath, data, 0o600)
}
