package state

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// On-demand profile captures (ADR-967). apid is the producer (queue + read),
// schedd the consumer (claim, finish, expire). Neither interface is part of
// Store; callers assert them like the other profiling stores.

var (
	ErrProfileCaptureQuota  = errors.New("profile capture limit reached for this account")
	ErrProfileCaptureActive = errors.New("a profile capture is already queued or running for this app")
)

// ProfileCaptureBlob is one collector's unparsed pprof profile.
type ProfileCaptureBlob struct {
	Kind      string
	ProcessID string
	Profile   []byte
}

// ClaimedProfileCapture is a queued capture schedd now owns.
type ClaimedProfileCapture struct {
	ID        string
	AppID     string
	AccountID string
	Capture   api.ProfileCapture
}

type ProfileCaptureStore interface {
	CreateProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture) error
	GetProfileCapture(ctx context.Context, accountID, appID, id string) (api.ProfileCapture, error)
	ListProfileCaptures(ctx context.Context, accountID, appID string) ([]api.ProfileCapture, error)
	ProfileCaptureBlobs(ctx context.Context, accountID, appID, id string) ([]ProfileCaptureBlob, error)
}

type ProfileCaptureQueue interface {
	// ClaimProfileCapture returns ErrNotFound when nothing is queued.
	ClaimProfileCapture(ctx context.Context, now time.Time) (ClaimedProfileCapture, error)
	// FinishProfileCapture stamps a capturing row ready or failed. It returns
	// ErrNotFound when the row is no longer capturing (expired meanwhile).
	FinishProfileCapture(ctx context.Context, id string, result api.ProfileCapture, blobs []ProfileCaptureBlob, now time.Time) error
	// ExpireProfileCaptures fails interrupted captures and deletes expired ones.
	ExpireProfileCaptures(ctx context.Context, now time.Time) (int64, error)
}

var (
	_ ProfileCaptureStore = (*MemStore)(nil)
	_ ProfileCaptureStore = (*PgStore)(nil)
	_ ProfileCaptureQueue = (*MemStore)(nil)
	_ ProfileCaptureQueue = (*PgStore)(nil)
)

// ProfileCaptureInterruptedAfter bounds a capture's claimed lifetime: its
// window, the guest grace and transport slack.
const ProfileCaptureInterruptedAfter = api.ProfileCaptureMaxDuration + api.ProfileCaptureGrace + time.Minute

// ProfileCaptureQueuedTimeout fails captures no schedd claimed.
const ProfileCaptureQueuedTimeout = 10 * time.Minute

const profileCaptureInterruptedReason = "capture was interrupted before it completed"

// profileCaptureRequestDoc is the stored request; lifecycle columns are
// merged on read.
func profileCaptureRequestDoc(c api.ProfileCapture) ([]byte, error) {
	return json.Marshal(map[string]any{"kinds": c.Kinds, "duration_seconds": c.DurationSeconds,
		"requested_instance_id": c.RequestedInstanceID, "processes": 0, "profiles": []api.ProfileCaptureProfile{}})
}

// profileCaptureResultDoc is merged into the stored document on finish.
func profileCaptureResultDoc(r api.ProfileCapture) ([]byte, error) {
	profiles := r.Profiles
	if profiles == nil {
		profiles = []api.ProfileCaptureProfile{}
	}
	return json.Marshal(map[string]any{"instance_id": r.InstanceID, "deployment_id": r.DeploymentID,
		"processes": r.Processes, "dropped": r.Dropped, "reason": r.Reason, "profiles": profiles})
}

func validProfileCaptureFinish(result api.ProfileCapture, blobs []ProfileCaptureBlob) error {
	if result.Status != api.ProfileCaptureReady && result.Status != api.ProfileCaptureFailed {
		return errors.New("profile capture must finish ready or failed")
	}
	if len(blobs) > api.ProfileCaptureMaxProfiles {
		return errors.New("profile capture has too many profiles")
	}
	for _, b := range blobs {
		if !api.ValidProfileKind(b.Kind) || len(b.Profile) == 0 || len(b.Profile) > api.ProfileMaxCompressedBytes || len(b.ProcessID) > 16 {
			return errors.New("profile capture blob exceeds bounds")
		}
	}
	return nil
}
