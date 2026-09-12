package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path"
	"strings"
)

// ExecutionRuntime is the closed set of interpreters with a sanitized runtime
// snapshot and a guest-side one-shot executor. Compiled runtimes intentionally
// do not appear in v1: compilation belongs in the builder isolation boundary,
// not in a restored execution guest.
type ExecutionRuntime string

const (
	ExecutionRuntimeNode22    ExecutionRuntime = "node22"
	ExecutionRuntimeNode24    ExecutionRuntime = "node24"
	ExecutionRuntimePython312 ExecutionRuntime = "python312"
	ExecutionRuntimePython313 ExecutionRuntime = "python313"
)

// Valid reports whether the runtime has an initial one-shot execution
// contract. Availability of a particular snapshot is checked later by the
// scheduler; a missing snapshot must cold boot the same sanitized image.
func (r ExecutionRuntime) Valid() bool {
	switch r {
	case ExecutionRuntimeNode22, ExecutionRuntimeNode24,
		ExecutionRuntimePython312, ExecutionRuntimePython313:
		return true
	default:
		return false
	}
}

// ExecutionNetworkMode is deliberately a closed set. "none" means the guest
// receives loopback only: no tenant bridge route, service discovery, DNS, or
// public egress is installed for the disposable VM.
type ExecutionNetworkMode string

const ExecutionNetworkNone ExecutionNetworkMode = "none"

// ExecutionNetworkPolicy is separate from the resource limits so a later ADR
// can add a constrained allowlist without silently changing v1's deny-all
// default.
type ExecutionNetworkPolicy struct {
	Mode ExecutionNetworkMode `json:"mode"`
}

// ExecutionLimitRequest contains caller-selectable limits. Zero means use the
// plan default; negative values are always invalid.
type ExecutionLimitRequest struct {
	TimeoutMS       int `json:"timeout_ms,omitempty"`
	MemoryMB        int `json:"memory_mb,omitempty"`
	CPUMillicores   int `json:"cpu_millicores,omitempty"`
	EphemeralDiskMB int `json:"ephemeral_disk_mb,omitempty"`
	MaxOutputBytes  int `json:"max_output_bytes,omitempty"`
}

// ExecutionFile is one regular file in an ephemeral source bundle. Content
// is base64 encoded by JSON clients and exists only for the lifetime of the
// execution; it is never persisted in the customer-facing execution receipt.
type ExecutionFile struct {
	Path    string `json:"path"`
	Content []byte `json:"content"`
}

const (
	// ExecutionBundleMaxFiles prevents a caller from turning admission into a
	// directory-tree allocation attack. The plan's source-byte limit remains
	// the authoritative total-content cap.
	ExecutionBundleMaxFiles    = 256
	ExecutionBundleMaxPathSize = 256
)

// CreateExecutionRequest is the caller-authored one-shot execution contract.
// Source and input are never included in ExecutionResponse.
type CreateExecutionRequest struct {
	Runtime    ExecutionRuntime        `json:"runtime"`
	Source     string                  `json:"source,omitempty"`
	Entrypoint string                  `json:"entrypoint,omitempty"`
	Files      []ExecutionFile         `json:"files,omitempty"`
	Input      json.RawMessage         `json:"input,omitempty"`
	Limits     *ExecutionLimitRequest  `json:"limits,omitempty"`
	Network    *ExecutionNetworkPolicy `json:"network,omitempty"`
}

// ResolvedExecutionLimits is the immutable envelope admitted by apid and
// enforced independently by the scheduler, VMM cgroup, and guest executor.
type ResolvedExecutionLimits struct {
	TimeoutMS       int `json:"timeout_ms"`
	MemoryMB        int `json:"memory_mb"`
	CPUMillicores   int `json:"cpu_millicores"`
	EphemeralDiskMB int `json:"ephemeral_disk_mb"`
	MaxOutputBytes  int `json:"max_output_bytes"`
	PIDsMax         int `json:"pids_max"`
}

// ResolvedExecutionRequest is the normalized form persisted as execution
// intent. Input is always valid JSON and Network.Mode is always explicit.
type ResolvedExecutionRequest struct {
	Runtime    ExecutionRuntime
	Source     string
	Entrypoint string
	Files      []ExecutionFile
	Input      json.RawMessage
	Limits     ResolvedExecutionLimits
	Network    ExecutionNetworkPolicy
}

