package objectstorage

import (
	"context"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// A configured request deadline may exceed the legacy stream timeout. Calls
// without a deadline retain the legacy bound, and no stream exceeds the hard
// service ceiling even when a caller supplied an excessively long context.
func objectStreamTimeout(ctx context.Context) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		return max(time.Nanosecond, min(time.Until(deadline), api.MaxObjectTransferTimeout))
	}
	return api.ObjectTransferTimeout
}
