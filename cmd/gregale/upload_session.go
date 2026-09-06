package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
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
// should use the legacy multipart endpoint. A small local state record keeps
// the server session alive across CLI process restarts; the archive itself is
// never copied to the local state directory.
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
	archivePath, err := filepath.Abs(path)
	if err != nil {
		return api.DeploymentResponse{}, "", true, fmt.Errorf("absolutize tarball: %w", err)
	}
	archiveSHA256, err := hashUploadArchive(f)
	if err != nil {
		return api.DeploymentResponse{}, "", true, err
	}
	optionsSHA256, err := deployOptionsFingerprint(options)
	if err != nil {
		return api.DeploymentResponse{}, "", true, err
	}
	statePath, err := uploadStatePath(slug, archivePath)
	if err != nil {
		return api.DeploymentResponse{}, "", true, err
	}
	stateLock, err := lockResumableUploadState(ctx, statePath)
	if err != nil {
		return api.DeploymentResponse{}, "", true, err
	}
	defer func() {
		_ = stateLock.Unlock()
		_ = stateLock.Close()
	}()

	state, stateFound, stateErr := loadResumableUploadState(statePath)
	if stateErr != nil {
		// A corrupt local record cannot safely be resumed. It is only
		// recovery metadata, so discard it and start a clean session.
		if stateFound {
			removeResumableUploadState(statePath)
		}
		stateFound = false
		if !jsonOutput {
			PrintWarn(osStderr, "ignoring invalid upload recovery state: %v", stateErr)
		}
	}
	if stateFound && !uploadStateMatches(state, slug, archivePath, info, archiveSHA256, optionsSHA256) {
		// Do not leave an old spool consuming the account's upload budget
		// when the same local path now contains a different archive.
		if !jsonOutput {
			PrintWarn(osStderr, "upload recovery state does not match this archive or deploy options; starting a new upload")
		}
		cancelUploadBestEffort(ctx, c, state.UploadID)
		removeResumableUploadState(statePath)
		stateFound = false
	}

	for restart := 0; restart <= resumableUploadMaxRestarts; restart++ {
		var session api.ResumableUploadSession
		statePersisted := false
		if restart == 0 && stateFound {
			remote, discoverErr := c.GetUploadSession(ctx, state.UploadID)
			if discoverErr == nil {
				switch strings.ToLower(remote.Status) {
				case "committed", "complete", "completed":
					if remote.DeploymentID == nil || *remote.DeploymentID == "" {
						return api.DeploymentResponse{}, "", true, errors.New("upload session is committed without a deployment id")
					}
					dep, err := c.GetDeployment(ctx, *remote.DeploymentID)
					if err != nil {
						return api.DeploymentResponse{}, "", true, fmt.Errorf("recover committed upload deployment: %w", err)
					}
					removeResumableUploadState(statePath)
					return dep, archiveSHA256, true, nil
				case "open", "":
					if err := validateDiscoveredUpload(state, remote, info.Size()); err != nil {
						cancelUploadBestEffort(ctx, c, state.UploadID)
						removeResumableUploadState(statePath)
						stateFound = false
					} else {
						session = api.ResumableUploadSession{
							UploadID: remote.UploadID, AppSlug: remote.AppSlug,
							ChunkSize: int64(remote.ChunkSize), TotalSize: remote.TotalSize,
							ReceivedBytes: remote.ReceivedBytes, Status: remote.Status,
							ExpiresAt: remote.ExpiresAt, DeploymentID: remote.DeploymentID,
						}
						statePersisted = true
					}
				default:
					removeResumableUploadState(statePath)
					stateFound = false
				}
			} else if isUploadSessionRestart(discoverErr) {
				removeResumableUploadState(statePath)
				stateFound = false
			} else {
				return api.DeploymentResponse{}, "", true, fmt.Errorf("discover upload session: %w", discoverErr)
			}
		}

		if session.UploadID == "" {
			if restart > 0 {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return api.DeploymentResponse{}, "", true, fmt.Errorf("rewind tarball: %w", err)
				}
				if !jsonOutput {
					PrintWarn(osStderr, "upload session ended; restarting source upload (%d/%d)", restart, resumableUploadMaxRestarts)
				}
			}
			session, err = c.StartUpload(ctx, slug, info.Size(), archiveSHA256, options...)
			if err != nil {
				if errors.Is(err, api.ErrResumableUploadUnsupported) {
					return api.DeploymentResponse{}, "", false, nil
				}
				return api.DeploymentResponse{}, "", true, err
			}
			state = resumableUploadState{
				Version: resumableUploadStateVersion, UploadID: session.UploadID,
				AppSlug: slug, APIBase: apiBase(), ArchivePath: archivePath, ArchiveSize: info.Size(),
				ArchiveSHA256: archiveSHA256, ArchiveModTimeUnixNano: info.ModTime().UnixNano(),
				DeployOptionsSHA256: optionsSHA256, TotalSize: session.TotalSize,
				ChunkSize: session.ChunkSize, CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := saveResumableUploadState(statePath, state); err != nil {
				if !jsonOutput {
					PrintWarn(osStderr, "upload recovery will not survive a CLI restart: %v", err)
				}
			} else {
				statePersisted = true
			}
		}

		if session.TotalSize != info.Size() {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			if statePersisted {
				removeResumableUploadState(statePath)
			}
			return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session size mismatch: server=%d local=%d", session.TotalSize, info.Size())
		}
		if session.ChunkSize <= 0 || session.ChunkSize > resumableUploadMaxChunkSize {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			if statePersisted {
				removeResumableUploadState(statePath)
			}
			return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session returned invalid chunk size %d", session.ChunkSize)
		}

		if session.ReceivedBytes > 0 && progress != nil {
			progress(session.ReceivedBytes, session.TotalSize)
		}
		err = uploadSessionChunks(ctx, c, session, f, progress)
		if err != nil {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			if statePersisted {
				removeResumableUploadState(statePath)
			}
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
			removeResumableUploadState(statePath)
			return dep, archiveSHA256, true, nil
		}
		if ctx.Err() != nil {
			return api.DeploymentResponse{}, "", true, ctx.Err()
		}
		if isUploadSessionRestart(err) && restart < resumableUploadMaxRestarts {
			cancelUploadBestEffort(ctx, c, session.UploadID)
			removeResumableUploadState(statePath)
			stateFound = false
			continue
		}
		return api.DeploymentResponse{}, "", true, err
	}
	return api.DeploymentResponse{}, "", true, errors.New("resumable upload exhausted its restart budget")
}