// SourceBytes returns the admitted source footprint for state accounting. It
// counts file content for bundles and the legacy source string otherwise.
func (r ResolvedExecutionRequest) SourceBytes() int {
	if len(r.Files) == 0 {
		return len(r.Source)
	}
	total := 0
	for _, file := range r.Files {
		total += len(file.Content)
	}
	return total
}

// ExecutionSnapshotShape identifies the caller-controlled portion of a
// compatible runtime snapshot. Kernel, guest-executor, architecture, and base
// image digests are added by the snapshot catalog.
type ExecutionSnapshotShape struct {
	Runtime         ExecutionRuntime `json:"runtime"`
	MemoryMB        int              `json:"memory_mb"`
	EphemeralDiskMB int              `json:"ephemeral_disk_mb"`
}

// SnapshotShape returns the fields that must not be silently changed between
// snapshot capture and restore. CPU is absent because it is enforced as a host
// cgroup quota rather than a Firecracker machine shape.
func (r ResolvedExecutionRequest) SnapshotShape() ExecutionSnapshotShape {
	return ExecutionSnapshotShape{
		Runtime:         r.Runtime,
		MemoryMB:        r.Limits.MemoryMB,
		EphemeralDiskMB: r.Limits.EphemeralDiskMB,
	}
}

// Resolve validates a caller request against the selected plan and fills every
// default. No persistence, scheduling, or VM work may occur before this gate.
func (r CreateExecutionRequest) Resolve(plan Plan) (ResolvedExecutionRequest, *Problem) {
	planLimits, ok := plan.ExecutionLimits()
	if !ok || !planLimits.Allowed {
		return ResolvedExecutionRequest{}, ErrExecutionsNotAllowed(plan)
	}
	if !r.Runtime.Valid() {
		return ResolvedExecutionRequest{}, executionInvalid(
			CodeExecutionRuntimeInvalid,
			fmt.Sprintf("runtime %q is not supported; use node22, node24, python312, or python313", r.Runtime),
		)
	}

	var source string
	var entrypoint string
	var files []ExecutionFile
	sourcePresent := strings.TrimSpace(r.Source) != ""
	bundlePresent := len(r.Files) != 0 || strings.TrimSpace(r.Entrypoint) != ""
	if sourcePresent && bundlePresent {
		return ResolvedExecutionRequest{}, executionInvalid(CodeExecutionSourceInvalid, "source cannot be combined with files or entrypoint")
	}
	switch {
	case sourcePresent:
		if strings.ContainsRune(r.Source, '\x00') {
			return ResolvedExecutionRequest{}, executionInvalid(CodeExecutionSourceInvalid, "source must not contain NUL bytes")
		}
		if len(r.Source) > planLimits.MaxSourceBytes {
			return ResolvedExecutionRequest{}, executionPayloadTooLarge("source", planLimits.MaxSourceBytes, len(r.Source))
		}
		source = r.Source
	case bundlePresent:
		entrypoint = r.Entrypoint
		_, err := validateExecutionBundle(entrypoint, r.Files, planLimits.MaxSourceBytes)
		if err != nil {
			var tooLarge executionBundleTooLargeError
			if errors.As(err, &tooLarge) {
				return ResolvedExecutionRequest{}, executionPayloadTooLarge("bundle", planLimits.MaxSourceBytes, tooLarge.observed)
			}
			return ResolvedExecutionRequest{}, executionInvalid(CodeExecutionSourceInvalid, err.Error())
		}
		files = cloneExecutionFiles(r.Files)
	default:
		return ResolvedExecutionRequest{}, executionInvalid(CodeExecutionSourceInvalid, "source or a non-empty files bundle with entrypoint is required")
	}

	input := r.Input
	if len(input) == 0 {
		input = json.RawMessage("null")
	}
	if len(input) > planLimits.MaxInputBytes {
		return ResolvedExecutionRequest{}, executionPayloadTooLarge("input", planLimits.MaxInputBytes, len(input))
	}
	if !json.Valid(input) {
		return ResolvedExecutionRequest{}, executionInvalid(
			CodeExecutionPayloadInvalid,
			"input must be one complete JSON value",
		)
	}

	network := ExecutionNetworkPolicy{Mode: ExecutionNetworkNone}
	if r.Network != nil {
		network = *r.Network
		if network.Mode == "" {
			network.Mode = ExecutionNetworkNone
		}
	}
	if network.Mode != ExecutionNetworkNone {
		return ResolvedExecutionRequest{}, executionInvalid(
			CodeExecutionNetworkInvalid,
			fmt.Sprintf("network mode %q is not supported; v1 requires none", network.Mode),
		)
	}

	limits, problem := resolveExecutionLimits(r.Limits, planLimits)
	if problem != nil {
		return ResolvedExecutionRequest{}, problem
	}

	return ResolvedExecutionRequest{
		Runtime:    r.Runtime,
		Source:     source,
		Entrypoint: entrypoint,
		Files:      files,
		Input:      append(json.RawMessage(nil), input...),
		Limits:     limits,
		Network:    network,
	}, nil
}

