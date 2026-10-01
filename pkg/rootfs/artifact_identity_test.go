package rootfs

// adr: 393

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

type artifactPutBackend struct {
	storage.StorageBackend
	put func(context.Context, string, io.Reader) error
}

func (b artifactPutBackend) Put(ctx context.Context, key string, r io.Reader) error {
	return b.put(ctx, key, r)
}

func TestArtifactPublicationRequiresCompleteOutput(t *testing.T) {
	// Include unused capacity. ContentBytes would omit these bytes.
	payload := append([]byte("ext4 metadata and content"), make([]byte, 4096)...)
	for _, mode := range []string{"complete", "exact length without EOF", "prefix", "failed write", "cancelled write", "cancelled exact length"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "layer.ext4")
			if err := os.WriteFile(path, payload, 0o600); err != nil {
				t.Fatal(err)
			}
			f, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			defer f.Close()
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			var published []byte
			failure := errors.New("object upload failed")
			be := artifactPutBackend{put: func(_ context.Context, _ string, r io.Reader) error {
				switch mode {
				case "prefix":
					published, err = io.ReadAll(io.LimitReader(r, 5))
				case "exact length without EOF", "cancelled exact length":
					published, err = io.ReadAll(io.LimitReader(r, int64(len(payload))))
				default:
					published, err = io.ReadAll(r)
				}
				if err != nil {
					return err
				}
				if mode == "failed write" {
					return failure
				}
				if mode == "cancelled write" || mode == "cancelled exact length" {
					cancel()
				}
				return nil
			}}
			got, err := publishArtifactIdentity(ctx, be, "apps/app/dep.ext4", f)
			if mode == "complete" || mode == "exact length without EOF" {
				if err != nil || got.Bytes != int64(len(payload)) || got.Digest != fmt.Sprintf("sha256:%x", sha256.Sum256(payload)) || !bytes.Equal(published, payload) {
					t.Fatalf("complete output not retained: %+v %v", got, err)
				}
			} else if err == nil || got != (ArtifactIdentity{}) {
				t.Fatalf("incomplete publication returned identity: %+v %v", got, err)
			}
			if mode == "failed write" && !errors.Is(err, failure) {
				t.Fatalf("lost upload error: %v", err)
			}
			if (mode == "cancelled write" || mode == "cancelled exact length") && !errors.Is(err, context.Canceled) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

type artifactReadFailure struct{ failure error }

func (r artifactReadFailure) Read(p []byte) (int, error) {
	copy(p, "partial")
	return len("partial"), r.failure
}

func TestArtifactIdentityRejectsEmptyAndFailedReads(t *testing.T) {
	if value, err := ReadArtifactIdentity(t.Context(), bytes.NewReader(nil)); err == nil || value != (ArtifactIdentity{}) {
		t.Fatalf("empty artifact: %+v %v", value, err)
	}
	failure := errors.New("stored object interrupted")
	if value, err := ReadArtifactIdentity(t.Context(), artifactReadFailure{failure}); !errors.Is(err, failure) || value != (ArtifactIdentity{}) {
		t.Fatalf("interrupted artifact: %+v %v", value, err)
	}
}
