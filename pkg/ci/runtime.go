package ci

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	RuntimeInstanceHeader   = "X-Gregale-CI-Instance"
	RuntimeCapabilityHeader = "X-Gregale-CI-Capability"
	RuntimeObjectHeader     = "X-Gregale-CI-Object"
	RuntimeDigestHeader     = "X-Gregale-CI-Digest"
	RuntimeSource           = "source"
	RuntimeControl          = "control"
	RuntimeUpload           = "upload"
	RuntimeProgress         = "progress"
	RuntimeInput            = "input"
)

var ErrRuntimeAuthority = errors.New("CI runtime authority unavailable")

type RuntimeProof struct {
	RunID      string `json:"run_id"`
	InstanceID string `json:"instance_id"`
	Capability string `json:"-"`
}

func (RuntimeProof) String() string         { return "CIRuntimeProof{redacted}" }
func (p RuntimeProof) GoString() string     { return p.String() }
func (p RuntimeProof) LogValue() slog.Value { return slog.StringValue(p.String()) }

// RuntimeHostProof never crosses the guest/API boundary. Only the scheduler's
// native lease can issue a runtime capability through the default-off launch gate.
type RuntimeHostProof struct {
	RunID, InstanceID string
	LeaseToken        string `json:"-"`
}

func (RuntimeHostProof) String() string         { return "CIRuntimeHostProof{redacted}" }
func (p RuntimeHostProof) GoString() string     { return p.String() }
func (p RuntimeHostProof) LogValue() slog.Value { return slog.StringValue(p.String()) }

type RuntimeView struct {
	Manifest        JobsManifest `json:"manifest"`
	CancelRequested bool         `json:"cancel_requested"`
	Sealed          bool         `json:"sealed"`
	ProgressEnabled bool         `json:"progress_enabled,omitempty"`
}

type RuntimeObject struct {
	Name   string `json:"name"`
	Digest string `json:"digest"`
	Bytes  int64  `json:"bytes"`
}

// RuntimeStore owns capabilities and immutable output receipts. No HTTP method
// issues a grant. Config gates new grants/source; existing grants may finalize
// output after admission is disabled, within the bounded cancellation window.
type RuntimeStore interface {
	AuthorizeJobsRuntime(context.Context, Config, RuntimeProof, string) (RuntimeView, error)
	BeginJobsRuntimeTransfer(context.Context, RuntimeProof) (string, error)
	EndJobsRuntimeTransfer(context.Context, RuntimeProof, string) error
	ReserveJobsRuntimeObject(context.Context, RuntimeProof, RuntimeObject) (string, bool, error)
	CommitJobsRuntimeObject(context.Context, RuntimeProof, RuntimeObject) error
	SealJobsRuntime(context.Context, RuntimeProof, []byte) error
}

type RuntimeResultStore interface {
	SealedJobsResult(context.Context, JobsSourceRequest) ([]byte, error)
}

func ValidRuntimeProof(proof RuntimeProof) bool {
	raw, err := hex.DecodeString(proof.Capability)
	return ValidID(proof.RunID) && ValidID(proof.InstanceID) && err == nil && len(raw) == 32 && strings.ToLower(proof.Capability) == proof.Capability
}

func DecodeJobsManifest(encoded string) (JobsManifest, error) {
	if len(encoded) > base64.StdEncoding.EncodedLen(api.CIJobsManifestMaxBytes) {
		return JobsManifest{}, ErrBackendProtocol
	}
	raw, err := base64.StdEncoding.Strict().DecodeString(encoded)
	if err != nil {
		return JobsManifest{}, ErrBackendProtocol
	}
	var manifest JobsManifest
	if err := decodeRuntimeJSON(raw, &manifest, api.CIJobsManifestMaxBytes); err != nil {
		return JobsManifest{}, err
	}
	if err := ValidateJobsManifest(manifest); err != nil {
		return JobsManifest{}, err
	}
	return manifest, nil
}

func ValidateJobsManifest(m JobsManifest) error {
	if m.Job.Matrix != nil {
		return ErrBackendProtocol
	}
	key := AttemptKey{AccountID: m.AccountID, ExecutionID: m.ExecutionID, AttemptID: m.AttemptID, JobName: m.Job.Name, Fence: 1}
	if validateJobsInputs(m) != nil || !ValidID(m.AccountID) || !ValidID(m.ExecutionID) || !ValidID(m.AttemptID) || !shaPattern.MatchString(m.CommitSHA) ||
		!repoPattern.MatchString(m.Repository) || m.Deadline.IsZero() || m.OutputPrefix != jobsObjectPrefix(key)+"output/" {
		return ErrBackendProtocol
	}
	def := Definition{Version: 1, Repository: m.Repository, Jobs: map[string]Job{m.Job.Name: m.Job.Job}}
	// Dependencies have already been satisfied by the controller, outside this VM.
	job := def.Jobs[m.Job.Name]
	job.Needs = nil
	job.Inputs = nil
	def.Jobs[m.Job.Name] = job
	if _, err := Normalize(def); err != nil {
		return ErrBackendProtocol
	}
	return validateSourceArchive(key, m.Source)
}

