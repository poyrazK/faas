package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	resumableUploadMaxChunkSize = 64 * 1024 * 1024
	resumableUploadMaxAttempts  = 5
	resumableUploadMaxRestarts  = 2
	resumableUploadCancelWait   = 2 * time.Second

	// These codes are owned by the server upload-session contract. Keep the
	// client copy private so this PR can merge independently of that server PR.
	uploadSessionNotFoundCode         = "upload_session_not_found"
	uploadSessionOffsetConflictCode   = "upload_session_offset_conflict"
	uploadSessionAlreadyCommittedCode = "upload_session_already_committed"
	uploadSessionExpiredCode          = "upload_session_expired"
)

var resumableOffsetRE = regexp.MustCompile(`server is at ([0-9]+)`)

// resumableUploadProgress receives the acknowledged byte count, never the
// bytes merely read from disk. That makes the progress line truthful when a
// request is retried or the server has to resume from a CAS conflict.
type resumableUploadProgress func(uploaded, total int64)

// canUseResumableUpload reports whether the resumable session can represent
// this deploy. Metadata is persisted with the session; traffic/canary remain
// on the legacy path until the rollout mutation is made transactional with
// upload commit.
func canUseResumableUpload(sh shape, runtime, handler string, dockerfile bool, sourceRoot string, ann api.DeployAnnotations, trafficPercent int, canaryPreset, canaryStages string) bool {
	return (sh == shapeApp || sh == shapeFunction) &&
		trafficPercent < 0 &&
		canaryPreset == "" &&
		canaryStages == ""
}

// DeployResumableTarball streams a local archive through the resumable upload
// protocol. supported=false means the API is an older server and the caller
// should use the legacy multipart endpoint. The archive is never buffered in
// memory; only the server-selected chunk and the SHA-256 state are retained.
func DeployResumableTarball(c *Client, ctx context.Context, slug, path string, progress resumableUploadProgress, options ...api.UploadDeployOptions) (dep api.DeploymentResponse, sourceSHA256 string, supported bool, err error) {
	f, err := openCustomerFile(path)
	if err != nil {
		return api.DeploymentResponse{}, "", true, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return api.DeploymentResponse{}, "", true, fmt.Errorf("stat tarball: %w", err)
	}
	if info.Size() <= 0 {
		return api.DeploymentResponse{}, "", true, errors.New("tarball is empty")
	}

	for restart := 0; restart <= resumableUploadMaxRestarts; restart++ {
		if restart > 0 {
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return api.DeploymentResponse{}, "", true, fmt.Errorf("rewind tarball: %w", err)
			}
			if !jsonOutput {
				PrintWarn(osStderr, "upload session ended; restarting source upload (%d/%d)", restart, resumableUploadMaxRestarts)
			}
		}

		session, err := c.StartUpload(ctx, slug, info.Size(), "", options...)
		if err != nil {
			if errors.Is(err, api.ErrResumableUploadUnsupported) {
				return api.DeploymentResponse{}, "", false, nil
			}
			return api.DeploymentResponse{}, "", true, err
		}
		if session.TotalSize != info.Size() {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session size mismatch: server=%d local=%d", session.TotalSize, info.Size())
		}
		if session.ChunkSize <= 0 || session.ChunkSize > resumableUploadMaxChunkSize {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session returned invalid chunk size %d", session.ChunkSize)
		}

		digest, err := uploadSessionChunks(ctx, c, session, f, progress)
		if err != nil {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			if ctx.Err() != nil {
				return api.DeploymentResponse{}, "", true, ctx.Err()
			}
			if isUploadSessionRestart(err) && restart < resumableUploadMaxRestarts {
				continue
			}
			return api.DeploymentResponse{}, "", true, err
		}

		dep, err := commitUploadWithRetry(ctx, c, session.UploadID)
		if err == nil {
			return dep, digest, true, nil
		}
		if ctx.Err() != nil {
			return api.DeploymentResponse{}, "", true, ctx.Err()
		}
		if isUploadSessionRestart(err) && restart < resumableUploadMaxRestarts {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			continue
		}
		return api.DeploymentResponse{}, "", true, err
	}
	return api.DeploymentResponse{}, "", true, errors.New("resumable upload exhausted its restart budget")
}

