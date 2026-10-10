package profileproto

import (
	"fmt"
	"regexp"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// On-demand captures (ADR-967) arm the guest's dormant collectors for a
// bounded window and return the collected pprof profiles to the host over
// the guest-init control listener. The host never parses the profiles; the
// control plane parses and normalizes them outside the root boundary.
const (
	KindCPU  = api.ProfileKindCPU
	KindHeap = api.ProfileKindHeap

	// CaptureMessageType shares guest-init's host-initiated control listener
	// (vsock 1024) with resume, extension, CPU-limit and memory-stats frames.
	CaptureMessageType uint32 = 7
	// CaptureMaxRequestBytes bounds the JSON request body.
	CaptureMaxRequestBytes = 1024
	CaptureMinDuration     = api.ProfileCaptureMinDuration
	CaptureMaxDuration     = api.ProfileCaptureMaxDuration
	// CaptureGrace covers the final collector flush after the window ends.
	CaptureGrace = api.ProfileCaptureGrace
	// CaptureMaxProfiles bounds one reply. Profiles are per process and kind.
	CaptureMaxProfiles = api.ProfileCaptureMaxProfiles
	// CaptureMaxProfileBytes bounds the sum of returned compressed profiles,
	// keeping each host hop under gRPC's default 4 MiB message limit.
	CaptureMaxProfileBytes = api.ProfileCaptureMaxProfileBytes
	// CaptureMaxReplyBytes bounds the JSON reply (base64 inflates by 4/3).
	CaptureMaxReplyBytes = CaptureMaxProfileBytes*4/3 + 64<<10
)

// Capture acknowledgement bytes. Zero is followed by a length-prefixed
// CaptureResult; any other value is a typed refusal with no body.
const (
	CaptureAckOK          byte = 0
	CaptureAckUnsupported byte = 20
	CaptureAckBusy        byte = 21
	CaptureAckSuspended   byte = 22
	CaptureAckInvalid     byte = 23
)

var captureIDPattern = regexp.MustCompile(`^[a-f0-9]{32}$`)

type CaptureRequest struct {
	CaptureID      string   `json:"capture_id"`
	Kinds          []string `json:"kinds"`
	DurationMillis int64    `json:"duration_millis"`
}

// CapturedProfile is one collector's contribution. Profile is a gzip or raw
// pprof protobuf, unparsed on the host.
type CapturedProfile struct {
	Kind          string `json:"kind"`
	ProcessID     string `json:"process_id,omitempty"`
	Profile       []byte `json:"profile"`
	FromUnixNano  int64  `json:"from_unix_nano,omitempty"`
	UntilUnixNano int64  `json:"until_unix_nano,omitempty"`
}

// CaptureResult reports what the guest observed. Processes counts collectors
// that polled during the window; zero means nothing in the VM is instrumented.
// Dropped counts profiles discarded by the reply bounds.
type CaptureResult struct {
	Profiles  []CapturedProfile `json:"profiles"`
	Processes int               `json:"processes"`
	Dropped   int               `json:"dropped,omitempty"`
	Reason    string            `json:"reason,omitempty"`
}

func ValidKind(kind string) bool { return api.ValidProfileKind(kind) }

// Duration returns the requested capture window.
func (r CaptureRequest) Duration() time.Duration {
	return time.Duration(r.DurationMillis) * time.Millisecond
}

func (r CaptureRequest) Validate() error {
	if !captureIDPattern.MatchString(r.CaptureID) {
		return fmt.Errorf("capture_id must be 32 lowercase hex characters")
	}
	if d := r.Duration(); d < CaptureMinDuration || d > CaptureMaxDuration {
		return fmt.Errorf("capture duration must be between %s and %s", CaptureMinDuration, CaptureMaxDuration)
	}
	if len(r.Kinds) == 0 || len(r.Kinds) > 2 {
		return fmt.Errorf("capture needs one or two profile kinds")
	}
	seen := map[string]bool{}
	for _, kind := range r.Kinds {
		if !ValidKind(kind) || seen[kind] {
			return fmt.Errorf("profile kinds must be distinct values of cpu or heap")
		}
		seen[kind] = true
	}
	return nil
}

// WantsKind reports whether kind was requested.
func (r CaptureRequest) WantsKind(kind string) bool {
	for _, k := range r.Kinds {
		if k == kind {
			return true
		}
	}
	return false
}

// Validate bounds a reply before the host forwards it.
func (r CaptureResult) Validate() error {
	if len(r.Profiles) > CaptureMaxProfiles {
		return fmt.Errorf("capture returned too many profiles")
	}
	total := 0
	for _, p := range r.Profiles {
		if !ValidKind(p.Kind) || len(p.Profile) == 0 {
			return fmt.Errorf("capture returned an invalid profile")
		}
		total += len(p.Profile)
	}
	if total > CaptureMaxProfileBytes || len(r.Reason) > 256 || r.Processes < 0 || r.Dropped < 0 {
		return fmt.Errorf("capture reply exceeds bounds")
	}
	return nil
}
