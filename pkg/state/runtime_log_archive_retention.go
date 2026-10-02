package state

import (
	"context"
	"errors"

	"github.com/onebox-faas/faas/pkg/region"
)

var ErrRuntimeLogArchiveExpired = errors.New("state: runtime log archive day expired")

// RuntimeLogArchiveRetentionBatchMax bounds housekeeping I/O, not customer usage.
const RuntimeLogArchiveRetentionBatchMax = 256

type RuntimeLogArchiveRetentionQuery struct {
	BeforeDay, AfterDay, AfterInstance string
	Limit                              int
}

func (q RuntimeLogArchiveRetentionQuery) valid() bool {
	return region.ValidRuntimeLogDay(q.BeforeDay) && region.ValidRuntimeLogDay(q.AfterDay) &&
		(q.AfterInstance == "" || region.ValidRuntimeLogInstance(q.AfterInstance)) &&
		q.Limit > 0 && q.Limit <= RuntimeLogArchiveRetentionBatchMax
}

// Callers hold the app publication lock from retirement through remote DELETE
// and metadata collection. Retirement survives both failures and worker restarts.
type RuntimeLogArchiveRetentionStore interface {
	RuntimeLogArchiveLocker
	ListRuntimeLogArchiveRetentionCandidates(context.Context, RuntimeLogArchiveRetentionQuery) ([]RuntimeLogArchive, error)
	RetireRuntimeLogArchive(context.Context, RuntimeLogArchive) error
	DeleteCollectedRuntimeLogArchive(context.Context, RuntimeLogArchive) error
	RuntimeLogArchiveDayExpired(context.Context, string, string) (bool, error)
}
