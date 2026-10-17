package ci

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// SourceArchiveStreamer matches githubd's existing scoped RPC. No SCM token is
// returned. The verifier must recheck durable installation ownership and the
// exact immutable repository ID, including when githubd has a cached token.
type SourceArchiveStreamer interface {
	StreamSourceRef(context.Context, string, int64, string, string, int64) (io.ReadCloser, string, bool, int64, error)
}

type JobsBlobStore interface {
	Put(context.Context, string, io.Reader) error
	Get(context.Context, string) (io.ReadCloser, error)
}

type GitHubArchiveSource struct {
	Verify GitHubVerifier
	Stream SourceArchiveStreamer
	Blobs  JobsBlobStore
}

// Stage copies a bounded archive as opaque bytes. It never extracts source or
// invokes git/a shell on the host. The qualified guest runner must validate and
// extract it inside its VM with the same digest and a bounded expanded size.
func (s GitHubArchiveSource) Stage(ctx context.Context, req JobsSourceRequest) (SourceArchive, error) {
	if s.Verify == nil || s.Stream == nil || s.Blobs == nil || !validJobsSourceRequest(req) {
		return SourceArchive{}, ErrGitHubAccess
	}
	ctx, cancel := context.WithTimeout(ctx, time.Duration(api.CIGitHubRPCSeconds)*time.Second)
	defer cancel()
	if err := s.Verify(ctx, req.Key.AccountID, req.Binding.InstallationID, req.Binding.RepositoryID, req.Repository); err != nil {
		return SourceArchive{}, ErrGitHubAccess
	}
	if cache, ok := s.Blobs.(interface {
		SourceForAttempt(context.Context, JobsSourceRequest) (SourceArchive, bool, error)
	}); ok {
		archive, exists, err := cache.SourceForAttempt(ctx, req)
		if err != nil || exists {
			return archive, err
		}
	}
	body, sha, _, _, err := s.Stream.StreamSourceRef(ctx, req.Key.AccountID, req.Binding.InstallationID, req.Repository, req.CommitSHA, api.CIJobsSourceArchiveMaxBytes)
	if err != nil {
		return SourceArchive{}, ErrGitHubUnavailable
	}
	if body == nil {
		return SourceArchive{}, ErrGitHubUnavailable
	}
	defer func() { _ = body.Close() }()
	if sha != req.CommitSHA {
		return SourceArchive{}, protocolError("source commit did not match exact SHA")
	}
	file, err := os.CreateTemp("", "gregale-ci-source-*")
	if err != nil {
		return SourceArchive{}, errors.New("CI source staging unavailable")
	}
	defer func() { _ = os.Remove(file.Name()) }()
	defer func() { _ = file.Close() }()
	hash := sha256.New()
	count, err := copyJobsBlob(ctx, io.MultiWriter(file, hash), io.LimitReader(body, api.CIJobsSourceArchiveMaxBytes+1))
	if err != nil || count == 0 || count > api.CIJobsSourceArchiveMaxBytes {
		return SourceArchive{}, errors.New("CI source archive is unavailable or exceeds its bound")
	}
	digest := hex.EncodeToString(hash.Sum(nil))
	archive := SourceArchive{Key: jobsObjectPrefix(req.Key) + "source/" + digest + ".tar.gz", Digest: "sha256:" + digest, Bytes: count}
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return SourceArchive{}, errors.New("CI source staging unavailable")
	}
	if err := s.Blobs.Put(ctx, archive.Key, file); err != nil {
		return SourceArchive{}, errors.New("CI source publication unavailable")
	}
	return archive, nil
}

func validJobsSourceRequest(req JobsSourceRequest) bool {
	return ValidID(req.Key.AccountID) && ValidID(req.Key.ExecutionID) && ValidID(req.Key.AttemptID) &&
		ValidName(req.Key.JobName) && req.Key.Fence > 0 && req.Binding.AccountID == req.Key.AccountID &&
		req.Binding.Enabled && req.Binding.InstallationID > 0 && req.Binding.RepositoryID > 0 &&
		req.Binding.Repository == req.Repository && repoPattern.MatchString(req.Repository) && shaPattern.MatchString(req.CommitSHA)
}

