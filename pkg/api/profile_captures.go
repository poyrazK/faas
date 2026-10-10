package api

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// On-demand profile capture statuses (ADR-967).
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
		if !ValidProfileKind(kind) || seen[kind] {
			return fmt.Errorf("kinds must list distinct values of cpu or heap")
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
	ID                  string                  `json:"id"`
	AppID               string                  `json:"app_id"`
	Status              string                  `json:"status"`
	Kinds               []string                `json:"kinds"`
	DurationSeconds     int                     `json:"duration_seconds"`
	RequestedInstanceID string                  `json:"requested_instance_id,omitempty"`
	InstanceID          string                  `json:"instance_id,omitempty"`
	DeploymentID        string                  `json:"deployment_id,omitempty"`
	Processes           int                     `json:"processes"`
	Dropped             int                     `json:"dropped,omitempty"`
	Reason              string                  `json:"reason,omitempty"`
	Profiles            []ProfileCaptureProfile `json:"profiles"`
	CreatedAt           time.Time               `json:"created_at"`
	CompletedAt         *time.Time              `json:"completed_at,omitempty"`
	ExpiresAt           time.Time               `json:"expires_at"`
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
	Value    int64                  `json:"value"`
	Children []*ProfileCaptureStack `json:"children"`
}

// ProfileHeapResponse is a continuous heap profile (ADR-967). Query.Start is
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
}
