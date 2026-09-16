package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
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
//     security boundary belongs in cmd/gregale, not pkg/api — moving
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
func DeployTarball(c *Client, ctx context.Context, slug, path, runtime, handler string, dockerfile bool, ann api.DeployAnnotations) (api.DeploymentResponse, error) {
	return DeployTarballWithSourceRoot(c, ctx, slug, path, runtime, handler, dockerfile, "", ann)
}

// DeployTarballWithSourceRoot is the workspace-aware counterpart to
// DeployTarball. sourceRoot is relative to the uploaded repository context;
// an empty value preserves the legacy archive-root behavior.
func DeployTarballWithSourceRoot(c *Client, ctx context.Context, slug, path, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations) (api.DeploymentResponse, error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	defer func() { _ = f.Close() }()
	return c.DeployMultipartWithSourceRoot(ctx, slug, f, filepath.Base(path), runtime, handler, dockerfile, sourceRoot, ann)
}

// DeployDevSourceTarball preserves the CLI's symlink-safe customer-file
// boundary while calling the SDK's developer source transport.
func DeployDevSourceTarball(c *Client, ctx context.Context, slug, sourcePath, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations, baseRevision, targetRevision string, deleted []string) (api.DeploymentResponse, error) {
	f, err := openCustomerFile(sourcePath)
	if err != nil {
		return api.DeploymentResponse{}, err
	}
	defer func() { _ = f.Close() }()
	return c.DeployDevSource(ctx, slug, f, filepath.Base(sourcePath), runtime, handler, dockerfile, sourceRoot, ann, baseRevision, targetRevision, deleted)
}

// ExportAccountFile fetches the GDPR export bundle and writes the
// raw JSON to outPath with mode 0600. includeSecrets=false drops the
// ciphertext slice. The CLI owns file creation (mode + atomic rename)
// so the SDK stays a wire-layer concern.
func ExportAccountFile(c *Client, ctx context.Context, outPath string, includeSecrets bool) error {
	dir := filepath.Dir(outPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create export directory: %w", err)
	}
	requestID := newExportRequestID()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		tmp, err := os.CreateTemp(dir, ".gregale-export-*.tmp")
		if err != nil {
			return fmt.Errorf("create export file: %w", err)
		}
		tmpPath := tmp.Name()
		cleanup := func() {
			_ = tmp.Close()
			_ = os.Remove(tmpPath)
		}
		if err := tmp.Chmod(0o600); err != nil {
			cleanup()
			return fmt.Errorf("secure export file: %w", err)
		}

		_, streamErr := c.StreamAccountExport(ctx, includeSecrets, requestID, tmp)
		if streamErr != nil {
			cleanup()
			lastErr = streamErr
			if attempt == 0 && retryableExportTransfer(streamErr) {
				continue
			}
			break
		}
		if _, err := tmp.Seek(0, io.SeekStart); err != nil {
			cleanup()
			return fmt.Errorf("rewind export: %w", err)
		}
		if err := validateSingleJSONDocument(tmp); err != nil {
			cleanup()
			return err
		}
		if err := tmp.Sync(); err != nil {
			cleanup()
			return fmt.Errorf("sync export: %w", err)
		}
		if err := tmp.Close(); err != nil {
			cleanup()
			return fmt.Errorf("close export: %w", err)
		}
		if err := os.Rename(tmpPath, outPath); err != nil {
			cleanup()
			return fmt.Errorf("commit export: %w", err)
		}
		return nil
	}
	return lastErr
}

func newExportRequestID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	return fmt.Sprintf("gregale-export-%d", time.Now().UTC().UnixNano())
}

func retryableExportTransfer(err error) bool {
	var truncated *api.ResponseTruncatedError
	var apiErr *api.APIError
	return errors.As(err, &truncated) ||
		(!errors.As(err, &apiErr) && (strings.Contains(err.Error(), "could not reach the API") ||
			strings.Contains(err.Error(), "stream account export")))
}

func validateSingleJSONDocument(r io.Reader) error {
	dec := json.NewDecoder(r)
	depth, roots := 0, 0
	for {
		tok, err := dec.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("validate export JSON: %w", err)
		}
		if delim, ok := tok.(json.Delim); ok {
			switch delim {
			case '{', '[':
				if depth == 0 {
					roots++
					if delim != '{' {
						return errors.New("validate export JSON: root must be an object")
					}
				}
				depth++
			case '}', ']':
				depth--
				if depth < 0 {
					return errors.New("validate export JSON: unbalanced document")
				}
			}
		} else if depth == 0 {
			roots++
		}
	}
	if depth != 0 || roots != 1 {
		return fmt.Errorf("validate export JSON: expected one complete object, found %d", roots)
	}
	return nil
}
