package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Capture triggers (ADR-1155): a person or API client, guest-init reporting
// memory pressure on the instance, the gateway reporting a slow request, or
// schedd seeing the instance use its whole CPU quota.
const (
	ProfileCaptureTriggerManual         = "manual"
	ProfileCaptureTriggerMemoryPressure = "memory_pressure"
	// ProfileCaptureTriggerSlowRequest marks a stack snapshot taken while a
	// request was in flight longer than ProfileCaptureSlowRequestAfter.
	ProfileCaptureTriggerSlowRequest = "slow_request"
	// ProfileCaptureTriggerCPUSaturation marks a CPU capture taken after the
	// instance used ProfileCPUSaturationPercent of its CPU quota for
	// ProfileCPUSaturationAfter.
	ProfileCaptureTriggerCPUSaturation = "cpu_saturation"
)

// On-demand profile capture statuses (ADR-1155).
const (
	ProfileCaptureQueued    = "queued"
	ProfileCaptureCapturing = "capturing"
	ProfileCaptureReady     = "ready"
	ProfileCaptureFailed    = "failed"
)

// CreateProfileCaptureRequest starts an on-demand capture on one RUNNING
// instance. Kinds defaults to ["cpu"], DurationSeconds to 10, and an empty
// InstanceID selects the app's earliest-started RUNNING instance.
type CreateProfileCaptureRequest struct {
	Kinds           []string `json:"kinds,omitempty"`
	DurationSeconds int      `json:"duration_seconds,omitempty"`
	InstanceID      string   `json:"instance_id,omitempty"`
}

// Normalize applies defaults and validates the request in place.
func (r *CreateProfileCaptureRequest) Normalize() error {
	if len(r.Kinds) == 0 {
		r.Kinds = []string{ProfileKindCPU}
	}
	if r.DurationSeconds == 0 {
		r.DurationSeconds = int(ProfileCaptureDefaultDuration / time.Second)
	}
	minimum, maximum := int(ProfileCaptureMinDuration/time.Second), int(ProfileCaptureMaxDuration/time.Second)
	if r.DurationSeconds < minimum || r.DurationSeconds > maximum {
		return fmt.Errorf("duration_seconds must be between %d and %d", minimum, maximum)
	}
	seen := map[string]bool{}
	for _, kind := range r.Kinds {
		if !ValidCaptureProfileKind(kind) || seen[kind] {
			return fmt.Errorf("kinds must list distinct values of cpu, heap, stacks, block or mutex")
		}
		seen[kind] = true
	}
	if r.InstanceID != "" {
		if _, err := uuid.Parse(r.InstanceID); err != nil {
			return fmt.Errorf("instance_id must be a UUID")
		}
	}
	return nil
}

// ProfileCaptureProfile summarizes one collector's stored profile.
type ProfileCaptureProfile struct {
	Kind      string `json:"kind"`
	ProcessID string `json:"process_id,omitempty"`
	Bytes     int    `json:"bytes"`
}

// ProfileCapture is the public record of one on-demand capture. Processes
// counts instrumented processes that saw the capture; zero with an empty
// Profiles list means the instance has no collector (custom image or a Go
// app that does not call guestprofiling.Start).
type ProfileCapture struct {
	ID                  string   `json:"id"`
	AppID               string   `json:"app_id"`
	Status              string   `json:"status"`
	Kinds               []string `json:"kinds"`
	DurationSeconds     int      `json:"duration_seconds"`
	RequestedInstanceID string   `json:"requested_instance_id,omitempty"`
	// Trigger is manual, memory_pressure, slow_request or cpu_saturation;
	// TriggerDetail explains an automatic capture ("guest memory available
	// fell to 31 MiB of 246 MiB").
	Trigger       string                  `json:"trigger,omitempty"`
	TriggerDetail string                  `json:"trigger_detail,omitempty"`
	InstanceID    string                  `json:"instance_id,omitempty"`
	DeploymentID  string                  `json:"deployment_id,omitempty"`
	Processes     int                     `json:"processes"`
	Dropped       int                     `json:"dropped,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
	Profiles      []ProfileCaptureProfile `json:"profiles"`
	// RequestID and RequestTraceID name the in-flight request that started a
	// slow_request capture; the request explorer links back by trace id.
	RequestID      string `json:"request_id,omitempty"`
	RequestTraceID string `json:"request_trace_id,omitempty"`
	// Partial marks a capture that ended early, normally because the
	// workload was OOM-killed; its heap profiles are the last checkpoints.
	Partial bool `json:"partial,omitempty"`
	// OOMKilledAt is set when the captured instance was OOM-killed within
	// ProfileCaptureOOMLinkWindow of the capture: the heap profile shows
	// memory retained before the kill. Peak and plan are in MB.
	OOMKilledAt *time.Time `json:"oom_killed_at,omitempty"`
	OOMPeakMB   int        `json:"oom_peak_mb,omitempty"`
	OOMPlanMB   int        `json:"oom_plan_mb,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
	CompletedAt *time.Time `json:"completed_at,omitempty"`
	ExpiresAt   time.Time  `json:"expires_at"`
}

// Done reports whether the capture reached a terminal status.
func (c ProfileCapture) Done() bool {
	return c.Status == ProfileCaptureReady || c.Status == ProfileCaptureFailed
}

// Automatic reports whether the platform, not a person, started the capture.
func (c ProfileCapture) Automatic() bool {
	return c.Trigger != "" && c.Trigger != ProfileCaptureTriggerManual
}