func uploadSessionChunks(ctx context.Context, c *Client, session api.ResumableUploadSession, f *os.File, progress resumableUploadProgress) (string, error) {
	h := sha256.New()
	buf := make([]byte, int(session.ChunkSize))
	offset := int64(0)
	for offset < session.TotalSize {
		want := session.TotalSize - offset
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		n, readErr := io.ReadFull(f, buf[:int(want)])
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return "", fmt.Errorf("read tarball at offset %d: %w", offset, readErr)
		}
		if int64(n) != want {
			return "", fmt.Errorf("tarball changed during upload: read %d bytes at offset %d, wanted %d", n, offset, want)
		}

		chunk := buf[:n]
		next, err := appendUploadWithRetry(ctx, c, session.UploadID, offset, chunk)
		if err != nil {
			return "", err
		}
		_, _ = h.Write(chunk)
		offset = next
		if progress != nil {
			progress(offset, session.TotalSize)
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func appendUploadWithRetry(ctx context.Context, c *Client, uploadID string, offset int64, chunk []byte) (int64, error) {
	for attempt := 0; attempt < resumableUploadMaxAttempts; attempt++ {
		next, err := c.AppendUpload(ctx, uploadID, offset, chunk)
		if err == nil {
			return next, nil
		}
		if current, ok := currentUploadOffset(err); ok {
			expected := offset + int64(len(chunk))
			switch current {
			case expected:
				// The server accepted the chunk but the response was
				// lost. Treat the CAS result as the acknowledgment.
				return current, nil
			case offset:
				// The server is still at our offset; a retry is safe.
			default:
				return 0, fmt.Errorf("upload session offset diverged: local=%d server=%d", offset, current)
			}
		}
		if !retryableUploadError(err) || attempt == resumableUploadMaxAttempts-1 {
			return 0, err
		}
		if err := waitForUploadRetry(ctx, err, attempt); err != nil {
			return 0, err
		}
	}
	return 0, errors.New("upload append exhausted its retry budget")
}

func commitUploadWithRetry(ctx context.Context, c *Client, uploadID string) (api.DeploymentResponse, error) {
	for attempt := 0; attempt < resumableUploadMaxAttempts; attempt++ {
		dep, err := c.CommitUpload(ctx, uploadID)
		if err == nil {
			return dep, nil
		}
		if isUploadProblem(err, uploadSessionAlreadyCommittedCode) {
			deploymentID := committedDeploymentID(err)
			if deploymentID == "" {
				return api.DeploymentResponse{}, fmt.Errorf("upload committed but response did not include a deployment id: %w", err)
			}
			return c.GetDeployment(ctx, deploymentID)
		}
		if isUploadSessionRestart(err) || !retryableUploadError(err) || attempt == resumableUploadMaxAttempts-1 {
			return api.DeploymentResponse{}, err
		}
		if err := waitForUploadRetry(ctx, err, attempt); err != nil {
			return api.DeploymentResponse{}, err
		}
	}
	return api.DeploymentResponse{}, errors.New("upload commit exhausted its retry budget")
}

func currentUploadOffset(err error) (int64, bool) {
	var ae *api.APIError
	if !errors.As(err, &ae) || ae.Problem.Code != uploadSessionOffsetConflictCode {
		return 0, false
	}
	match := resumableOffsetRE.FindStringSubmatch(ae.Problem.Detail)
	if len(match) != 2 {
		return 0, false
	}
	n, parseErr := strconv.ParseInt(match[1], 10, 64)
	return n, parseErr == nil && n >= 0
}

func committedDeploymentID(err error) string {
	var ae *api.APIError
	if !errors.As(err, &ae) {
		return ""
	}
	fields := strings.Fields(ae.Problem.Detail)
	for i := 0; i+1 < len(fields); i++ {
		field := fields[i]
		if field == "deployment" {
			return strings.Trim(fields[i+1], ".,;:)")
		}
	}
	return ""
}

func isUploadSessionRestart(err error) bool {
	return isUploadProblem(err, uploadSessionExpiredCode) ||
		isUploadProblem(err, uploadSessionNotFoundCode)
}

func isUploadProblem(err error, code string) bool {
	var ae *api.APIError
	return errors.As(err, &ae) && ae.Problem.Code == code
}

func retryableUploadError(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *api.APIError
	if !errors.As(err, &ae) {
		// Transport and response-shape errors are safe to retry for a
		// chunk: the offset CAS makes a repeated PATCH idempotent.
		return true
	}
	switch ae.Problem.Status {
	case 408, 425, 429, 500, 502, 503, 504:
		return true
	default:
		return false
	}
}

func waitForUploadRetry(ctx context.Context, err error, attempt int) error {
	delay := time.Duration(1<<attempt) * 250 * time.Millisecond
	var ae *api.APIError
	if errors.As(err, &ae) {
		if values := ae.Problem.HasHeader("Retry-After"); len(values) > 0 {
			if seconds, parseErr := strconv.Atoi(values[0]); parseErr == nil && seconds > 0 {
				delay = time.Duration(seconds) * time.Second
			}
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func cancelUploadBestEffort(ctx context.Context, c *Client, uploadID string) {
	if uploadID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resumableUploadCancelWait)
	defer cancel()
	_ = c.CancelUpload(ctx, uploadID)
}
