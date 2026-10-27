package fcvm

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/onebox-faas/faas/pkg/storage"
)

// warmDeleteBackend answers Delete with a per-key error and counts calls.
type warmDeleteBackend struct {
	storage.StorageBackend
	errs  map[string]error
	calls []string
}

func (b *warmDeleteBackend) Delete(_ context.Context, key string) error {
	b.calls = append(b.calls, key)
	return b.errs[key]
}

// TestDeleteWarmSnapshotPermanentRefusalIsFinal is issue #2673: a delete the
// backend refuses for good must not come back as an error, or builderd
// re-queues the snapshot and retries it on every sweep forever.
func TestDeleteWarmSnapshotPermanentRefusalIsFinal(t *testing.T) {
	transient := errors.New("registry 503")
	for name, tc := range map[string]struct {
		errs    map[string]error
		wantErr error
	}{
		"all deleted": {},
		"quarantined by github packages": {errs: map[string]error{
			"snap/builder/mem": fmt.Errorf("%w: github packages refused version deletion", storage.ErrDeleteQuarantined),
		}},
		"backend cannot delete": {errs: map[string]error{
			"snap/builder/vmstate": fmt.Errorf("%w: delete returned 405", storage.ErrDeleteUnsupported),
		}},
		"transient failure still retries": {
			errs:    map[string]error{"snap/builder/mem": transient},
			wantErr: transient,
		},
	} {
		t.Run(name, func(t *testing.T) {
			backend := &warmDeleteBackend{errs: tc.errs}
			v := &JailerVMM{storage: backend}
			err := v.DeleteWarmSnapshot(context.Background(), "snap/builder/mem", "snap/builder/vmstate")
			switch {
			case tc.wantErr == nil && err != nil:
				t.Fatalf("DeleteWarmSnapshot = %v, want nil", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Fatalf("DeleteWarmSnapshot = %v, want %v", err, tc.wantErr)
			}
			if len(backend.calls) < 2 || backend.calls[0] != "snap/builder/mem" || backend.calls[1] != "snap/builder/vmstate" {
				t.Fatalf("deleted %v, want mem and vmstate attempted", backend.calls)
			}
		})
	}
}