// Announced reports whether the capture is worth telling the app's owner
// about unprompted (ADR-1155): ready with profiles, and started by the
// platform or linked to an OOM kill. Webhooks and the app page announce
// exactly these.
func (c ProfileCapture) Announced() bool {
	return c.Status == ProfileCaptureReady && len(c.Profiles) > 0 && (c.Automatic() || c.OOMKilledAt != nil)
}

type ProfileCaptureList struct {
	Captures []ProfileCapture `json:"captures"`
}

// ProfileCaptureWebhookPayload is delivered with profile.capture_ready when
// an automatic capture, or one linked to an OOM kill, finishes with
// profiles, and with profile.capture_oom_linked when an OOM kill is linked
// to a capture that already finished (ADR-1155). It carries the capture
// record and where to open it, never profile samples.
type ProfileCaptureWebhookPayload struct {
	Version int            `json:"version"`
	Capture ProfileCapture `json:"capture"`
	// CapturePath is the dashboard capture page; APIPath the capture resource.
	CapturePath string `json:"capture_path"`
	APIPath     string `json:"api_path"`
}

// ProfileCaptureWebhookVersion is the ProfileCaptureWebhookPayload version.
const ProfileCaptureWebhookVersion = 1

// ProfileCaptureFunction is a function's sampled cost in the view's unit:
// CPU nanoseconds, live heap bytes, or goroutines/threads parked there.
type ProfileCaptureFunction struct {
	Name   string                 `json:"name"`
	File   string                 `json:"file,omitempty"`
	Line   int64                  `json:"line,omitempty"`
	Source *ProfileSourceLocation `json:"source,omitempty"`
	Self   int64                  `json:"self"`
	Total  int64                  `json:"total"`
}

type ProfileCaptureStack struct {
	Name     string                 `json:"name"`
	File     string                 `json:"file,omitempty"`
	Line     int64                  `json:"line,omitempty"`
	Source   *ProfileSourceLocation `json:"source,omitempty"`
	Value    int64                  `json:"value"`
	Children []*ProfileCaptureStack `json:"children"`
}

// ProfileHeapResponse is a continuous heap profile (ADR-1155). Query.Start is
// narrowed to the last collection window before Query.End so the view is a
// point-in-time live heap rather than a sum of snapshots.
type ProfileHeapResponse struct {
	Query ProfileQuery `json:"query"`
	ProfileCaptureView
}

// ProfileCaptureView is the merged profile of one kind across the capture's
// processes. Unit is "nanoseconds" for cpu, "bytes" for heap and "count"
// (goroutines or threads) for stacks. Node and Python heap captures report
// memory allocated during the window that is still live at its end; Go
// reports the sampled live heap. Stacks is a snapshot taken when the capture
// starts: Go's goroutines, Python's threads and asyncio tasks, and Node's
// active handles and requests by type.
type ProfileCaptureView struct {
	Capture    ProfileCapture           `json:"capture"`
	Kind       string                   `json:"kind"`
	Unit       string                   `json:"unit"`
	Total      int64                    `json:"total"`
	Functions  []ProfileCaptureFunction `json:"functions"`
	Flamegraph *ProfileCaptureStack     `json:"flamegraph"`
	Empty      bool                     `json:"empty"`
	// Source is the capture deployment's recorded Git provenance; function
	// and frame Source links use its commit.
	Source *ProfileSource `json:"source,omitempty"`
}

// ProfileCaptureDiff compares one kind between two ready captures of an app
// (ADR-1155). Values keep the view's unit and are not rescaled: Node and
// Python heap values cover each capture's own window, so captures of
// different durations carry a warning rather than a silent adjustment.
type ProfileCaptureDiff struct {
	Baseline       ProfileCapture                `json:"baseline"`
	Candidate      ProfileCapture                `json:"candidate"`
	Kind           string                        `json:"kind"`
	Unit           string                        `json:"unit"`
	BaselineTotal  int64                         `json:"baseline_total"`
	CandidateTotal int64                         `json:"candidate_total"`
	Delta          int64                         `json:"delta"`
	Functions      []ProfileCaptureFunctionDelta `json:"functions"`
	Flamegraph     *ProfileCaptureStackDelta     `json:"flamegraph"`
	Warnings       []string                      `json:"warnings"`
}

// ProfileCaptureFunctionDelta is one function's self cost in both captures,
// ordered by growth. Named functions aggregate across lines.
type ProfileCaptureFunctionDelta struct {
	Name      string                 `json:"name"`
	File      string                 `json:"file,omitempty"`
	Line      int64                  `json:"line,omitempty"`
	Source    *ProfileSourceLocation `json:"source,omitempty"`
	Baseline  int64                  `json:"baseline"`
	Candidate int64                  `json:"candidate"`
	Delta     int64                  `json:"delta"`
}

// ProfileCaptureStackDelta is one call path in both captures. Source comes
// from the candidate's deployment when the frame exists there.
type ProfileCaptureStackDelta struct {
	Name      string                      `json:"name"`
	File      string                      `json:"file,omitempty"`
	Line      int64                       `json:"line,omitempty"`
	Source    *ProfileSourceLocation      `json:"source,omitempty"`
	Baseline  int64                       `json:"baseline"`
	Candidate int64                       `json:"candidate"`
	Delta     int64                       `json:"delta"`
	Children  []*ProfileCaptureStackDelta `json:"children"`
}
