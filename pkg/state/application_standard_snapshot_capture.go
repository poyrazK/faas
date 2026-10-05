package state

// adr: 435. A catalog record retains history; only a freshly issued grant is authority.

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/runtimeadmission"
)

type ApplicationStandardSnapshotCaptureStore interface {
	IssueApplicationStandardSnapshotCapture(context.Context, string, ApplicationStandardSnapshotCaptureRequest) (runtimeadmission.SnapshotGrant, error)
	PublishApplicationStandardSnapshotCapture(context.Context, runtimeadmission.SnapshotAcknowledgment) error
	GetApplicationStandardSnapshotCapture(context.Context, string, string, string, string) (ApplicationStandardSnapshotCaptureRecord, error)
}

type ApplicationStandardSnapshotCaptureRequest struct {
	Token, InstanceID, MemoryKey, VMStateKey, PrivateDriveKey, FCVersion, Mode string
	BeforeCheckpoint                                                           bool
	SourceStartedAtUnixNano                                                    int64
}

type ApplicationStandardSnapshotCaptureRecord struct {
	ExpectedState         string
	Grant                 runtimeadmission.SnapshotGrant
	Acknowledgment        *runtimeadmission.SnapshotAcknowledgment
	CreatedAt, ReceivedAt time.Time
	inputs                json.RawMessage // Private immutable source inputs, also retained by PostgreSQL.
}

func (r ApplicationStandardSnapshotCaptureRecord) Clone() ApplicationStandardSnapshotCaptureRecord {
	r.Grant = r.Grant.Clone()
	r.inputs = append(json.RawMessage(nil), r.inputs...)
	if r.Acknowledgment != nil {
		owned := r.Acknowledgment.Clone()
		r.Acknowledgment = &owned
	}
	return r
}

func (r ApplicationStandardSnapshotCaptureRequest) validate(expectedState string) error {
	for _, value := range []string{r.Token, r.InstanceID} {
		id, err := uuid.Parse(value)
		if err != nil || id == uuid.Nil || id.String() != value {
			return ErrInvalidArgument
		}
	}
	if !standardSnapshotStateMode(expectedState, r.Mode) || r.SourceStartedAtUnixNano <= 0 {
		return ErrInvalidArgument
	}
	return nil
}

func standardSnapshotStateMode(state, mode string) bool {
	return state == string(StateRunning) && mode == "warm" || state == string(StateSnapshotting) && mode == "park" || state == string(StateMigrating) && mode == "migration"
}

func (r ApplicationStandardSnapshotCaptureRequest) grant(parent runtimeadmission.Receipt, now, expires time.Time) runtimeadmission.SnapshotGrant {
	return runtimeadmission.SnapshotGrant{Version: runtimeadmission.SnapshotGrantVersion, Token: r.Token, Parent: parent.Clone(), MemoryKey: r.MemoryKey, VMStateKey: r.VMStateKey, PrivateDriveKey: r.PrivateDriveKey, FCVersion: r.FCVersion, Mode: r.Mode, BeforeCheckpoint: r.BeforeCheckpoint, SourceStartedAtUnixNano: r.SourceStartedAtUnixNano, IssuedAtUnixNano: now.UnixNano(), ExpiresAtUnixNano: expires.UnixNano()}
}

func standardSnapshotIssueRetry(old ApplicationStandardSnapshotCaptureRecord, state string, candidate runtimeadmission.SnapshotGrant, now, deadline time.Time) (runtimeadmission.SnapshotGrant, error) {
	candidate.IssuedAtUnixNano, candidate.ExpiresAtUnixNano = old.Grant.IssuedAtUnixNano, old.Grant.ExpiresAtUnixNano
	if old.ExpectedState != state || old.Acknowledgment != nil || !candidate.Equal(old.Grant) || old.Grant.Validate(now) != nil || !standardNativeGrantWithinArtifactLease(old.Grant.ExpiresAtUnixNano, deadline) {
		return runtimeadmission.SnapshotGrant{}, ErrConflict
	}
	return old.Grant.Clone(), nil
}

func standardSnapshotRecordValid(r ApplicationStandardSnapshotCaptureRecord) error {
	// Validation at historical issue/completion time never renews a grant.
	if !standardSnapshotStateMode(r.ExpectedState, r.Grant.Mode) || r.CreatedAt.IsZero() || r.Grant.Validate(time.Unix(0, r.Grant.IssuedAtUnixNano)) != nil {
		return ErrApplicationStandardRuntimeStale
	}
	if r.Acknowledgment != nil && (r.ReceivedAt.IsZero() || r.Acknowledgment.Check(r.Grant, time.Unix(0, r.Acknowledgment.CompletedAtUnixNano)) != nil) {
		return ErrApplicationStandardRuntimeStale
	}
	if r.Acknowledgment == nil && !r.ReceivedAt.IsZero() {
		return ErrApplicationStandardRuntimeStale
	}
	return nil
}
