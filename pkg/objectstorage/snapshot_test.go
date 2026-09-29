package objectstorage

import (
	"context"
	"errors"
	"testing"
	"time"
)

type snapshotLister struct {
	enabled bool
	pages   map[string]ObjectVersionPage
}

func (s snapshotLister) BucketVersioningEnabled(context.Context, string) (bool, error) {
	return s.enabled, nil
}

func (s snapshotLister) ListObjectVersions(_ context.Context, _, cursor string, _ int32) (ObjectVersionPage, error) {
	return s.pages[cursor], nil
}

func TestCaptureObjectManifestSelectsPointInTimeAcrossPages(t *testing.T) {
	cutoff := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	version := func(key, id string, offset time.Duration, deleted bool) ObjectVersion {
		return ObjectVersion{Key: key, VersionID: id, Size: 12, LastModified: cutoff.Add(offset), Deleted: deleted}
	}
	provider := snapshotLister{enabled: true, pages: map[string]ObjectVersionPage{
		"": {Items: []ObjectVersion{
			version("a", "a-new", time.Second, false),
			version("a", "a-old", -time.Second, false),
			version("b", "b-delete", -time.Second, true),
		}, NextCursor: "page-2"},
		"page-2": {Items: []ObjectVersion{
			version("b", "b-old", -2*time.Second, false),
			version("c", "c-after", time.Second, true),
			version("c", "c-old", -time.Second, false),
		}},
	}}
	manifest, err := CaptureObjectManifest(context.Background(), provider, "source", cutoff, 2)
	if err != nil || len(manifest) != 2 || manifest[0].Key != "a" || manifest[0].VersionID != "a-old" || manifest[1].Key != "c" {
		t.Fatalf("manifest = %+v, %v", manifest, err)
	}
	if hash, err := ObjectManifestHash(manifest); err != nil || len(hash) != 64 {
		t.Fatalf("manifest hash = %q, %v", hash, err)
	}
}

func TestCaptureObjectManifestFailsClosedOnAmbiguousOrUnversionedSource(t *testing.T) {
	cutoff := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	versions := []ObjectVersion{
		{Key: "same", VersionID: "v1", LastModified: cutoff},
		{Key: "same", VersionID: "v2", LastModified: cutoff, Deleted: true},
	}
	provider := snapshotLister{enabled: true, pages: map[string]ObjectVersionPage{"": {Items: versions}}}
	if _, err := CaptureObjectManifest(context.Background(), provider, "source", cutoff, 1); !errors.Is(err, ErrAmbiguousObjectSnapshot) {
		t.Fatalf("ambiguous versions = %v", err)
	}
	provider.enabled = false
	if _, err := CaptureObjectManifest(context.Background(), provider, "source", cutoff, 1); !errors.Is(err, ErrUnsupported) {
		t.Fatalf("unversioned source = %v", err)
	}
	provider.enabled = true
	provider.pages[""] = ObjectVersionPage{NextCursor: "loop"}
	provider.pages["loop"] = ObjectVersionPage{NextCursor: "loop"}
	if _, err := CaptureObjectManifest(context.Background(), provider, "source", cutoff, 3); !errors.Is(err, ErrInvalid) {
		t.Fatalf("cursor loop = %v", err)
	}
}
