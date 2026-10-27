package state

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// On-demand profile captures (ADR-1155). apid is the producer (queue + read),
// schedd the consumer (claim, finish, expire). Neither interface is part of
// Store; callers assert them like the other profiling stores.

var (
	ErrProfileCaptureQuota  = errors.New("profile capture limit reached for this account")
	ErrProfileCaptureActive = errors.New("a profile capture is already queued or running for this app")
	// ErrProfileCaptureRecent rejects an automatic capture inside the
	// per-app minimum interval for its trigger.
	ErrProfileCaptureRecent = errors.New("an automatic profile capture ran recently for this app")
	// ErrProfileCaptureStorage matches a ProfileCaptureStorageError.
	ErrProfileCaptureStorage = errors.New("profile capture storage is full for this account")
)

// ProfileCaptureStorageError rejects a capture that might not fit the
// account's capture storage limit. Used counts the stored profiles plus
// api.ProfileCaptureMaxProfileBytes for each capture still queued or running.
// Automatic reports the automatic captures' share (Limit is that share and
// Used their bytes) rather than the whole limit.
type ProfileCaptureStorageError struct {
	Limit, Used int64
	Automatic   bool
}

func (e *ProfileCaptureStorageError) Error() string {
	if e.Automatic {
		return fmt.Sprintf("%v: automatic captures use %d of their %d bytes", ErrProfileCaptureStorage, e.Used, e.Limit)
	}
	return fmt.Sprintf("%v: %d of %d bytes used", ErrProfileCaptureStorage, e.Used, e.Limit)
}

func (e *ProfileCaptureStorageError) Is(target error) bool { return target == ErrProfileCaptureStorage }

// checkProfileCaptureStorage admits a capture when the account's stored and
// reserved bytes leave room for one more capture of the largest size.
func checkProfileCaptureStorage(storedBytes, active, limit int64) error {
	used := storedBytes + active*api.ProfileCaptureMaxProfileBytes
	if used+api.ProfileCaptureMaxProfileBytes > limit {
		return &ProfileCaptureStorageError{Limit: limit, Used: used}
	}
	return nil
}

// profileCaptureUsage is an account's capture storage: stored bytes of
// unexpired captures and captures still queued or running, in total and for
// automatic captures alone.
type profileCaptureUsage struct {
	StoredBytes, Active                   int64
	AutomaticStoredBytes, AutomaticActive int64
}

// rotatableProfileCapture is a finished automatic capture without an OOM
// link, a candidate to rotate out; candidates come oldest first.
type rotatableProfileCapture struct {
	ID    string
	Bytes int64
}

// planProfileCaptureStorage admits a capture under the account's capture
// storage limit (ADR-1155) and returns the automatic captures to delete
// first. A manual capture may use the whole limit. An automatic capture may
// use only api.ProfileCaptureAutomaticStoragePercent of it, so automatic
// captures never fill the room manual captures need; when that share is full
// the oldest candidates are rotated out. Nothing is rotated out unless the
// new capture then fits.
func planProfileCaptureStorage(u profileCaptureUsage, automatic bool, limit int64, candidates []rotatableProfileCapture) ([]string, error) {
	var rotate []string
	if automatic {
		share := limit * api.ProfileCaptureAutomaticStoragePercent / 100
		used := func() int64 { return u.AutomaticStoredBytes + u.AutomaticActive*api.ProfileCaptureMaxProfileBytes }
		inUse := used()
		for _, c := range candidates {
			if used()+api.ProfileCaptureMaxProfileBytes <= share {
				break
			}
			rotate = append(rotate, c.ID)
			u.AutomaticStoredBytes -= c.Bytes
			u.StoredBytes -= c.Bytes
		}
		if used()+api.ProfileCaptureMaxProfileBytes > share {
			return nil, &ProfileCaptureStorageError{Limit: share, Used: inUse, Automatic: true}
		}
	}
	if err := checkProfileCaptureStorage(u.StoredBytes, u.Active, limit); err != nil {
		return nil, err
	}
	return rotate, nil
}