type executionBundleTooLargeError struct {
	observed int
}

func (e executionBundleTooLargeError) Error() string {
	return "bundle exceeds the source-byte limit"
}

func validateExecutionBundle(entrypoint string, files []ExecutionFile, maxBytes int) (int, error) {
	if strings.TrimSpace(entrypoint) == "" {
		return 0, fmt.Errorf("entrypoint is required for a files bundle")
	}
	if len(files) == 0 {
		return 0, fmt.Errorf("files must contain at least one file")
	}
	if len(files) > ExecutionBundleMaxFiles {
		return 0, fmt.Errorf("bundle contains %d files; maximum is %d", len(files), ExecutionBundleMaxFiles)
	}
	seen := make(map[string]struct{}, len(files))
	total := 0
	entrypointFound := false
	for _, file := range files {
		clean, err := validateExecutionFilePath(file.Path)
		if err != nil {
			return 0, err
		}
		if _, ok := seen[clean]; ok {
			return 0, fmt.Errorf("bundle contains duplicate path %q", clean)
		}
		seen[clean] = struct{}{}
		if clean == entrypoint {
			entrypointFound = true
		}
		total += len(file.Content)
		if total > maxBytes {
			return total, executionBundleTooLargeError{observed: total}
		}
	}
	if clean, err := validateExecutionFilePath(entrypoint); err != nil {
		return 0, fmt.Errorf("invalid entrypoint: %w", err)
	} else if clean != entrypoint {
		return 0, fmt.Errorf("entrypoint must be normalized relative path")
	}
	for filePath := range seen {
		for parent := path.Dir(filePath); parent != "."; parent = path.Dir(parent) {
			if _, ok := seen[parent]; ok {
				return 0, fmt.Errorf("bundle path conflict: %q is both a file and a directory", parent)
			}
		}
	}
	if !entrypointFound {
		return 0, fmt.Errorf("entrypoint %q is not present in files", entrypoint)
	}
	if total == 0 {
		return 0, fmt.Errorf("bundle content must not be empty")
	}
	return total, nil
}

// ValidateExecutionBundle applies the same path, file-count, and byte-count
// checks used by API admission. Guest-side code calls this again before
// writing files, so a future transport cannot turn an untrusted manifest into
// a host path traversal.
func ValidateExecutionBundle(entrypoint string, files []ExecutionFile, maxBytes int) error {
	_, err := validateExecutionBundle(entrypoint, files, maxBytes)
	return err
}

func validateExecutionFilePath(value string) (string, error) {
	if value == "" || len(value) > ExecutionBundleMaxPathSize || strings.ContainsRune(value, '\x00') || strings.ContainsRune(value, '\\') {
		return "", fmt.Errorf("file path is empty, too long, or contains forbidden characters")
	}
	if strings.HasPrefix(value, "/") {
		return "", fmt.Errorf("file path %q must be relative", value)
	}
	clean := path.Clean(value)
	if clean != value || clean == "." || clean == ".." || strings.HasPrefix(clean, "../") {
		return "", fmt.Errorf("file path %q must be a normalized relative path", value)
	}
	return clean, nil
}

