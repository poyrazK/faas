package state

import (
	"context"
	"fmt"
	"time"
)

// AppendJobUsage mirrors PgStore.AppendJobUsage in the in-memory adapter.
// AppID retains the job ID as the aggregate compatibility key used by the
// existing UsageByMonth/UsageDaily read shapes; MeterKind and JobID preserve
// the widened row identity for callers that inspect the minute ledger.
func (m *MemStore) AppendJobUsage(ctx context.Context, accountID, jobID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error {
	if jobID == "" {
		return fmt.Errorf("state: job usage requires job_id")
	}
	return m.appendUsage(ctx, accountID, jobID, instanceID, minute, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes, coldBootCount, tailSeconds, "job", jobID)
}

var _ JobUsageAppender = (*MemStore)(nil)
var _ JobQuotaCreator = (*MemStore)(nil)
