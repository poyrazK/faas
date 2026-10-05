package state

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

// ObjectWriteProtectionSnapshot freezes the accepted bucket policy and explicit
// write selection. Native bucket defaults cannot change through Gregale until
// all accepted writes drain; recovery compares against this snapshot only.
type ObjectWriteProtectionSnapshot struct {
	Enabled          bool                            `json:"enabled,omitempty"`
	Revision         int64                           `json:"revision,omitempty"`
	CapturedAt       *time.Time                      `json:"captured_at,omitempty"`
	DefaultRetention *api.ObjectLockDefaultRetention `json:"default_retention,omitempty"`
	Requested        api.ObjectWriteProtection       `json:"requested,omitzero"`
}

func (p ObjectWriteProtectionSnapshot) Empty() bool {
	return !p.Enabled && p.Revision == 0 && p.CapturedAt == nil && p.DefaultRetention == nil && p.Requested.Empty()
}
func (p ObjectWriteProtectionSnapshot) ValidInput() bool {
	return !p.Enabled && p.Revision == 0 && p.CapturedAt == nil && p.DefaultRetention == nil && p.Requested.Valid()
}
func (p ObjectWriteProtectionSnapshot) Valid() bool {
	if p.Empty() {
		return true
	}
	return p.Enabled && p.Revision >= 0 && p.Revision <= api.MaxObjectBucketObjectLockRevision && p.CapturedAt != nil && !p.CapturedAt.IsZero() && p.CapturedAt.Year() >= 1 && p.CapturedAt.Year() <= 9999 && p.Requested.Valid() && (p.DefaultRetention == nil || p.DefaultRetention.Valid() && p.DefaultRetention.DefaultEventHold == nil)
}
func (p ObjectWriteProtectionSnapshot) Clone() ObjectWriteProtectionSnapshot {
	p.Requested = p.Requested.Clone()
	if p.CapturedAt != nil {
		t := *p.CapturedAt
		p.CapturedAt = &t
	}
	c := api.ObjectBucketObjectLockConfiguration{Enabled: true, DefaultRetention: p.DefaultRetention}.Clone()
	p.DefaultRetention = c.DefaultRetention
	return p
}
func (p ObjectWriteProtectionSnapshot) Equal(other ObjectWriteProtectionSnapshot) bool {
	a, _ := json.Marshal(p)
	b, _ := json.Marshal(other)
	return bytes.Equal(a, b)
}
func (p ObjectWriteProtectionSnapshot) Proof() string {
	if p.Empty() {
		return ""
	}
	b, _ := json.Marshal(p)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// MinimumRetention binds the accepted duration to admission. Native S3 applies
// defaults at creation; its resulting deadline must be at least this minimum.
func (p ObjectWriteProtectionSnapshot) MinimumRetention() api.ObjectVersionRetention {
	if p.Requested.Retention != nil {
		return p.Requested.Retention.Clone()
	}
	if p.DefaultRetention == nil || p.CapturedAt == nil {
		return api.ObjectVersionRetention{}
	}
	d := p.DefaultRetention
	until := *p.CapturedAt
	if d.Days != nil {
		until = until.AddDate(0, 0, int(*d.Days))
	}
	if d.Years != nil {
		until = until.AddDate(int(*d.Years), 0, 0)
	}
	return (api.ObjectVersionRetention{Mode: d.Mode, RetainUntilDate: &until}).ForWrite()
}
func captureObjectWriteProtection(j ObjectBucketObjectLock, p ObjectWriteProtectionSnapshot, now time.Time) (ObjectWriteProtectionSnapshot, error) {
	if !p.ValidInput() || objectLockActive(j) {
		return p, ErrConflict
	}
	if !j.EnabledRequired && !j.NativeEnabledObserved && (j.ObservedConfiguration == nil || !j.ObservedConfiguration.Enabled) {
		if !p.Requested.Empty() {
			return p, ErrConflict
		}
		return ObjectWriteProtectionSnapshot{}, nil
	}
	if !j.ObservedKnown || j.ObservedConfiguration == nil || !j.ObservedConfiguration.Enabled || !j.ObservedConfiguration.Valid() || j.ObservedConfiguration.DefaultRetention != nil && j.ObservedConfiguration.DefaultRetention.DefaultEventHold != nil {
		return p, ErrConflict
	}
	now = now.UTC().Truncate(time.Millisecond)
	p.Enabled, p.Revision, p.CapturedAt = true, j.Revision, &now
	p.DefaultRetention = j.ObservedConfiguration.Clone().DefaultRetention
	p.Requested = p.Requested.ForWrite()
	if !p.Valid() || !p.MinimumRetention().Valid() || p.Requested.Retention != nil && !p.Requested.Retention.RetainUntilDate.After(now) {
		return p, ErrConflict
	}
	return p.Clone(), nil
}
func (m *MemStore) captureObjectWriteProtectionLocked(bucket string, p ObjectWriteProtectionSnapshot) (ObjectWriteProtectionSnapshot, error) {
	return captureObjectWriteProtection(m.objectBucketObjectLock[bucket], p, m.clock())
}
func captureObjectWriteProtectionSQL(ctx context.Context, db sqlc.DBTX, bucket string, p ObjectWriteProtectionSnapshot) (ObjectWriteProtectionSnapshot, error) {
	if err := sqlc.New().ObjectWriteProtectionBucketLock(ctx, db, mustPgUUID(bucket)); err != nil {
		return p, mapErr(err)
	}
	j, err := readObjectBucketObjectLock(ctx, db, bucket)
	if errors.Is(err, ErrNotFound) || errors.Is(err, pgx.ErrNoRows) {
		j = ObjectBucketObjectLock{}
	} else if err != nil {
		return p, err
	}
	now, err := sqlc.New().ObjectWriteProtectionClock(ctx, db)
	if err != nil {
		return p, mapErr(err)
	}
	return captureObjectWriteProtection(j, p, now.Time)
}
func protectionSnapshotJSON(p ObjectWriteProtectionSnapshot) ([]byte, error) {
	if !p.Valid() {
		return nil, ErrConflict
	}
	b, err := json.Marshal(p)
	if err != nil || len(b) > api.MaxObjectWriteProtectionSnapshotBytes {
		return nil, ErrConflict
	}
	return b, nil
}
func protectionSnapshotFromJSON(b []byte) (ObjectWriteProtectionSnapshot, error) {
	var p ObjectWriteProtectionSnapshot
	if len(b) == 0 || len(b) > api.MaxObjectWriteProtectionSnapshotBytes || len(bytes.TrimSpace(b)) < 2 || bytes.TrimSpace(b)[0] != '{' {
		return p, ErrConflict
	}
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if d.Decode(&p) != nil || d.Decode(new(any)) != io.EOF || !p.Valid() {
		return ObjectWriteProtectionSnapshot{}, ErrConflict
	}
	return p, nil
}
func validTrackedProtectionResult(old, c ObjectUploadCompletion) bool {
	return old.Protection.Equal(c.Protection) && old.Protection.Valid() && (c.Status != "completed" && c.VerifiedProtection == "" || c.Status == "completed" && c.VerifiedProtection == old.Protection.Proof() && (old.Protection.Empty() || c.ProviderVersionID != "" && c.ProviderVersionID != "null"))
}

func sameMultipartProtectionRequest(old ObjectMultipartUpload, p ObjectWriteProtectionSnapshot) bool {
	a, _ := json.Marshal(old.Protection.Requested)
	b, _ := json.Marshal(p.Requested.ForWrite())
	return bytes.Equal(a, b)
}
