package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/storage"
)

type sidecarPutBackend struct {
	storage.StorageBackend
	put func(context.Context, string, io.Reader) error
}

func (b sidecarPutBackend) Put(ctx context.Context, key string, r io.Reader) error {
	return b.put(ctx, key, r)
}

// Prod hunts r26/r27: builds staging one runtime base raced on its digest and
// scan sidecars and OVH answered 409 OperationAborted, failing one app of a
// project deploy. A conflict is written again (at most twice); a third
// conflict and any other failure are reported.
func TestSharedBaseSidecarWritesAgainAfterAConflict(t *testing.T) {
	saved := sharedBaseSidecarConflictBackoff
	sharedBaseSidecarConflictBackoff = []time.Duration{0, 0}
	t.Cleanup(func() { sharedBaseSidecarConflictBackoff = saved })

	for _, conflicts := range []int{1, 2, 3} {
		t.Run(fmt.Sprintf("conflicts=%d", conflicts), func(t *testing.T) {
			attempts := 0
			var published string
			be := sidecarPutBackend{put: func(_ context.Context, _ string, r io.Reader) error {
				attempts++
				body, err := io.ReadAll(r)
				if err != nil {
					return err
				}
				if attempts <= conflicts {
					return fmt.Errorf("storage: s3 put: %w", storage.ErrWriteConflict)
				}
				published = string(body)
				return nil
			}}
			err := (&Handler{}).writeBaseDigestSidecar(t.Context(), be, "base/node24.ext4.digest", "sha256:cfg", "sha256:init", "")
			want := baseDigestSidecarValueWithSource("sha256:cfg", "sha256:init", "")
			if conflicts < 3 {
				if err != nil || attempts != conflicts+1 || published != want {
					t.Fatalf("err %v attempts %d published %q", err, attempts, published)
				}
				return
			}
			if !errors.Is(err, storage.ErrWriteConflict) || attempts != 3 {
				t.Fatalf("third conflict: err %v attempts %d", err, attempts)
			}
		})
	}

	denied := errors.New("access denied")
	attempts := 0
	be := sidecarPutBackend{put: func(context.Context, string, io.Reader) error { attempts++; return denied }}
	if err := putSharedBaseSidecar(t.Context(), be, "scans/x.scan.json", []byte("{}")); !errors.Is(err, denied) || attempts != 1 {
		t.Fatalf("other failure: err %v attempts %d", err, attempts)
	}

	sharedBaseSidecarConflictBackoff = []time.Duration{time.Hour}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	conflict := sidecarPutBackend{put: func(context.Context, string, io.Reader) error { return storage.ErrWriteConflict }}
	if err := putSharedBaseSidecar(ctx, conflict, "k", nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled wait: err %v", err)
	}
}