func validateSourceArchive(key AttemptKey, archive SourceArchive) error {
	if !artifactDigestPattern.MatchString(archive.Digest) || archive.Bytes < 1 || archive.Bytes > api.CIJobsSourceArchiveMaxBytes ||
		archive.Key != jobsObjectPrefix(key)+"source/"+strings.TrimPrefix(archive.Digest, "sha256:")+".tar.gz" {
		return protocolError("invalid account-owned CI source archive")
	}
	return nil
}

type JobsArtifact struct {
	ArtifactRef
	Key string `json:"key"`
}

// JobsResultManifest describes the final guest result. The scoped runtime seals
// it in PostgreSQL for SealedJobsOutput; an uploaded result.json has no authority.
// BlobJobsOutput retains the separate object-based qualification harness.
type JobsResultManifest struct {
	Version      int            `json:"version"`
	AccountID    string         `json:"account_id"`
	ExecutionID  string         `json:"execution_id"`
	AttemptID    string         `json:"attempt_id"`
	CommitSHA    string         `json:"commit_sha"`
	Status       string         `json:"status"`
	Steps        []StepResult   `json:"steps"`
	ExitCode     *int           `json:"exit_code,omitempty"`
	Logs         SourceArchive  `json:"logs"`
	LogTruncated bool           `json:"log_truncated"`
	Artifacts    []JobsArtifact `json:"artifacts"`
}

type BlobJobsOutput struct{ Blobs JobsBlobStore }

func (s BlobJobsOutput) Collect(ctx context.Context, req JobsSourceRequest, job PlannedJob, status string, exit *int) (Observation, error) {
	if s.Blobs == nil || !validJobsSourceRequest(req) {
		return Observation{}, ErrGitHubAccess
	}
	raw, err := s.read(ctx, jobsObjectPrefix(req.Key)+"output/result.json", api.CIJobsResultManifestMaxBytes)
	if err != nil {
		return Observation{}, err
	}
	var result JobsResultManifest
	if err := uniqueJobsJSON(json.NewDecoder(bytes.NewReader(raw)), 0); err != nil {
		return Observation{}, protocolError("ambiguous or deeply nested Jobs result")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return Observation{}, protocolError("invalid Jobs result manifest")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Observation{}, protocolError("trailing Jobs result content")
	}
	if result.Version != 1 || result.AccountID != req.Key.AccountID || result.ExecutionID != req.Key.ExecutionID ||
		result.AttemptID != req.Key.AttemptID || result.CommitSHA != req.CommitSHA {
		return Observation{}, protocolError("Jobs result identity mismatch")
	}
	out := Observation{Status: result.Status, Steps: result.Steps, ExitCode: result.ExitCode, MoreLogs: result.LogTruncated}
	for _, artifact := range result.Artifacts {
		out.Artifacts = append(out.Artifacts, artifact.ArtifactRef)
	}
	// Validate counts, selectors and byte budgets before opening any artifact.
	if len(result.Steps) != len(job.Steps) || len(result.Artifacts) > api.CIMaxArtifactsPerJob ||
		(result.LogTruncated && result.Logs.Bytes != api.CILogMaxBytesPerAttempt) ||
		result.Logs.Bytes < 0 || result.Logs.Bytes > api.CILogMaxBytesPerAttempt {
		return Observation{}, protocolError("invalid Jobs result bounds")
	}
	if err := validateArtifacts(job.Artifacts, out); err != nil {
		return Observation{}, err
	}
	if result.Logs.Key != jobsObjectPrefix(req.Key)+"output/logs.bin" {
		return Observation{}, protocolError("CI log object is outside attempt scope")
	}
	logs, err := s.readVerified(ctx, result.Logs, api.CILogMaxBytesPerAttempt)
	if err != nil {
		return Observation{}, err
	}
	out.Logs = logs
	ordinary := job
	ordinary.JUnit = nil // Report diagnostics are computed after artifact verification.
	if err := validateJobsOutput(ordinary, jobsTask{Status: status, ExitCode: exit, Stopped: true}, out); err != nil {
		return Observation{}, err
	}
	reports := make(map[string]TestReport, len(job.JUnit))
	for _, artifact := range result.Artifacts {
		expected := jobsObjectPrefix(req.Key) + "output/artifacts/" + artifact.Path
		if artifact.Key != expected {
			return Observation{}, protocolError("CI artifact object is outside attempt scope")
		}
		if slices.Contains(job.JUnit, artifact.Path) && artifact.Bytes <= api.CIJUnitMaxBytes {
			raw, err := s.readVerified(ctx, SourceArchive{Key: artifact.Key, Digest: artifact.Digest, Bytes: artifact.Bytes}, api.CIJUnitMaxBytes)
			if err != nil {
				return Observation{}, err
			}
			report, err := parseJUnit(ctx, raw, artifact.ArtifactRef)
			if err != nil {
				return Observation{}, err
			}
			reports[artifact.Path] = report
		} else {
			if err := s.verifyArtifact(ctx, artifact); err != nil {
				return Observation{}, err
			}
			if slices.Contains(job.JUnit, artifact.Path) {
				reports[artifact.Path] = TestReport{ArtifactRef: artifact.ArtifactRef, Status: ReportLimit}
			}
		}
	}
	for _, name := range job.JUnit {
		report, ok := reports[name]
		if !ok {
			report = TestReport{ArtifactRef: ArtifactRef{Path: name}, Status: ReportMissing}
		}
		out.TestReports = append(out.TestReports, report)
	}
	return out, nil
}

