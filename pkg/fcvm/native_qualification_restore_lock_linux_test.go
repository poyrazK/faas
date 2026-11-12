//go:build linux

package fcvm

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestNativeQualificationRestoreReleasesProducerLockBeforeStaging(t *testing.T) {
	f, target, owner, _, claimCtx := nativeQualificationRestoreStagingFixture(t)
	ctx, cancel := context.WithTimeout(claimCtx, 3*time.Second)
	defer cancel()
	req := WakeRequest{qualificationRestoreReceipt: qualificationRestoreReceiptControl{token: uuid.NewString(), macKey: strings.Repeat("a", 64)},
		qualificationRestoreReceiptWaiter: &qualificationConfigReceiptWaiter{result: make(chan error, 1)}}
	err := f.capture.v.RestoreNativeQualification(ctx, target.Execution, owner.Lease, req, [2]string{}, "1.7.0")
	if err == nil || !strings.Contains(err.Error(), "stage captured kernel/base") || errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("restore did not reach backing staging with its original producer lock released: %v", err)
	}
}
