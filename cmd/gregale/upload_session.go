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
	"github.com/onebox-faas/faas/pkg/reqbudget"
)

const (
	resumableUploadMaxChunkSize = 64 * 1024 * 1024
	resumableUploadMaxAttempts  = 5
	resumableUploadMaxRestarts  = 2
	resumableUploadCancelWait   = 2 * time.Second
	resumableUploadConfirmWait  = 2 * time.Second

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
		len(ann.OverrideEnv) == 0 &&
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
	return deployResumableTarball(c, ctx, slug, path, progress, nil, options...)
}

func deployResumableTarball(c *Client, ctx context.Context, slug, path string, progress resumableUploadProgress, observe func(deployUploadIdentity) error, options ...api.UploadDeployOptions) (dep api.DeploymentResponse, sourceSHA256 string, supported bool, err error) {
	phase := "upload"
	outcome := deploySubmissionRejected
	defer func() {
		if err != nil {
			var submission *deploySubmissionError
			if !errors.As(err, &submission) {
				err = submissionError(outcome, err)
			}
			err = &deployPhaseError{stage: phase, err: err}
		}
	}()
	if _, err := validateConfigAPIBase(c.BaseURL()); err != nil {
		return api.DeploymentResponse{}, "", true, errors.New("upload recovery API endpoint cannot contain credentials, query parameters or fragments")
	}

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
	identity := uploadRecoveryIdentity(c, ctx, slug, archiveSHA256, optionsSHA256)
	statePath, err := uploadStatePath(identity)
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
	if stateFound && !uploadStateMatches(state, c, slug, identity, info.Size(), archiveSHA256, optionsSHA256) {
		// An inconsistent record is not proof of ownership of its upload ID.
		// Discard local metadata without cancelling an unrelated server session.
		if !jsonOutput {
			PrintWarn(osStderr, "upload recovery state does not match this archive or deploy options; starting a new upload")
		}
		removeResumableUploadState(statePath)
		state = resumableUploadState{}
		stateFound = false
	}

	if stateFound {
		if state.CommitAttempted {
			phase, outcome = "submission", deploySubmissionUnknown
		}
		if err := saveSessionUpload(ctx, c, state); err != nil {
			return api.DeploymentResponse{}, "", true, fmt.Errorf("save deploy upload identity: %w", err)
		}
	}

	for restart := 0; restart <= resumableUploadMaxRestarts; restart++ {
		phase = "upload"
		var session api.ResumableUploadSession
		statePersisted := false
		if restart == 0 && stateFound {
			// A saved session may already have committed a deployment.
			phase = "submission"
			outcome = deploySubmissionUnknown
			if observe != nil {
				if err := observe(deployUploadIdentity{UploadID: state.UploadID, TotalSize: state.TotalSize}); err != nil {
					return api.DeploymentResponse{}, "", true, fmt.Errorf("save upload identity: %w", err)
				}
			}
			remote, discoverErr := c.GetUploadSession(ctx, state.UploadID)
			if discoverErr == nil {
				if observe != nil {
					if remote.UploadID != state.UploadID || remote.AppSlug != slug || remote.TotalSize != info.Size() {
						return api.DeploymentResponse{}, "", true, errors.New("discovered upload does not match its recorded identity")
					}
					if remote.Status == "" || (remote.DeploymentID != nil && *remote.DeploymentID != "" && remote.Status != "committed" && remote.Status != "complete" && remote.Status != "completed") {
						return api.DeploymentResponse{}, "", true, errors.New("discovered upload has contradictory or missing acceptance evidence")
					}
				}
				switch strings.ToLower(remote.Status) {
				case "committed", "complete", "completed":
					if err := validateDiscoveredUpload(state, remote, info.Size()); err != nil {
						return api.DeploymentResponse{}, "", true, err
					}
					outcome = deploySubmissionAccepted
					state.CommitAttempted = true
					persistUploadCommitAttempt(statePath, state)
					if remote.DeploymentID == nil || *remote.DeploymentID == "" {
						return api.DeploymentResponse{}, "", true, errors.New("upload session is committed without a deployment id")
					}
					if session := sessionFromContext(ctx); session != nil {
						if err := session.bindCommittedCronUpload(ctx, c, state.UploadID, *remote.DeploymentID); err != nil {
							return api.DeploymentResponse{ID: *remote.DeploymentID}, "", true, err
						}
					}
					phase = "deployment"
					dep, err := c.GetDeployment(ctx, *remote.DeploymentID)
					if err != nil {
						return api.DeploymentResponse{ID: *remote.DeploymentID}, "", true, fmt.Errorf("recover committed upload deployment: %w", err)
					}
					removeResumableUploadState(statePath)
					return dep, archiveSHA256, true, nil
				case "open", "":
					if err := validateDiscoveredUpload(state, remote, info.Size()); err != nil {
						if state.CommitAttempted {
							return api.DeploymentResponse{}, "", true, err
						}
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
					if !state.CommitAttempted {
						outcome = deploySubmissionRejected
					}
				case "expired", "cancelled", "canceled":
					if state.CommitAttempted {
						return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session is %s after an uncertain commit; inspect deployment history before starting another upload", remote.Status)
					}
					outcome = deploySubmissionRejected
					// Keep the ended session ID until its replacement is saved.
					// It scopes a fresh start key, even after another interruption.
					stateFound = false
				default:
					return api.DeploymentResponse{}, "", true, fmt.Errorf("upload session returned unknown status %q", remote.Status)
				}
			} else if isUploadSessionRestart(discoverErr) {
				if state.CommitAttempted {
					return api.DeploymentResponse{}, "", true, fmt.Errorf("discover upload after an uncertain commit: %w", discoverErr)
				}
				// Guided workflows journal the upload identity; a missing
				// session cannot prove their submission was rejected.
				if observe != nil {
					return api.DeploymentResponse{}, "", true, fmt.Errorf("saved upload outcome is unconfirmed: %w", discoverErr)
				}
				outcome = deploySubmissionRejected
				stateFound = false
			} else {
				return api.DeploymentResponse{}, "", true, fmt.Errorf("discover upload session: %w", discoverErr)
			}
		}

		phase = "upload"
		if session.UploadID == "" {
			if restart > 0 {
				if _, err := f.Seek(0, io.SeekStart); err != nil {
					return api.DeploymentResponse{}, "", true, fmt.Errorf("rewind tarball: %w", err)
				}
				if !jsonOutput {
					PrintWarn(osStderr, "upload session ended; restarting source upload (%d/%d)", restart, resumableUploadMaxRestarts)
				}
			}
			startCtx := ctx
			if baseKey := api.IdempotencyKeyFromContext(ctx); baseKey != "" {
				operation := "resumable-start"
				if state.UploadID != "" {
					operation += "-after-" + state.UploadID
				}
				startCtx = api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(baseKey, operation))
			}
			session, err = c.StartUpload(startCtx, slug, info.Size(), archiveSHA256, options...)
			if isUploadSessionCap(err) && reclaimAppUploadSessions(ctx, c, slug, statePath) > 0 {
				if !jsonOutput {
					PrintWarn(osStderr, "cancelled earlier unfinished uploads for %s that this deploy cannot resume; retrying", slug)
				}
				session, err = c.StartUpload(startCtx, slug, info.Size(), archiveSHA256, options...)
			}
			if err != nil {
				if errors.Is(err, api.ErrResumableUploadUnsupported) {
					return api.DeploymentResponse{}, "", false, nil
				}
				return api.DeploymentResponse{}, "", true, err
			}
			state = resumableUploadState{
				Version: resumableUploadStateVersion, UploadID: session.UploadID,
				AppSlug: slug, APIBase: c.BaseURL(), RecoveryIdentitySHA256: identity,
				ArchivePath: archivePath, ArchiveSize: info.Size(), ArchiveSHA256: archiveSHA256,
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

		if err := saveSessionUpload(ctx, c, state); err != nil {
			return api.DeploymentResponse{}, "", true, fmt.Errorf("save deploy upload identity: %w", err)
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
		if observe != nil {
			if err := observe(deployUploadIdentity{UploadID: session.UploadID, TotalSize: session.TotalSize}); err != nil {
				return api.DeploymentResponse{}, "", true, fmt.Errorf("save upload identity: %w", err)
			}
		}
		err = uploadSessionChunks(ctx, c, session, f, progress)
		if err != nil {
			if isUploadProblem(err, uploadSessionAlreadyCommittedCode) {
				// Start may replay a session whose successful checkpoint was
				// already cleared. Recover its deployment rather than cancel it.
				phase = "deployment"
				state.CommitAttempted = true
				if statePersisted {
					persistUploadCommitAttempt(statePath, state)
				}
				dep, recoverErr := recoverCommittedUpload(ctx, c, err)
				if recoverErr == nil {
					removeResumableUploadState(statePath)
				}
				return dep, archiveSHA256, true, recoverErr
			}
			if ctx.Err() != nil {
				return api.DeploymentResponse{}, "", true, ctx.Err()
			}
			if state.CommitAttempted {
				return api.DeploymentResponse{}, "", true, submissionError(deploySubmissionUnknown, err)
			}
			if isUploadSessionRestart(err) {
				if restart < resumableUploadMaxRestarts {
					continue
				}
			} else if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) && !retryableUploadError(err) {
				cancelUploadBestEffort(ctx, c, session.UploadID)
				if statePersisted {
					removeResumableUploadState(statePath)
				}
			}
			return api.DeploymentResponse{}, "", true, err
		}

		phase = "submission"
		if ctx.Err() != nil {
			return api.DeploymentResponse{}, "", true, ctx.Err()
		}
		priorCommitAttempted := state.CommitAttempted
		state.CommitAttempted = true
		if statePersisted {
			// Do not leave a saved "never committed" record behind if this
			// attempt could admit a build. A later process might replace it.
			if err := saveResumableUploadState(statePath, state); err != nil {
				return api.DeploymentResponse{}, "", true, fmt.Errorf("record upload commit attempt: %w", err)
			}
		}
		dep, err := commitUploadWithRetry(ctx, c, session.UploadID)
		if err == nil {
			removeResumableUploadState(statePath)
			return dep, archiveSHA256, true, nil
		}
		commitOutcome := submissionOutcome(dep, err)
		if priorCommitAttempted && commitOutcome == deploySubmissionRejected {
			commitOutcome = deploySubmissionUnknown
			err = submissionError(commitOutcome, err)
		}
		if commitOutcome == deploySubmissionUnknown {
			dep, err = reconcileUploadCommit(ctx, c, state, err)
			return dep, "", true, err
		}
		if commitOutcome == deploySubmissionRejected {
			state.CommitAttempted = false
			if statePersisted {
				persistUploadCommitAttempt(statePath, state)
			}
		}
		if commitOutcome == deploySubmissionRejected && isUploadSessionRestart(err) && restart < resumableUploadMaxRestarts {
			stateFound = false
			continue
		}
		return dep, "", true, err
	}
	return api.DeploymentResponse{}, "", true, errors.New("resumable upload exhausted its restart budget")
}

func persistUploadCommitAttempt(statePath string, state resumableUploadState) {
	if err := saveResumableUploadState(statePath, state); err != nil && !jsonOutput {
		PrintWarn(osStderr, "could not save upload commit recovery state: %v", err)
	}
}

// A positive receipt confirms acceptance. Negative reads cannot disprove it:
// admission can precede updating the upload row, or still be running remotely.
func reconcileUploadCommit(ctx context.Context, c *Client, state resumableUploadState, commitErr error) (api.DeploymentResponse, error) {
	confirmCtx, cancel := context.WithTimeout(reqbudget.WithoutBudget(context.WithoutCancel(ctx)), resumableUploadConfirmWait)
	defer cancel()
	remote, err := c.GetUploadSession(confirmCtx, state.UploadID)
	if err == nil && validateDiscoveredUpload(state, remote, state.TotalSize) == nil {
		switch strings.ToLower(remote.Status) {
		case "committed", "complete", "completed":
			dep := api.DeploymentResponse{}
			if remote.DeploymentID != nil {
				dep.ID = *remote.DeploymentID
			}
			return dep, submissionError(deploySubmissionAccepted, commitErr)
		}
	}
	return api.DeploymentResponse{}, submissionError(deploySubmissionUnknown, commitErr)
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

func uploadStateMatches(state resumableUploadState, c *Client, slug, identity string, size int64, archiveSHA256, optionsSHA256 string) bool {
	return state.Version == resumableUploadStateVersion && state.UploadID != "" &&
		state.AppSlug == slug && state.APIBase == c.BaseURL() && state.RecoveryIdentitySHA256 == identity &&
		state.ArchiveSize == size && state.DeployOptionsSHA256 == optionsSHA256 && state.ArchiveSHA256 == archiveSHA256
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
		appendCtx := ctx
		if baseKey := api.IdempotencyKeyFromContext(ctx); baseKey != "" {
			operation := "resumable-append-" + session.UploadID + "-" + strconv.FormatInt(offset, 10)
			appendCtx = api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(baseKey, operation))
		}
		next, err := appendUploadWithRetry(appendCtx, c, session.UploadID, offset, chunk)
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
	commitCtx := ctx
	if baseKey := api.IdempotencyKeyFromContext(ctx); baseKey != "" {
		commitCtx = api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(baseKey, "resumable-commit-"+uploadID))
	}
	outcome := deploySubmissionRejected
	for attempt := 0; attempt < resumableUploadMaxAttempts; attempt++ {
		dep, err := c.CommitUpload(commitCtx, uploadID)
		if err == nil {
			return dep, nil
		}
		if isUploadProblem(err, uploadSessionAlreadyCommittedCode) {
			return recoverCommittedUpload(ctx, c, err)
		}
		if submissionOutcome(dep, err) == deploySubmissionAccepted {
			return dep, submissionError(deploySubmissionAccepted, err)
		}
		if submissionOutcome(dep, err) == deploySubmissionUnknown {
			outcome = deploySubmissionUnknown
		}
		if isUploadSessionRestart(err) || !retryableUploadError(err) || attempt == resumableUploadMaxAttempts-1 {
			return api.DeploymentResponse{}, submissionError(outcome, err)
		}
		if err := waitForUploadRetry(ctx, err, attempt); err != nil {
			return api.DeploymentResponse{}, submissionError(outcome, err)
		}
	}
	return api.DeploymentResponse{}, errors.New("upload commit exhausted its retry budget")
}

func recoverCommittedUpload(ctx context.Context, c *Client, committedErr error) (dep api.DeploymentResponse, err error) {
	defer func() { err = submissionError(deploySubmissionAccepted, err) }()
	deploymentID := committedDeploymentID(committedErr)
	if deploymentID == "" {
		return api.DeploymentResponse{}, fmt.Errorf("upload committed but response did not include a deployment id: %w", committedErr)
	}
	dep, err = c.GetDeployment(ctx, deploymentID)
	if err != nil {
		return api.DeploymentResponse{ID: deploymentID}, err
	}
	return dep, nil
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
	if baseKey := api.IdempotencyKeyFromContext(ctx); baseKey != "" {
		ctx = api.ContextWithIdempotencyKey(ctx, deployOperationIdempotencyKey(baseKey, "resumable-cancel-"+uploadID))
	}
	_ = c.CancelUpload(ctx, uploadID)
}