func hashUploadArchive(f *os.File) (string, error) {
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind tarball for fingerprint: %w", err)
	}
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("fingerprint tarball: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return "", fmt.Errorf("rewind tarball after fingerprint: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func uploadStateMatches(state resumableUploadState, slug, archivePath string, info os.FileInfo, archiveSHA256, optionsSHA256 string) bool {
	if state.Version != resumableUploadStateVersion || state.UploadID == "" || state.AppSlug != slug || state.APIBase != apiBase() || state.ArchivePath != archivePath || state.ArchiveSize != info.Size() || state.DeployOptionsSHA256 != optionsSHA256 {
		return false
	}
	if state.ArchiveSHA256 != "" {
		return strings.EqualFold(state.ArchiveSHA256, archiveSHA256)
	}
	return state.ArchiveModTimeUnixNano == info.ModTime().UnixNano()
}

func validateDiscoveredUpload(state resumableUploadState, remote api.UploadSessionResponse, localSize int64) error {
	if remote.UploadID == "" {
		return errors.New("server returned an empty upload id")
	}
	if remote.UploadID != state.UploadID {
		return fmt.Errorf("server returned upload id %q, want %q", remote.UploadID, state.UploadID)
	}
	if remote.AppSlug != "" && remote.AppSlug != state.AppSlug {
		return fmt.Errorf("server returned app %q, want %q", remote.AppSlug, state.AppSlug)
	}
	if remote.TotalSize != localSize || remote.TotalSize != state.TotalSize {
		return fmt.Errorf("server returned upload size %d, want %d", remote.TotalSize, localSize)
	}
	if remote.ChunkSize <= 0 || (state.ChunkSize > 0 && int64(remote.ChunkSize) != state.ChunkSize) {
		return fmt.Errorf("server returned chunk size %d, want %d", remote.ChunkSize, state.ChunkSize)
	}
	if remote.ReceivedBytes < 0 || remote.ReceivedBytes > remote.TotalSize {
		return fmt.Errorf("server returned received offset %d for size %d", remote.ReceivedBytes, remote.TotalSize)
	}
	return nil
}

func uploadSessionChunks(ctx context.Context, c *Client, session api.ResumableUploadSession, f *os.File, progress resumableUploadProgress) error {
	if session.ReceivedBytes < 0 || session.ReceivedBytes > session.TotalSize {
		return fmt.Errorf("upload session returned invalid received offset %d", session.ReceivedBytes)
	}
	if _, err := f.Seek(session.ReceivedBytes, io.SeekStart); err != nil {
		return fmt.Errorf("seek tarball to upload offset %d: %w", session.ReceivedBytes, err)
	}
	buf := make([]byte, int(session.ChunkSize))
	offset := session.ReceivedBytes
	for offset < session.TotalSize {
		want := session.TotalSize - offset
		if want > int64(len(buf)) {
			want = int64(len(buf))
		}
		n, readErr := io.ReadFull(f, buf[:int(want)])
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
			return fmt.Errorf("read tarball at offset %d: %w", offset, readErr)
		}
		if int64(n) != want {
			return fmt.Errorf("tarball changed during upload: read %d bytes at offset %d, wanted %d", n, offset, want)
		}

		chunk := buf[:n]
		next, err := appendUploadWithRetry(ctx, c, session.UploadID, offset, chunk)
		if err != nil {
			return err
		}
		offset = next
		if progress != nil {
			progress(offset, session.TotalSize)
		}
	}
	return nil
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
	if isUploadProblem(err, uploadSessionExpiredCode) || isUploadProblem(err, uploadSessionNotFoundCode) {
		return true
	}
	var ae *api.APIError
	return errors.As(err, &ae) && (ae.Problem.Status == http.StatusNotFound || ae.Problem.Status == http.StatusGone)
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
