package state

import (
	"context"
	"fmt"
	"time"
)

// AppendJobUsage writes a job-task minute with the widened usage contract
// from migration 00257. Unlike the legacy app path, job rows keep app_id
// NULL and carry their billing identity in job_id + meter_kind. The conflict
// merge mirrors AppendUsage: billing-floor columns are first-write-wins while
// telemetry counters accumulate on redelivery.
func (s *PgStore) AppendJobUsage(ctx context.Context, accountID, jobID, instanceID string, minute time.Time, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes int64, coldBootCount int32, tailSeconds int64) error {
	if jobID == "" {
		return fmt.Errorf("state: job usage requires job_id")
	}
	_, err := s.pool.Exec(ctx,
		`insert into usage_minutes (account_id, app_id, job_id, meter_kind, instance_id, minute, mb_seconds, requests, cpu_usec, tx_bytes, net_tx_bytes, net_rx_bytes, cold_boot_count, tail_seconds)
		 values ($1, NULL, $2, 'job', $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		 on conflict (instance_id, minute) do update
		   set mb_seconds      = case when usage_minutes.mb_seconds = 0 and EXCLUDED.mb_seconds > 0 then EXCLUDED.mb_seconds else usage_minutes.mb_seconds end,
		       cpu_usec        = usage_minutes.cpu_usec        + EXCLUDED.cpu_usec,
		       tx_bytes        = usage_minutes.tx_bytes        + EXCLUDED.tx_bytes,
		       net_tx_bytes    = usage_minutes.net_tx_bytes    + EXCLUDED.net_tx_bytes,
		       net_rx_bytes    = usage_minutes.net_rx_bytes    + EXCLUDED.net_rx_bytes,
		       cold_boot_count = usage_minutes.cold_boot_count + EXCLUDED.cold_boot_count,
		       tail_seconds    = usage_minutes.tail_seconds    + EXCLUDED.tail_seconds`,
		accountID, jobID, instanceID, minute, mbSeconds, requests, cpuUsec, txBytes, netTxBytes, netRxBytes, coldBootCount, tailSeconds)
	return err
}

var _ JobUsageAppender = (*PgStore)(nil)
