package imaged

import (
	"context"
	"errors"
	"fmt"
	"io"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

type sidecarPutBackend struct {
	storage.StorageBackend
	put func(context.Context, string, io.Reader) error
}

func (b sidecarPutBackend) Put(ctx context.Context, key string, r io.Reader) error {
	return b.put(ctx, key, r)
}

// Prod hunt r26: two builds staging the node24 runtime base raced on the
// digest sidecar and OVH answered 409 OperationAborted, failing one app of a
// project deploy. One conflict is written again; a second is reported.
func TestBaseDigestSidecarWritesAgainAfterAConflict(t *testing.T) {
	for _, conflicts := range []int{1, 2} {
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
			if conflicts == 1 {
				if err != nil || attempts != 2 || published != want {
					t.Fatalf("err %v attempts %d published %q", err, attempts, published)
				}
				return
			}
			if !errors.Is(err, storage.ErrWriteConflict) || attempts != 2 {
				t.Fatalf("second conflict: err %v attempts %d", err, attempts)
			}
		})
	}

	denied := errors.New("access denied")
	attempts := 0
	be := sidecarPutBackend{put: func(context.Context, string, io.Reader) error { attempts++; return denied }}
	if err := (&Handler{}).writeBaseDigestSidecar(t.Context(), be, "k", "d", "", ""); !errors.Is(err, denied) || attempts != 1 {
		t.Fatalf("other failure: err %v attempts %d", err, attempts)
	}
}
