package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type ownedCleanupProbe struct {
	Provider
	BucketVersioningProvider
	BucketObjectLockProvider
	ObjectVersionLockProvider
	page                     ObjectVersionsPage
	retention                api.ObjectVersionRetention
	hold                     api.ObjectVersionLegalHold
	protected                bool
	readErr                  error
	deletes, protectionReads int
}

func (*ownedCleanupProbe) GetBucketVersioning(context.Context, string) (BucketVersioning, error) {
	return BucketVersioning{Status: "Enabled"}, nil
}
func (p *ownedCleanupProbe) GetBucketObjectLock(context.Context, string) (api.ObjectBucketObjectLockConfiguration, error) {
	return api.ObjectBucketObjectLockConfiguration{Enabled: p.protected}, nil
}
func (p *ownedCleanupProbe) ListObjectVersions(_ context.Context, _ string, cursor string, limit int32) (ObjectVersionsPage, error) {
	if cursor != "" || limit != api.ObjectOwnedCleanupBatchSize {
		return ObjectVersionsPage{}, ErrInvalid
	}
	return p.page, nil
}
func (p *ownedCleanupProbe) DeleteObjectVersion(context.Context, string, string, string) (VersionDeleteResult, error) {
	p.deletes++
	return VersionDeleteResult{}, nil
}
func (p *ownedCleanupProbe) DeleteMutableObject(_ context.Context, _, _, selector string) (MutableDeleteResult, error) {
	p.deletes++
	return MutableDeleteResult{ProviderVersionID: selector}, nil
}
func (p *ownedCleanupProbe) GetObjectVersionRetention(context.Context, string, string, string) (api.ObjectVersionRetention, error) {
	p.protectionReads++
	return p.retention, p.readErr
}
func (p *ownedCleanupProbe) GetObjectVersionLegalHold(context.Context, string, string, string) (api.ObjectVersionLegalHold, error) {
	p.protectionReads++
	return p.hold, p.readErr
}

func TestOwnedCleanupProtectionAndBounds(t *testing.T) {
	future, past := time.Now().Add(time.Hour), time.Now().Add(-time.Hour)
	duration := int32(1)
	data := ObjectVersionInventoryEntry{Key: "key", ProviderVersionID: "v1", SizeBytes: 1}
	for _, tc := range []struct {
		name    string
		mutate  func(*ownedCleanupProbe)
		want    error
		deletes int
	}{
		{"legal hold", func(p *ownedCleanupProbe) { p.hold.Status = "ON" }, ErrObjectProtected, 0},
		{"compliance", func(p *ownedCleanupProbe) {
			p.retention = api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &future}
		}, ErrObjectProtected, 0},
		{"governance", func(p *ownedCleanupProbe) {
			p.retention = api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &future}
		}, ErrObjectProtected, 0},
		{"event hold", func(p *ownedCleanupProbe) {
			p.retention = api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &duration}}
		}, ErrObjectProtected, 0},
		{"unreadable protection", func(p *ownedCleanupProbe) { p.readErr = ErrConfiguration }, ErrConfiguration, 0},
		{"invalid hold", func(p *ownedCleanupProbe) { p.hold.Status = "unknown" }, ErrUnavailable, 0},
		{"invalid retention", func(p *ownedCleanupProbe) { p.retention.Mode = "unknown" }, ErrUnavailable, 0},
		{"expired retention", func(p *ownedCleanupProbe) {
			p.retention = api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &past}
		}, nil, 1},
		{"marker", func(p *ownedCleanupProbe) { p.page.Items[0].DeleteMarker = true; p.readErr = ErrConfiguration }, nil, 1},
		{"null", func(p *ownedCleanupProbe) { p.page.Items[0].ProviderVersionID = "null" }, nil, 1},
		{"duplicate identity", func(p *ownedCleanupProbe) { p.page.Items = append(p.page.Items, data) }, ErrUnavailable, 0},
		{"invalid identity", func(p *ownedCleanupProbe) { p.page.Items[0].ProviderVersionID = "" }, ErrUnavailable, 0},
		{"negative size", func(p *ownedCleanupProbe) { p.page.Items[0].SizeBytes = -1 }, ErrUnavailable, 0},
		{"empty truncated", func(p *ownedCleanupProbe) { p.page.Items = nil; p.page.NextCursor = "opaque" }, ErrUnavailable, 0},
		{"oversized page", func(p *ownedCleanupProbe) {
			p.page.Items = make([]ObjectVersionInventoryEntry, api.ObjectOwnedCleanupBatchSize+1)
		}, ErrUnavailable, 0},
		{"bounded continuation", func(p *ownedCleanupProbe) { p.page.NextCursor = "opaque" }, ErrCleanupPending, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &ownedCleanupProbe{page: ObjectVersionsPage{Items: []ObjectVersionInventoryEntry{data}}, protected: true, hold: api.ObjectVersionLegalHold{Status: "OFF"}}
			tc.mutate(p)
			if err := CleanupOwnedBucketObjects(t.Context(), p, "owned"); !errors.Is(err, tc.want) || p.deletes != tc.deletes {
				t.Fatal("unsafe or unbounded cleanup", err, p.deletes)
			}
			if tc.name == "marker" && p.protectionReads != 0 {
				t.Fatal("marker protection read")
			}
		})
	}
}