// automaticShareFull reports whether an automatic capture needs candidates
// to rotate out, so stores read them only then.
func (u profileCaptureUsage) automaticShareFull(limit int64) bool {
	share := limit * api.ProfileCaptureAutomaticStoragePercent / 100
	return u.AutomaticStoredBytes+(u.AutomaticActive+1)*api.ProfileCaptureMaxProfileBytes > share
}

// profileCaptureHourlyLimit is the hourly quota a capture counts against.
func profileCaptureHourlyLimit(automatic bool) int64 {
	if automatic {
		return api.ProfileCaptureAutomaticMaxPerAccountHour
	}
	return api.ProfileCaptureMaxPerAccountHour
}

// ProfileCaptureBlob is one collector's unparsed pprof profile.
type ProfileCaptureBlob struct {
	Kind      string
	ProcessID string
	Profile   []byte
}

// ProfileCaptureOOM is the OOM kill linked to a capture (ADR-1155).
type ProfileCaptureOOM struct {
	KilledAt       time.Time
	PeakMB, PlanMB int
}

// ClaimedProfileCapture is a queued capture schedd now owns.
type ClaimedProfileCapture struct {
	ID        string
	AppID     string
	AccountID string
	Capture   api.ProfileCapture
}

type ProfileCaptureStore interface {
	// CreateProfileCapture queues a manual capture. storageLimit is the
	// account plan's ProfilingLimits.CaptureStorageBytes; a capture that might
	// not fit returns a *ProfileCaptureStorageError.
	CreateProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture, storageLimit int64) error
	GetProfileCapture(ctx context.Context, accountID, appID, id string) (api.ProfileCapture, error)
	// DeleteProfileCapture deletes one of the app's captures and its
	// profiles, in any status, or returns ErrNotFound. A capture still
	// running finishes into nothing.
	DeleteProfileCapture(ctx context.Context, accountID, appID, id string) error
	ListProfileCaptures(ctx context.Context, accountID, appID string) ([]api.ProfileCapture, error)
	ProfileCaptureBlobs(ctx context.Context, accountID, appID, id string) ([]ProfileCaptureBlob, error)
	// LatestOOMProfileCapture returns the deployment's newest ready capture
	// linked to an OOM kill, or ErrNotFound.
	LatestOOMProfileCapture(ctx context.Context, accountID, appID, deploymentID string) (api.ProfileCapture, error)
	// ProfileCaptureForRequest returns the newest capture taken while the
	// request with this trace id was in flight, or ErrNotFound.
	ProfileCaptureForRequest(ctx context.Context, accountID, appID, traceID string) (api.ProfileCapture, error)
}

