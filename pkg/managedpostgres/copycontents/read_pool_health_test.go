// adr: 585
package copycontents

import (
	"context"
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
)

func TestContentsReadPoolHealthDoesNotFundReadAndRejectsLostOwnership(t *testing.T) {
	ctx := t.Context()
	for _, pool := range []*ReadPool{nil, {}} {
		if !errors.Is(pool.CheckForWorker(ctx), pgerrors.ErrUnavailable) {
			t.Fatal("unowned spool healthy")
		}
	}
	for _, fault := range []string{"none", "canceled", "low_space", "lost_marker", "closed"} {
		t.Run(fault, func(t *testing.T) {
			cfg, p := readPoolFixture(t, 1, 32, 64)
			var want error
			switch fault {
			case "canceled":
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(t.Context())
				cancel()
				want = context.Canceled
			case "low_space":
				p.limits.MinFreeBytes = math.MaxInt64
				want = pgerrors.ErrQuotaExceeded
			case "lost_marker":
				if err := os.Remove(filepath.Join(cfg.SpoolDir, readPoolLockName)); err != nil {
					t.Fatal(err)
				}
				want = pgerrors.ErrConflict
			case "closed":
				if err := p.Close(); err != nil {
					t.Fatal(err)
				}
				want = pgerrors.ErrUnavailable
			}
			if err := p.CheckForWorker(ctx); !errors.Is(err, want) {
				t.Fatalf("health %s: %v, want %v", fault, err, want)
			}
			if p.readers != 0 || p.memory != 0 || p.disk != 0 {
				t.Fatal("health probe funded a read")
			}
		})
		ctx = t.Context()
	}
}