func DecodeJobsResult(raw []byte) (JobsResultManifest, error) {
	var result JobsResultManifest
	if err := decodeRuntimeJSON(raw, &result, api.CIJobsResultManifestMaxBytes); err != nil {
		return result, err
	}
	return result, nil
}

func decodeRuntimeJSON(raw []byte, dst any, bound int) error {
	if len(raw) == 0 || len(raw) > bound {
		return ErrBackendProtocol
	}
	if err := uniqueJobsJSON(json.NewDecoder(bytes.NewReader(raw)), 0); err != nil {
		return ErrBackendProtocol
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return ErrBackendProtocol
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return ErrBackendProtocol
	}
	return nil
}

func JobsArtifactSelected(selectors []string, name string) bool {
	if !validArtifact(name) || strings.ContainsAny(name, "*?[") {
		return false
	}
	for _, selector := range selectors {
		if artifactMatches(selector, name) {
			return true
		}
	}
	return false
}

// JobsArtifactDirectoryNeeded prunes unrelated directories before artifact
// traversal. A wildcard prefix still requires the bounded recursive scan.
func JobsArtifactDirectoryNeeded(selectors []string, dir string) bool {
	for _, selector := range selectors {
		parts := strings.Split(selector, "/")
		literal := make([]string, 0, len(parts))
		for _, part := range parts {
			if strings.ContainsAny(part, "*?[") {
				break
			}
			literal = append(literal, part)
		}
		prefix := strings.Join(literal, "/")
		if prefix == "" || prefix == dir || strings.HasPrefix(prefix, dir+"/") || strings.HasPrefix(dir, prefix+"/") {
			return true
		}
	}
	return false
}

func runtimeObjectKey(m JobsManifest, object RuntimeObject) (string, error) {
	if object.Bytes < 0 || !artifactDigestPattern.MatchString(object.Digest) {
		return "", ErrBackendProtocol
	}
	if object.Name == "logs.bin" {
		if object.Bytes > api.CILogMaxBytesPerAttempt {
			return "", ErrLimit
		}
	} else {
		name, ok := strings.CutPrefix(object.Name, "artifacts/")
		if !ok || !JobsArtifactSelected(m.Job.Artifacts, name) || object.Bytes > api.CIArtifactMaxBytes {
			return "", ErrBackendProtocol
		}
	}
	return m.OutputPrefix + object.Name, nil
}

// SealedJobsOutput reads the final manifest from the transactional journal.
// Uploading an object named result.json never creates a publishable result.
type SealedJobsOutput struct {
	Store RuntimeResultStore
	Blobs JobsBlobStore
}

func (SealedJobsOutput) runtimeSealRequired() {}

func (s SealedJobsOutput) Collect(ctx context.Context, req JobsSourceRequest, job PlannedJob, status string, exit *int) (Observation, error) {
	if s.Store == nil || s.Blobs == nil {
		return Observation{}, ErrRuntimeAuthority
	}
	if status != StatusSucceeded && status != StatusFailed && status != StatusCancelled && status != "timeout" && status != "oom" {
		return Observation{}, ErrBackendProtocol
	}
	raw, err := s.Store.SealedJobsResult(ctx, req)
	if err != nil {
		return Observation{}, err
	}
	// A native cancellation or failure may race the guest's final seal.
	// The scheduler's confirmed receipt determines the attempt outcome.
	result, err := DecodeJobsResult(raw)
	if err != nil {
		return Observation{}, err
	}
	if status == StatusCancelled {
		result.Status = StatusCancelled
		if exit != nil {
			result.ExitCode = exit
		}
	} else if status != StatusSucceeded {
		result.Status = StatusFailed
		if exit != nil && *exit != 0 {
			result.ExitCode = exit
		} else if result.ExitCode == nil || *result.ExitCode == 0 {
			code := 125
			result.ExitCode = &code
		}
	}
	raw, err = json.Marshal(result)
	if err != nil {
		return Observation{}, err
	}
	reader := sealedResultBlobs{Jobs: s.Blobs, Key: jobsObjectPrefix(req.Key) + "output/result.json", Result: raw}
	return (BlobJobsOutput{Blobs: reader}).Collect(ctx, req, job, status, exit)
}

type sealedResultBlobs struct {
	Jobs   JobsBlobStore
	Key    string
	Result []byte
}

func (b sealedResultBlobs) Put(context.Context, string, io.Reader) error { return ErrRuntimeAuthority }
func (b sealedResultBlobs) Get(ctx context.Context, key string) (io.ReadCloser, error) {
	if key == b.Key {
		return io.NopCloser(bytes.NewReader(b.Result)), nil
	}
	return b.Jobs.Get(ctx, key)
}

func runtimeExpiry(deadline time.Time) time.Time {
	return deadline.Add(time.Duration(api.CIJobsFinalizeSeconds) * time.Second)
}