type ProfileCaptureQueue interface {
	// ClaimProfileCapture returns ErrNotFound when nothing is queued.
	ClaimProfileCapture(ctx context.Context, now time.Time) (ClaimedProfileCapture, error)
	// FinishProfileCapture stamps a capturing row ready or failed. It returns
	// ErrNotFound when the row is no longer capturing (expired meanwhile).
	// A ready automatic or OOM-linked capture enqueues profile.capture_ready
	// in the same transaction.
	FinishProfileCapture(ctx context.Context, id string, result api.ProfileCapture, blobs []ProfileCaptureBlob, now time.Time) error
	// ExpireProfileCaptures fails interrupted captures and deletes expired ones.
	ExpireProfileCaptures(ctx context.Context, now time.Time) (int64, error)
	// StartProfileCapture records the instance a capturing row runs on, so
	// an OOM kill during the window can find it.
	StartProfileCapture(ctx context.Context, id, instanceID, deploymentID string) error
	// StampProfileCaptureOOM links an OOM kill to the newest heap capture
	// started on that instance since `since`, returning its id or
	// ErrNotFound. Re-stamping the same kill is idempotent. The first kill
	// linked to an already ready capture enqueues profile.capture_oom_linked;
	// a capture still running announces it when it finishes.
	StampProfileCaptureOOM(ctx context.Context, appID, instanceID string, oom ProfileCaptureOOM, since time.Time) (string, error)
	// QueueAutomaticProfileCapture queues a platform-triggered capture under
	// the same quota, storage and single-active rules as CreateProfileCapture,
	// plus a per-app minimum interval for c.Trigger.
	QueueAutomaticProfileCapture(ctx context.Context, accountID, appID string, c api.ProfileCapture, minInterval time.Duration, storageLimit int64) error
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
	trigger := c.Trigger
	if trigger == "" {
		trigger = api.ProfileCaptureTriggerManual
	}
	doc := map[string]any{"kinds": c.Kinds, "duration_seconds": c.DurationSeconds,
		"requested_instance_id": c.RequestedInstanceID, "trigger": trigger, "trigger_detail": c.TriggerDetail,
		"processes": 0, "profiles": []api.ProfileCaptureProfile{}}
	if c.RequestID != "" {
		doc["request_id"] = c.RequestID
	}
	if c.RequestTraceID != "" {
		doc["request_trace_id"] = c.RequestTraceID
	}
	return json.Marshal(doc)
}

// profileCaptureResultDoc is merged into the stored document on finish.
func profileCaptureResultDoc(r api.ProfileCapture) ([]byte, error) {
	profiles := r.Profiles
	if profiles == nil {
		profiles = []api.ProfileCaptureProfile{}
	}
	return json.Marshal(map[string]any{"instance_id": r.InstanceID, "deployment_id": r.DeploymentID,
		"processes": r.Processes, "dropped": r.Dropped, "reason": r.Reason, "partial": r.Partial, "profiles": profiles})
}

func validProfileCaptureFinish(result api.ProfileCapture, blobs []ProfileCaptureBlob) error {
	if result.Status != api.ProfileCaptureReady && result.Status != api.ProfileCaptureFailed {
		return errors.New("profile capture must finish ready or failed")
	}
	if len(blobs) > api.ProfileCaptureMaxProfiles {
		return errors.New("profile capture has too many profiles")
	}
	for _, b := range blobs {
		if !ProfileCaptureBlobFits(b) {
			return errors.New("profile capture blob exceeds bounds")
		}
	}
	return nil
}

// ProfileCaptureBlobFits reports whether one collector's profile is within
// the bounds FinishProfileCapture stores (profile_capture_data's CHECKs).
func ProfileCaptureBlobFits(b ProfileCaptureBlob) bool {
	return api.ValidCaptureProfileKind(b.Kind) && len(b.Profile) > 0 && len(b.Profile) <= api.ProfileMaxCompressedBytes && len(b.ProcessID) <= 16
}

// profileCaptureNotice returns the webhook payload for an announced capture
// (ADR-1155). Manual captures without an OOM kill are not announced: whoever
// asked for them is already waiting.
func profileCaptureNotice(event AppWebhookEvent, c api.ProfileCapture, slug string) ([]byte, bool, error) {
	if event != AppWebhookEventProfileCaptureReady && event != AppWebhookEventProfileCaptureOOMLinked {
		return nil, false, errors.New("profile capture notice: unsupported event " + string(event))
	}
	if !c.Announced() || event == AppWebhookEventProfileCaptureOOMLinked && c.OOMKilledAt == nil {
		return nil, false, nil
	}
	body, err := json.Marshal(api.ProfileCaptureWebhookPayload{Version: api.ProfileCaptureWebhookVersion, Capture: c,
		CapturePath: "/dashboard/apps/" + url.PathEscape(slug) + "/profiles/captures/" + url.PathEscape(c.ID),
		APIPath:     "/v1/apps/" + url.PathEscape(slug) + "/profiles/captures/" + url.PathEscape(c.ID)})
	if err != nil {
		return nil, false, err
	}
	return body, true, nil
}
