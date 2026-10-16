package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/ci"
)

func (c *operatorHTTPClient) downloadCIArtifact(ctx context.Context, o ciExecutionOptions, path string) error {
	var row ci.Execution
	if err := c.doJSON(ctx, http.MethodGet, "/v1/internal/ci/executions/"+o.id, nil, &row, false, nil); err != nil {
		return err
	}
	var ref *ci.ArtifactRef
	for _, job := range row.Jobs {
		if job.Name == o.job && job.Attempt != nil {
			for _, item := range job.Attempt.Artifacts {
				if item.Path == o.artifact {
					copy := item
					ref = &copy
				}
			}
		}
	}
	if ref == nil || ref.Bytes < 0 || ref.Bytes > api.CIArtifactMaxBytes {
		return fmt.Errorf("artifact is not present in verified execution output")
	}
	if _, err := os.Lstat(o.output); !os.IsNotExist(err) {
		return fmt.Errorf("artifact output must be a new file")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", operatorUserAgent)
	if c.session != nil {
		req.AddCookie(&http.Cookie{Name: "faas_sid", Value: c.session.Cookie})
	}
	response, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("download CI artifact: %w", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		var problem api.Problem
		_ = json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&problem)
		return &operatorHTTPError{Status: response.StatusCode, Code: problem.Code, Detail: problem.Detail}
	}
	if response.ContentLength != ref.Bytes || response.Header.Get(ci.RuntimeDigestHeader) != ref.Digest {
		return fmt.Errorf("artifact response disagrees with verified execution metadata")
	}
	return saveCIArtifact(response.Body, o.output, *ref)
}

func saveCIArtifact(body io.Reader, destination string, ref ci.ArtifactRef) error {
	file, err := os.CreateTemp(filepath.Dir(destination), ".gregale-ci-artifact-*")
	if err != nil {
		return fmt.Errorf("create artifact file: %w", err)
	}
	defer func() { _ = file.Close(); _ = os.Remove(file.Name()) }()
	hash := sha256.New()
	count, err := io.Copy(io.MultiWriter(file, hash), io.LimitReader(body, ref.Bytes+1))
	if err != nil {
		return fmt.Errorf("read artifact bytes: %w", err)
	}
	if count != ref.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != ref.Digest {
		return fmt.Errorf("artifact checksum or byte count mismatch")
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Linking the fully verified private file publishes it atomically and refuses
	// an existing destination, including a symlink created during the download.
	if err := os.Link(file.Name(), destination); err != nil {
		return fmt.Errorf("publish new artifact file: %w", err)
	}
	return nil
}
