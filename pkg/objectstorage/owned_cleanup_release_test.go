package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// lockedVersion is one version in releaseCleanupProbe with its protection.
type lockedVersion struct {
	retention api.ObjectVersionRetention
	hold      string
	deleted   bool
}

// releaseCleanupProbe is a lock-enabled versioned bucket whose versions each
// carry their own protection, listed in pages of api.ObjectOwnedCleanupBatchSize.
type releaseCleanupProbe struct {
	Provider
	BucketVersioningProvider
	BucketObjectLockProvider
	keys                           []string
	versions                       map[string]*lockedVersion
	lists, bypassDeletes, holdsOff int
	eventReleases                  int
	eventDuration                  time.Duration
}

func newReleaseCleanupProbe() *releaseCleanupProbe {
	return &releaseCleanupProbe{versions: map[string]*lockedVersion{}}
}

func (p *releaseCleanupProbe) add(key string, v lockedVersion) {
	if v.hold == "" {
		v.hold = "OFF"
	}
	p.keys = append(p.keys, key)
	p.versions[key] = &v
}

func (*releaseCleanupProbe) GetBucketVersioning(context.Context, string) (BucketVersioning, error) {
	return BucketVersioning{Status: "Enabled"}, nil
}
func (*releaseCleanupProbe) GetBucketObjectLock(context.Context, string) (api.ObjectBucketObjectLockConfiguration, error) {
	return api.ObjectBucketObjectLockConfiguration{Enabled: true}, nil
}
func (p *releaseCleanupProbe) ListObjectVersions(_ context.Context, _ string, cursor string, limit int32) (ObjectVersionsPage, error) {
	p.lists++
	start := 0
	if cursor != "" {
		n, err := strconv.Atoi(cursor)
		if err != nil {
			return ObjectVersionsPage{}, ErrInvalid
		}
		start = n
	}
	var page ObjectVersionsPage
	i := start
	for ; i < len(p.keys) && len(page.Items) < int(limit); i++ {
		if !p.versions[p.keys[i]].deleted {
			page.Items = append(page.Items, ObjectVersionInventoryEntry{Key: p.keys[i], ProviderVersionID: "v1", SizeBytes: 1})
		}
	}
	if i < len(p.keys) {
		page.NextCursor = strconv.Itoa(i)
	}
	return page, nil
}
func (p *releaseCleanupProbe) protectedNow(v *lockedVersion) bool {
	r := v.retention
	return v.hold == "ON" || r.EventHold == "ON" || r.RetainUntilDate != nil && r.RetainUntilDate.After(time.Now())
}
func (p *releaseCleanupProbe) DeleteObjectVersion(_ context.Context, _, key, _ string) (VersionDeleteResult, error) {
	v := p.versions[key]
	if p.protectedNow(v) {
		return VersionDeleteResult{}, ErrDeletionRejected
	}
	v.deleted = true
	return VersionDeleteResult{}, nil
}
func (p *releaseCleanupProbe) DeleteObjectVersionBypassingGovernance(_ context.Context, _, key, _ string) (VersionDeleteResult, error) {
	v := p.versions[key]
	if v.hold == "ON" || v.retention.EventHold == "ON" || v.retention.Mode == "COMPLIANCE" && p.protectedNow(v) {
		return VersionDeleteResult{}, ErrDeletionRejected
	}
	p.bypassDeletes++
	v.deleted = true
	return VersionDeleteResult{}, nil
}
func (p *releaseCleanupProbe) GetObjectVersionRetention(_ context.Context, _, key, _ string) (api.ObjectVersionRetention, error) {
	return p.versions[key].retention.Clone(), nil
}
func (p *releaseCleanupProbe) PutObjectVersionRetention(_ context.Context, _, key, _ string, r api.ObjectVersionRetention, bypass bool) error {
	v := p.versions[key]
	if r.EventHold != "OFF" || v.retention.EventHold != "ON" || r.Mode != v.retention.Mode || bypass != (r.Mode == "GOVERNANCE") {
		return fmt.Errorf("unexpected retention change %+v bypass=%v", r, bypass)
	}
	p.eventReleases++
	until := time.Now().Add(p.eventDuration)
	v.retention = api.ObjectVersionRetention{Mode: r.Mode, RetainUntilDate: &until, EventHold: "OFF"}
	return nil
}
func (p *releaseCleanupProbe) GetObjectVersionLegalHold(_ context.Context, _, key, _ string) (api.ObjectVersionLegalHold, error) {
	return api.ObjectVersionLegalHold{Status: p.versions[key].hold}, nil
}
func (p *releaseCleanupProbe) PutObjectVersionLegalHold(_ context.Context, _, key, _ string, h api.ObjectVersionLegalHold) error {
	if h.Status != "OFF" {
		return ErrInvalid
	}
	p.holdsOff++
	p.versions[key].hold = "OFF"
	return nil
}

func (p *releaseCleanupProbe) remaining() []string {
	var out []string
	for _, k := range p.keys {
		if !p.versions[k].deleted {
			out = append(out, k)
		}
	}
	return out
}

