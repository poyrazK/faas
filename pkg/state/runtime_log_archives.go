package state

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/region"
)

// RuntimeLogArchiveBinding survives instance garbage collection. Only durable
// scheduler-owned instance/app/deployment rows supply its identity.
type RuntimeLogArchiveBinding struct {
	InstanceID, AppID, AccountID, DeploymentID, ComputeRegion string
}

type RuntimeLogArchive struct {
	RuntimeLogArchiveBinding
	Day, StorageKey, SHA256 string
	Bytes                   int64
	PublishedAt             time.Time
}

func (a RuntimeLogArchive) ValidReference() bool {
	return a.AppID != "" && a.AccountID != "" && a.DeploymentID != "" &&
		region.Valid(a.ComputeRegion) && region.ValidRuntimeLogInstance(a.InstanceID) &&
		region.ValidRuntimeLogDay(a.Day) && a.StorageKey == region.RuntimeLogArchiveKey(a.ComputeRegion, a.InstanceID, a.Day)
}

func (a RuntimeLogArchive) Published() bool {
	return a.ValidReference() && a.Bytes > 0 && region.ValidArtifactSHA256(a.SHA256) && !a.PublishedAt.IsZero()
}

type RuntimeLogArchiveReader interface {
	RuntimeLogArchiveByInstanceDay(context.Context, string, string) (RuntimeLogArchive, error)
}

type RuntimeLogArchiveCatalog interface {
	RuntimeLogArchiveReader
	CaptureRuntimeLogArchiveBinding(context.Context, string, string) (RuntimeLogArchiveBinding, error)
	RuntimeLogArchiveBindingByInstance(context.Context, string) (RuntimeLogArchiveBinding, error)
	RuntimeLogArchiveFragmentPublished(context.Context, string, string, string) (bool, error)
	PublishRuntimeLogArchiveFragment(context.Context, string, string, int64, string, string) error
	ReserveRuntimeLogArchive(context.Context, RuntimeLogArchive) error
	PublishRuntimeLogArchive(context.Context, string, string, int64, string) error
	ListAppRuntimeLogArchives(context.Context, string) ([]RuntimeLogArchive, error)
}

// Publication and hard deletion share this app-scoped fence. Reservations are
// durable before PUT, so a timed-out upload is still collected on deletion.
type RuntimeLogArchiveLocker interface {
	AcquireRuntimeLogArchivePublicationLock(context.Context, string) (func(context.Context), error)
}

func (s *MemStore) AcquireRuntimeLogArchivePublicationLock(ctx context.Context, appID string) (func(context.Context), error) {
	if appID == "" {
		return nil, ErrInvalidArgument
	}
	return s.AcquireDeploymentActivationLock(ctx, "runtime-logs-app:"+appID)
}

func (s *PgStore) AcquireRuntimeLogArchivePublicationLock(ctx context.Context, appID string) (func(context.Context), error) {
	if appID == "" {
		return nil, ErrInvalidArgument
	}
	return s.AcquireDeploymentActivationLock(ctx, "runtime-logs-app:"+appID)
}
