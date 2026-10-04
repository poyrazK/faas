// adr: 531
package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type snapshotRetentionObserver func(context.Context, string, ObjectVersion) (ObjectVersionRetention, error)

func (f snapshotRetentionObserver) ObserveObjectVersionRetention(ctx context.Context, bucket string, item ObjectVersion) (ObjectVersionRetention, error) {
	return f(ctx, bucket, item)
}

func TestObjectManifestRetentionRequiresExactUnexpiredProtection(t *testing.T) {
	until := time.Now().UTC().Add(time.Hour)
	items := []ObjectVersion{{Key: "a", VersionID: "v1", MetadataVersion: "3"}, {Key: "b", VersionID: "v2"}}
	for _, fault := range []string{"none", "wrong_version", "wrong_metadata", "missing_retention", "short_retention", "provider_error", "cancelled", "elapsed_deadline"} {
		t.Run(fault, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			deadline := until
			if fault == "elapsed_deadline" {
				deadline = time.Now().Add(-time.Second)
			}
			observer := snapshotRetentionObserver(func(_ context.Context, bucket string, item ObjectVersion) (ObjectVersionRetention, error) {
				calls++
				if bucket != "source" {
					t.Fatalf("changed source bucket: %q", bucket)
				}
				out := ObjectVersionRetention{VersionID: item.VersionID, MetadataVersion: item.MetadataVersion, RetainedUntil: until}
				if item.Key == "b" {
					switch fault {
					case "wrong_version":
						out.VersionID = "latest"
					case "wrong_metadata":
						out.MetadataVersion = "new"
					case "missing_retention":
						out.RetainedUntil = time.Time{}
					case "short_retention":
						out.RetainedUntil = until.Add(-time.Nanosecond)
					case "provider_error":
						return out, ErrNotFound
					case "cancelled":
						cancel()
					}
				}
				return out, nil
			})
			err := VerifyObjectManifestRetention(ctx, observer, "source", items, deadline)
			want := ErrObjectSnapshotRetentionUnavailable
			switch fault {
			case "none":
				if err != nil || calls != 2 {
					t.Fatalf("protected manifest: calls %d, error %v", calls, err)
				}
				return
			case "provider_error":
				want = ErrNotFound
			case "cancelled":
				want = context.Canceled
			case "elapsed_deadline":
				if calls != 0 {
					t.Fatal("expired worker reached provider")
				}
			}
			if !errors.Is(err, want) {
				t.Fatalf("retention fault: %v, want %v", err, want)
			}
		})
	}
}
