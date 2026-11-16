package datapipeline

import (
	"math"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
)

// The WAL cursor stays private. Only headroom and its bounded estimate cross
// the control API; no source row, key or replication connection is exposed.
type walRetentionSample struct {
	api.DataPipelineWALRetention
	SourceLSN  string    `json:"source_lsn"`
	SampledAt  time.Time `json:"sampled_at"`
	Generation int64     `json:"generation"`
}

func (s *walRetentionSample) estimate(previous *walRetentionSample, generated pgtype.Int8) {
	if previous == nil || !generated.Valid || generated.Int64 <= 0 {
		return
	}
	elapsed := s.SampledAt.Sub(previous.SampledAt)
	if elapsed <= 0 || elapsed > api.DataPipelineObservationMaxAge {
		return
	}
	seconds := float64(s.RemainingBytes) * elapsed.Seconds() / float64(generated.Int64)
	if seconds >= math.MaxInt64 {
		return
	}
	estimate := int64(seconds)
	s.EstimatedSecondsRemaining = &estimate
}

func retentionPressure(p api.DataPipeline) bool {
	if p.WALRetention == nil {
		// Older workers do not supply source-specific evidence.
		return p.RetainedWALBytes != nil && *p.RetainedWALBytes >= api.DataPipelineRetentionWarningBytes
	}
	r := p.WALRetention
	return r.LimitBytes > 0 && (r.RemainingBytes <= r.LimitBytes/5 || r.EstimatedSecondsRemaining != nil && *r.EstimatedSecondsRemaining <= int64(api.DataPipelineRetentionWarningWindow/time.Second))
}

func retentionUrgent(p api.DataPipeline) bool {
	r := p.WALRetention
	return r != nil && r.LimitBytes > 0 && (r.RemainingBytes <= r.LimitBytes/10 || r.EstimatedSecondsRemaining != nil && *r.EstimatedSecondsRemaining <= int64(api.DataPipelineRetentionUrgentWindow/time.Second))
}