func cloneExecutionFiles(files []ExecutionFile) []ExecutionFile {
	cloned := make([]ExecutionFile, len(files))
	for i, file := range files {
		cloned[i] = ExecutionFile{Path: file.Path, Content: append([]byte(nil), file.Content...)}
	}
	return cloned
}

func resolveExecutionLimits(requested *ExecutionLimitRequest, plan ExecutionPlanLimits) (ResolvedExecutionLimits, *Problem) {
	limits := ResolvedExecutionLimits{
		TimeoutMS:       plan.DefaultTimeoutMS,
		MemoryMB:        plan.DefaultMemoryMB,
		CPUMillicores:   plan.DefaultCPUMillicores,
		EphemeralDiskMB: plan.DefaultEphemeralDiskMB,
		MaxOutputBytes:  plan.DefaultOutputBytes,
		PIDsMax:         plan.PIDsMax,
	}
	if requested == nil {
		return limits, nil
	}

	if requested.TimeoutMS < 0 || requested.MemoryMB < 0 || requested.CPUMillicores < 0 ||
		requested.EphemeralDiskMB < 0 || requested.MaxOutputBytes < 0 {
		return ResolvedExecutionLimits{}, executionInvalid(CodeExecutionLimitInvalid, "execution limits cannot be negative")
	}
	if requested.TimeoutMS != 0 {
		if requested.TimeoutMS < ExecutionTimeoutMinMS {
			return ResolvedExecutionLimits{}, executionLimitInvalid("timeout_ms", "must be at least 100")
		}
		if requested.TimeoutMS > plan.MaxTimeoutMS {
			return ResolvedExecutionLimits{}, executionLimitExceeded("timeout_ms", plan.MaxTimeoutMS, requested.TimeoutMS)
		}
		limits.TimeoutMS = requested.TimeoutMS
	}
	if requested.MemoryMB != 0 {
		if !ValidExecutionMemoryMB(requested.MemoryMB) {
			return ResolvedExecutionLimits{}, executionLimitInvalid("memory_mb", "must be one of 128, 256, 512, or 1024")
		}
		if requested.MemoryMB > plan.MaxMemoryMB {
			return ResolvedExecutionLimits{}, executionLimitExceeded("memory_mb", plan.MaxMemoryMB, requested.MemoryMB)
		}
		limits.MemoryMB = requested.MemoryMB
	}
	if requested.CPUMillicores != 0 {
		if !ValidAppCPUMillicores(requested.CPUMillicores) {
			return ResolvedExecutionLimits{}, executionLimitInvalid("cpu_millicores", "must be one of 250, 500, or 1000")
		}
		if requested.CPUMillicores > plan.MaxCPUMillicores {
			return ResolvedExecutionLimits{}, executionLimitExceeded("cpu_millicores", plan.MaxCPUMillicores, requested.CPUMillicores)
		}
		limits.CPUMillicores = requested.CPUMillicores
	}
	if requested.EphemeralDiskMB != 0 {
		if !ValidExecutionEphemeralDiskMB(requested.EphemeralDiskMB) {
			return ResolvedExecutionLimits{}, executionLimitInvalid("ephemeral_disk_mb", "must be one of 64, 128, 256, 512, 1024, or 2048")
		}
		if requested.EphemeralDiskMB > plan.MaxEphemeralDiskMB {
			return ResolvedExecutionLimits{}, executionLimitExceeded("ephemeral_disk_mb", plan.MaxEphemeralDiskMB, requested.EphemeralDiskMB)
		}
		limits.EphemeralDiskMB = requested.EphemeralDiskMB
	}
	if requested.MaxOutputBytes != 0 {
		if requested.MaxOutputBytes < ExecutionOutputMinBytes {
			return ResolvedExecutionLimits{}, executionLimitInvalid("max_output_bytes", "must be at least 1024")
		}
		if requested.MaxOutputBytes > plan.MaxOutputBytes {
			return ResolvedExecutionLimits{}, executionLimitExceeded("max_output_bytes", plan.MaxOutputBytes, requested.MaxOutputBytes)
		}
		limits.MaxOutputBytes = requested.MaxOutputBytes
	}

	return limits, nil
}

