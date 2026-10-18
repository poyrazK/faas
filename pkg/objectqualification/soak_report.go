//go:build linux

package objectqualification

import "time"

type SoakReport struct {
	Timeline          string             `json:"timeline"`
	Windows           int64              `json:"windows"`
	FullDuration      bool               `json:"full_duration"`
	MetricsWindows    int64              `json:"fresh_metrics_windows"`
	CompactionCycles  int64              `json:"observed_compaction_cycles"`
	CompactionRecords int64              `json:"observed_records_pruned"`
	CompactionBlobs   int64              `json:"observed_blobs_pruned"`
	Interruptions     []SoakInterruption `json:"interruptions"`
}

// ResourceSample is one immutable sampler observation, including validity and
// time. A failed probe never masquerades as zero retained bytes or zero lag.
type ResourceSample struct {
	ObservedAt        time.Time `json:"observed_at"`
	PrimaryGeneration int64     `json:"primary_generation"`
	DiskOK            bool      `json:"disk_ok"`
	MetricsOK         bool      `json:"metrics_ok"`
	Errors            int64     `json:"errors"`
	PrimaryRSS        int64     `json:"primary_rss_bytes"`
	FollowerRSS       int64     `json:"follower_rss_bytes"`
	RunnerRSS         int64     `json:"runner_rss_bytes"`
	DiskBytes         int64     `json:"physical_files_bytes"`
	Records           int64     `json:"journal_records"`
	JournalBytes      int64     `json:"journal_bytes"`
	ReplayBytes       int64     `json:"replay_bytes"`
	BaseSequence      int64     `json:"base_sequence"`
	AppliedSequence   int64     `json:"applied_sequence"`
	SafeSequence      int64     `json:"safe_sequence"`
	PinnedRecords     int64     `json:"pinned_records"`
	CompactionCycles  int64     `json:"compaction_cycles"`
	CompactionRecords int64     `json:"compaction_records_pruned"`
	CompactionBlobs   int64     `json:"compaction_blobs_pruned"`
}

type SoakWindow struct {
	Cycle                   int                       `json:"cycle"`
	ElapsedMS               float64                   `json:"elapsed_ms"`
	DurationMS              float64                   `json:"duration_ms"`
	Workflows               int64                     `json:"workflows_completed"`
	WorkflowsTotal          int64                     `json:"workflows_completed_total"`
	Operations              map[string]OperationStats `json:"operations"`
	RequestsPerSecond       float64                   `json:"requests_per_second"`
	CleanupRecordsPerSecond float64                   `json:"cleanup_records_per_second"`
	CleanupBlobsPerSecond   float64                   `json:"cleanup_blobs_per_second"`
	CleanupRatesValid       bool                      `json:"cleanup_rates_valid"`
	Sample                  *ResourceSample           `json:"sample,omitempty"`
	SampleAgeMS             float64                   `json:"sample_age_ms"`
	Final                   bool                      `json:"final"`
}

type SoakInterruption struct {
	Scenario               string  `json:"scenario"`
	StartedAfterMS         float64 `json:"started_after_ms"`
	DurationMS             float64 `json:"duration_ms"`
	PublishedBefore        int64   `json:"published_before"`
	PublishedDuring        int64   `json:"published_during"`
	CustomerRequestsBefore int64   `json:"customer_requests_before"`
	CustomerRequestsDuring int64   `json:"customer_requests_during"`
	CustomerActiveAtStart  int64   `json:"customer_active_at_start"`
	CustomerActiveDuring   int64   `json:"customer_active_during"`
	LiveTraffic            bool    `json:"live_traffic"`
	WorkflowsBefore        int64   `json:"workflows_completed_before"`
	WorkflowsAfter         int64   `json:"workflows_completed_after"`
	RejoinMS               float64 `json:"rejoin_ms"`
	Recovered              bool    `json:"recovered"`
	Error                  string  `json:"error,omitempty"`
}

func (m *measurements) beginWindows() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.window = make(map[string]*operationSamples)
}

func (m *measurements) takeWindow(cycle int, started time.Time, workflows int64) LoadReport {
	m.mu.Lock()
	ops := m.window
	m.window = make(map[string]*operationSamples)
	m.mu.Unlock()
	window := &measurements{ops: ops}
	return window.finish(cycle, started, workflows, "interval")
}