func (s BlobJobsOutput) read(ctx context.Context, key string, bound int64) ([]byte, error) {
	body, err := s.Blobs.Get(ctx, key)
	if err != nil || body == nil {
		return nil, errors.New("CI output object unavailable")
	}
	defer func() { _ = body.Close() }()
	var out bytes.Buffer
	count, err := copyJobsBlob(ctx, &out, io.LimitReader(body, bound+1))
	if err != nil || count > bound {
		return nil, protocolError("CI output object exceeds its bound or is unreadable")
	}
	return out.Bytes(), nil
}

func (s BlobJobsOutput) readVerified(ctx context.Context, blob SourceArchive, bound int64) ([]byte, error) {
	if blob.Bytes < 0 || blob.Bytes > bound || !artifactDigestPattern.MatchString(blob.Digest) {
		return nil, protocolError("invalid CI log blob reference")
	}
	raw, err := s.read(ctx, blob.Key, blob.Bytes)
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) != blob.Bytes || jobsDigest(raw) != blob.Digest {
		return nil, protocolError("CI log blob checksum mismatch")
	}
	return raw, nil
}

func (s BlobJobsOutput) verifyArtifact(ctx context.Context, artifact JobsArtifact) error {
	body, err := s.Blobs.Get(ctx, artifact.Key)
	if err != nil || body == nil {
		return errors.New("CI artifact object unavailable")
	}
	defer func() { _ = body.Close() }()
	hash := sha256.New()
	count, err := copyJobsBlob(ctx, hash, io.LimitReader(body, artifact.Bytes+1))
	if err != nil || count != artifact.Bytes || "sha256:"+hex.EncodeToString(hash.Sum(nil)) != artifact.Digest {
		return protocolError("CI artifact bytes or checksum mismatch")
	}
	return nil
}

func jobsDigest(raw []byte) string {
	digest := sha256.Sum256(raw)
	return fmt.Sprintf("sha256:%x", digest)
}

func copyJobsBlob(ctx context.Context, dst io.Writer, src io.Reader) (int64, error) {
	return io.Copy(dst, jobsContextReader{ctx: ctx, reader: src})
}

type jobsContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r jobsContextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

func uniqueJobsJSON(dec *json.Decoder, depth int) error {
	if depth > api.CIJobsResultJSONMaxDepth {
		return ErrBackendProtocol
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	seen := map[string]bool{}
	for dec.More() {
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[strings.ToLower(name)] {
				return ErrBackendProtocol
			}
			seen[strings.ToLower(name)] = true
		}
		if err := uniqueJobsJSON(dec, depth+1); err != nil {
			return err
		}
	}
	_, err = dec.Token()
	return err
}