func executionInvalid(code, detail string) *Problem {
	return NewProblem(http.StatusUnprocessableEntity, code, "Invalid execution request", detail).
		WithDocs(docsBase + "/executions#request")
}

func executionLimitInvalid(field, detail string) *Problem {
	return executionInvalid(CodeExecutionLimitInvalid, field+" "+detail)
}

func executionPayloadTooLarge(field string, limit, observed int) *Problem {
	return NewProblem(
		http.StatusRequestEntityTooLarge,
		CodeExecutionPayloadTooLarge,
		"Execution payload too large",
		fmt.Sprintf("%s is %d bytes; this plan allows %d", field, observed, limit),
	).WithByteLimit(int64(limit), int64(observed)).WithDocs(docsBase + "/executions#limits")
}

func executionLimitExceeded(field string, limit, observed int) *Problem {
	return NewProblem(
		http.StatusForbidden,
		CodeExecutionLimitExceeded,
		"Execution limit exceeds plan",
		fmt.Sprintf("%s is %d; this plan allows %d", field, observed, limit),
	).WithLimit(int64(limit), int64(observed)).WithDocs(docsBase + "/executions#limits")
}

// ErrExecutionsNotAllowed is returned before persistence when the account plan
// cannot create disposable executions. Unknown plans use the same fail-closed
// response and are never treated as Free implicitly.
func ErrExecutionsNotAllowed(plan Plan) *Problem {
	return NewProblem(
		http.StatusForbidden,
		CodeExecutionsNotAllowed,
		"Executions are not available",
		fmt.Sprintf("plan %q does not include disposable one-shot executions", plan),
	).WithDocs(docsBase + "/plans#executions")
}

// ExecutionStatus is the durable state machine exposed to callers.
type ExecutionStatus string

const (
	ExecutionStatusQueued      ExecutionStatus = "queued"
	ExecutionStatusRestoring   ExecutionStatus = "restoring"
	ExecutionStatusRunning     ExecutionStatus = "running"
	ExecutionStatusSucceeded   ExecutionStatus = "succeeded"
	ExecutionStatusFailed      ExecutionStatus = "failed"
	ExecutionStatusTimedOut    ExecutionStatus = "timed_out"
	ExecutionStatusOutOfMemory ExecutionStatus = "out_of_memory"
	ExecutionStatusCancelled   ExecutionStatus = "cancelled"
)

// Terminal reports whether no further state transition is allowed.
func (s ExecutionStatus) Terminal() bool {
	switch s {
	case ExecutionStatusSucceeded, ExecutionStatusFailed, ExecutionStatusTimedOut,
		ExecutionStatusOutOfMemory, ExecutionStatusCancelled:
		return true
	default:
		return false
	}
}

// ExecutionUsage contains bounded, billable measurements from the host. Guest
// self-reported usage is never authoritative.
type ExecutionUsage struct {
	WallTimeMS   int64 `json:"wall_time_ms"`
	CPUTimeMS    int64 `json:"cpu_time_ms"`
	PeakMemoryMB int   `json:"peak_memory_mb"`
}

// ExecutionFailure is safe caller-facing failure detail. Host paths, VMM
// command lines, and guest protocol frames must never appear in Message.
type ExecutionFailure struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ExecutionResponse intentionally omits source and input. Result, stdout, and
// stderr share the admitted MaxOutputBytes budget.
type ExecutionResponse struct {
	ID              string                  `json:"id"`
	Status          ExecutionStatus         `json:"status"`
	Runtime         ExecutionRuntime        `json:"runtime"`
	Limits          ResolvedExecutionLimits `json:"limits"`
	Result          json.RawMessage         `json:"result,omitempty"`
	Stdout          string                  `json:"stdout,omitempty"`
	Stderr          string                  `json:"stderr,omitempty"`
	OutputTruncated bool                    `json:"output_truncated"`
	ExitCode        *int                    `json:"exit_code,omitempty"`
	Usage           *ExecutionUsage         `json:"usage,omitempty"`
	Failure         *ExecutionFailure       `json:"failure,omitempty"`
	CreatedAt       string                  `json:"created_at"`
	StartedAt       *string                 `json:"started_at,omitempty"`
	FinishedAt      *string                 `json:"finished_at,omitempty"`
}
