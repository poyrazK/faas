package state

import (
	"context"
	"time"
)

// JobUsageAppender is the job-specific metering seam. Job-task rows have no
// app ownership: production writes app_id=NULL, job_id=<jobs.id>, and
// meter_kind='job'. It is deliberately optional on Store so existing Store
// test doubles and downstream adapters can migrate without changing the
// long-standing app AppendUsage signature.
type JobUsageAppender interface {
	AppendJobUsage(ctx context.Context, accountID, jobID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error
}
