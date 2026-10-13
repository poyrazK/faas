package api

import "time"

// Profile kinds (ADR-1155).
const (
	ProfileKindCPU  = "cpu"
	ProfileKindHeap = "heap"
)

// Capture triggers (ADR-1155): a person or API client, or guest-init
// reporting memory pressure on the instance.
const (
	ProfileCaptureTriggerManual         = "manual"
	ProfileCaptureTriggerMemoryPressure = "memory_pressure"
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
	// Trigger is manual or memory_pressure; TriggerDetail explains an
	// automatic capture ("guest memory available fell to 31 MiB of 246 MiB").
	Trigger       string                  `json:"trigger,omitempty"`
	TriggerDetail string                  `json:"trigger_detail,omitempty"`
	InstanceID    string                  `json:"instance_id,omitempty"`
	DeploymentID  string                  `json:"deployment_id,omitempty"`
	Processes     int                     `json:"processes"`
	Dropped       int                     `json:"dropped,omitempty"`
	Reason        string                  `json:"reason,omitempty"`
	Profiles      []ProfileCaptureProfile `json:"profiles"`
	CreatedAt     time.Time               `json:"created_at"`
	CompletedAt   *time.Time              `json:"completed_at,omitempty"`
	ExpiresAt     time.Time               `json:"expires_at"`
}

// Done reports whether the capture reached a terminal status.
func (c ProfileCapture) Done() bool {
	return c.Status == ProfileCaptureReady || c.Status == ProfileCaptureFailed
}

type ProfileCaptureList struct {
	Captures []ProfileCapture `json:"captures"`
}

// ProfileCaptureFunction is a function's sampled cost in the view's unit:
// CPU nanoseconds or live heap bytes.
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
// processes. Unit is "nanoseconds" for cpu and "bytes" for heap. Node and
// Python heap captures report memory allocated during the window that is
// still live at its end; Go reports the sampled live heap.
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