// TestOwnedCleanupContinuesPastProtectedVersions pins ADR-20261111123000
// decision 8 (object storage audit OS-6): a protected version no longer stops
// the pass, account erasure releases holds and bypasses GOVERNANCE, and only
// COMPLIANCE retention is kept, with a retry at its earliest end.
func TestOwnedCleanupContinuesPastProtectedVersions(t *testing.T) {
	soon, later := time.Now().Add(48*time.Hour), time.Now().Add(400*24*time.Hour)
	fill := func(p *releaseCleanupProbe) {
		p.add("held", lockedVersion{hold: "ON"})
		p.add("governance", lockedVersion{retention: api.ObjectVersionRetention{Mode: "GOVERNANCE", RetainUntilDate: &later}})
		p.add("compliance-later", lockedVersion{retention: api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &later}})
		p.add("compliance-soon", lockedVersion{retention: api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &soon}})
		p.add("plain", lockedVersion{})
	}

	t.Run("teardown keeps protected versions and deletes the rest", func(t *testing.T) {
		p := newReleaseCleanupProbe()
		fill(p)
		err := CleanupOwnedBucketObjects(t.Context(), p, "owned")
		var kept *ProtectedUntilError
		if !errors.As(err, &kept) || !errors.Is(err, ErrObjectProtected) || kept.Kept != 4 || !kept.Undated {
			t.Fatalf("err = %v %+v", err, kept)
		}
		if got := p.remaining(); len(got) != 4 || p.versions["plain"].deleted != true || p.holdsOff != 0 || p.bypassDeletes != 0 {
			t.Fatalf("remaining %v holdsOff=%d bypass=%d", got, p.holdsOff, p.bypassDeletes)
		}
		if d := kept.RetryAfter(time.Now()); d != api.ObjectOwnedCleanupProtectedRetryMin {
			t.Fatalf("a hold is rechecked after the minimum, got %v", d)
		}
	})

	t.Run("erasure releases what it can and waits for compliance", func(t *testing.T) {
		p := newReleaseCleanupProbe()
		fill(p)
		err := CleanupOwnedBucketObjectsWith(t.Context(), p, "owned", OwnedCleanupOptions{ReleaseProtection: true})
		var kept *ProtectedUntilError
		if !errors.As(err, &kept) || kept.Kept != 2 || kept.Undated || !kept.Until.Equal(soon) {
			t.Fatalf("err = %v %+v", err, kept)
		}
		if got := p.remaining(); len(got) != 2 || got[0] != "compliance-later" || got[1] != "compliance-soon" {
			t.Fatalf("remaining %v", got)
		}
		if p.holdsOff != 1 || p.bypassDeletes != 1 {
			t.Fatalf("holdsOff=%d bypass=%d", p.holdsOff, p.bypassDeletes)
		}
		if d := kept.RetryAfter(time.Now()); d < 47*time.Hour || d > 49*time.Hour {
			t.Fatalf("retry at the earliest retain-until, got %v", d)
		}
	})

	t.Run("erasure releases event holds and keeps compliance from the release", func(t *testing.T) {
		p := newReleaseCleanupProbe()
		p.eventDuration = 72 * time.Hour
		days := int32(3)
		p.add("event-governance", lockedVersion{retention: api.ObjectVersionRetention{Mode: "GOVERNANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}}})
		p.add("event-compliance", lockedVersion{retention: api.ObjectVersionRetention{Mode: "COMPLIANCE", EventHold: "ON", EventHoldDuration: &api.ObjectRetentionPeriod{Days: &days}}})
		err := CleanupOwnedBucketObjectsWith(t.Context(), p, "owned", OwnedCleanupOptions{ReleaseProtection: true})
		var kept *ProtectedUntilError
		if !errors.As(err, &kept) || kept.Kept != 1 || kept.Until.IsZero() {
			t.Fatalf("err = %v %+v", err, kept)
		}
		if got := p.remaining(); len(got) != 1 || got[0] != "event-compliance" || p.eventReleases != 2 || p.bypassDeletes != 1 {
			t.Fatalf("remaining %v releases=%d bypass=%d", got, p.eventReleases, p.bypassDeletes)
		}
	})

	t.Run("reads past a full page of kept versions", func(t *testing.T) {
		p := newReleaseCleanupProbe()
		for i := range api.ObjectOwnedCleanupBatchSize {
			p.add(fmt.Sprintf("locked-%03d", i), lockedVersion{retention: api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &later}})
		}
		p.add("plain", lockedVersion{})
		p.add("plain-2", lockedVersion{})
		err := CleanupOwnedBucketObjectsWith(t.Context(), p, "owned", OwnedCleanupOptions{ReleaseProtection: true})
		if !errors.Is(err, ErrObjectProtected) || !p.versions["plain"].deleted || !p.versions["plain-2"].deleted || p.lists != 2 {
			t.Fatalf("err=%v lists=%d remaining=%d", err, p.lists, len(p.remaining()))
		}
	})

	t.Run("stops after a bounded number of pages that hold only kept versions", func(t *testing.T) {
		p := newReleaseCleanupProbe()
		for i := range (api.ObjectOwnedCleanupProtectedPages + 1) * api.ObjectOwnedCleanupBatchSize {
			p.add(fmt.Sprintf("locked-%04d", i), lockedVersion{retention: api.ObjectVersionRetention{Mode: "COMPLIANCE", RetainUntilDate: &later}})
		}
		p.add("plain", lockedVersion{})
		err := CleanupOwnedBucketObjectsWith(t.Context(), p, "owned", OwnedCleanupOptions{ReleaseProtection: true})
		var kept *ProtectedUntilError
		if !errors.As(err, &kept) || p.lists != api.ObjectOwnedCleanupProtectedPages || p.versions["plain"].deleted {
			t.Fatalf("err=%v lists=%d", err, p.lists)
		}
		if d := kept.RetryAfter(time.Now()); d != api.ObjectOwnedCleanupProtectedRetryMax {
			t.Fatalf("a far date is rechecked after the maximum, got %v", d)
		}
	})
}
