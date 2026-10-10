package state

import (
	"context"
	"crypto/sha256"
	"fmt"
	"time"
)

// AppTaskInteractive is the interactive-session intent admitted with a manual
// app task (ADR-958). Only the SHA-256 digest of the one-time attach token is
// ever stored.
type AppTaskInteractive struct {
	TTY         bool
	TokenSHA256 []byte
}

// AppTaskAttach is the stored interactive-session record of one app task.
// NodeID is empty until schedd records where the running task VM lives.
type AppTaskAttach struct {
	TaskID         string
	TTY            bool
	TokenSHA256    []byte
	NodeID         string
	NodeRecordedAt *time.Time
	CreatedAt      time.Time
}

// AppTaskAttachTarget is what a gateway needs to route one attach.
type AppTaskAttachTarget struct {
	TaskID     string
	TaskStatus AppTaskStatus
	TTY        bool
	NodeID     string
}

const maxAppTaskAttachNodeIDBytes = 255

// AppTaskAttachStore holds interactive-session records. The record is
// created atomically with its task by CreateAppTask.
type AppTaskAttachStore interface {
	// AppTaskAttachByTask returns the interactive record of a task, or
	// ErrNotFound for an ordinary batch task.
	AppTaskAttachByTask(ctx context.Context, taskID string) (AppTaskAttach, error)
	// RecordAppTaskAttachNode stores the node of a running interactive task.
	// It returns ErrAppTaskLeaseLost unless the task is running under
	// leaseToken.
	RecordAppTaskAttachNode(ctx context.Context, taskID, leaseToken, nodeID string, recordedAt time.Time) error
	// AppTaskAttachTarget returns the routing view of an interactive task
	// owned by the account and app, or ErrNotFound.
	AppTaskAttachTarget(ctx context.Context, accountID, appID, taskID string) (AppTaskAttachTarget, error)
}

func validateAppTaskInteractive(params CreateAppTaskParams) error {
	if params.Interactive == nil {
		return nil
	}
	if params.Kind != AppTaskKindManual || params.RequireLiveDeployment || params.CronID != "" || params.ScheduledFor != nil ||
		params.ExclusiveOperationID != "" || params.BindingVerification != nil || params.FailureRules != nil ||
		params.OccurrenceID != "" || params.StartDeadlineAt != nil || params.RetryMax != 0 {
		return fmt.Errorf("%w: interactive sessions are plain manual tasks", ErrAppTaskInvalid)
	}
	if len(params.Interactive.TokenSHA256) != sha256.Size {
		return fmt.Errorf("%w: attach token digest must be SHA-256", ErrAppTaskInvalid)
	}
	return nil
}

func validAppTaskAttachNodeID(nodeID string) bool {
	return nodeID != "" && len(nodeID) <= maxAppTaskAttachNodeIDBytes
}

func cloneAppTaskInteractive(value *AppTaskInteractive) *AppTaskInteractive {
	if value == nil {
		return nil
	}
	return &AppTaskInteractive{TTY: value.TTY, TokenSHA256: append([]byte(nil), value.TokenSHA256...)}
}

func cloneAppTaskAttach(value AppTaskAttach) AppTaskAttach {
	value.TokenSHA256 = append([]byte(nil), value.TokenSHA256...)
	value.NodeRecordedAt = cloneAppTaskTimePtr(value.NodeRecordedAt)
	return value
}
