package objectstorage

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

type snapshotCopyProvider struct {
	source, destination string
	requestedVersion    string
}

func (p *snapshotCopyProvider) CopyObjectBetweenBuckets(_ context.Context, _, _ string, request CopyObjectRequest) (CopyObjectResult, error) {
	p.requestedVersion = request.SourceVersion
	return CopyObjectResult{ETag: "provider-ok"}, nil
}

func (p *snapshotCopyProvider) ReadObjectVersion(_ context.Context, _, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.source)), nil
}

func (p *snapshotCopyProvider) ReadObject(_ context.Context, _, _ string) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader(p.destination)), nil
}

func TestCopyAndVerifyObjectVersionRejectsCorruptDestination(t *testing.T) {
	provider := &snapshotCopyProvider{source: "original", destination: "original"}
	item := ObjectVersion{Key: "data.json", VersionID: "v1", Size: 8}
	result, err := CopyAndVerifyObjectVersion(context.Background(), provider, "source", "target", item)
	if err != nil || result.ETag != "provider-ok" || len(result.SHA256) != 64 || provider.requestedVersion != "v1" {
		t.Fatalf("verified copy = %+v, %v, version %q", result, err, provider.requestedVersion)
	}
	provider.destination = "corrupt!"
	if _, err := CopyAndVerifyObjectVersion(context.Background(), provider, "source", "target", item); !errors.Is(err, ErrObjectSnapshotCopyMismatch) {
		t.Fatalf("corrupt copy = %v", err)
	}
	provider.destination = "original"
	item.Size = 7
	if _, err := CopyAndVerifyObjectVersion(context.Background(), provider, "source", "target", item); !errors.Is(err, ErrObjectSnapshotCopyMismatch) {
		t.Fatalf("wrong manifest size = %v", err)
	}
}
