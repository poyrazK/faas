// Package objectqualification exercises disposable local or SSH object-node processes
// through the customer gateway. Its report is development evidence, not a
// provider release gate or a claim about independent-host durability.
package objectqualification

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"sort"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

type Report struct {
	Version       int              `json:"version"`
	Passed        bool             `json:"passed"`
	StartedAt     time.Time        `json:"started_at"`
	FinishedAt    time.Time        `json:"finished_at"`
	Scope         string           `json:"scope"`
	Config        Configuration    `json:"config"`
	DaemonSHA256  string           `json:"daemon_sha256"`
	ControlSHA256 string           `json:"control_sha256"`
	GoVersion     string           `json:"go_version"`
	Platform      string           `json:"platform"`
	Runs          []ModeReport     `json:"runs"`
	Error         string           `json:"error,omitempty"`
	Preflight     *PreflightReport `json:"preflight,omitempty"`
}

type Configuration struct {
	Duration                  string `json:"duration_per_cycle"`
	Workers                   int    `json:"workers"`
	Cycles                    int    `json:"cycles"`
	MaxWorkflows              int    `json:"max_workflows_per_cycle"`
	ObjectBytes               int64  `json:"object_bytes"`
	Seed                      int64  `json:"seed"`
	Durability                string `json:"durability"`
	WorkBytesPerSecond        int64  `json:"replication_work_bytes_per_second"`
	WorkEntriesPerSecond      int    `json:"replication_work_entries_per_second"`
	ReplicationDelay          string `json:"replication_delay_per_rpc"`
	ReplicationBytesPerSecond int64  `json:"replication_payload_bytes_per_second"`
	AutomaticCompaction       bool   `json:"automatic_compaction"`
	CompactionRecords         int64  `json:"compaction_records"`
	DiskBudgetBytes           int64  `json:"sampled_disk_budget_bytes"`
	Soak                      bool   `json:"soak"`
	SoakInterval              string `json:"soak_interval"`
	SoakFaults                bool   `json:"soak_faults"`
	SoakFaultDuration         string `json:"soak_fault_duration"`
	SoakWorkflowsPerSecond    int    `json:"soak_workflows_per_second"`
	RequireDedicatedStorage   bool   `json:"require_dedicated_storage,omitempty"`
	RecoveryRetention         bool   `json:"recovery_retention,omitempty"`
}

type Topology struct {
	ControllerKernelBootSHA256     string        `json:"controller_kernel_boot_sha256"`
	Follower                       AgentIdentity `json:"initial_follower"`
	DistinctKernelBoots            bool          `json:"distinct_kernel_boots"`
	PhysicalFailureDomainsVerified bool          `json:"physical_failure_domains_verified"`
	Transport                      string        `json:"transport"`
}

type ModeReport struct {
	Mode              string                     `json:"mode"`
	Passed            bool                       `json:"passed"`
	Checks            []Check                    `json:"checks"`
	Loads             []LoadReport               `json:"loads"`
	Resources         Resources                  `json:"resources"`
	Error             string                     `json:"error,omitempty"`
	Topology          *Topology                  `json:"topology,omitempty"`
	Recoveries        []RecoveryObservation      `json:"recoveries,omitempty"`
	RecoveryRetention *RecoveryRetentionEvidence `json:"recovery_retention,omitempty"`
}

type RecoveryObservation struct {
	Scenario string  `json:"scenario"`
	RejoinMS float64 `json:"rejoin_ms"`
	CaughtUp bool    `json:"caught_up"`
}

type Check struct {
	Name       string  `json:"name"`
	Passed     bool    `json:"passed"`
	DurationMS float64 `json:"duration_ms"`
	Error      string  `json:"error,omitempty"`
}

type LoadReport struct {
	Cycle                 int                       `json:"cycle"`
	DurationMS            float64                   `json:"duration_ms"`
	Workflows             int64                     `json:"workflows"`
	StopReason            string                    `json:"stop_reason"`
	Operations            map[string]OperationStats `json:"operations"`
	RequestsPerSecond     float64                   `json:"requests_per_second"`
	PayloadBytesPerSecond float64                   `json:"payload_bytes_per_second"`
	WorkflowsStarted      int64                     `json:"workflows_started"`
	Soak                  *SoakReport               `json:"soak,omitempty"`
}

type OperationStats struct {
	Attempts           int64   `json:"attempts"`
	Successes          int64   `json:"successes"`
	ExpectedRejections int64   `json:"expected_rejections"`
	Failures           int64   `json:"failures"`
	PayloadBytes       int64   `json:"payload_bytes"`
	P50MS              float64 `json:"p50_ms"`
	P95MS              float64 `json:"p95_ms"`
	P99MS              float64 `json:"p99_ms"`
	MaxMS              float64 `json:"max_ms"`
}

type Resources struct {
	Samples               int64 `json:"samples"`
	SampleErrors          int64 `json:"sample_errors"`
	MetricsSamples        int64 `json:"metrics_samples"`
	DiskSamples           int64 `json:"disk_samples"`
	PrimaryRSSPeak        int64 `json:"primary_rss_peak_bytes"`
	FollowerRSSPeak       int64 `json:"follower_rss_peak_bytes"`
	RunnerRSSPeak         int64 `json:"runner_rss_peak_bytes"`
	DiskPeak              int64 `json:"physical_files_peak_bytes"`
	JournalBytesPeak      int64 `json:"journal_bytes_peak"`
	ReplayBytesPeak       int64 `json:"replay_bytes_peak"`
	ReplicationLagPeak    int64 `json:"replication_lag_records_peak"`
	RemoteSamples         int64 `json:"remote_samples,omitempty"`
	CompactionRecordsPeak int64 `json:"compaction_records_pruned_peak"`
	CompactionBlobsPeak   int64 `json:"compaction_blobs_pruned_peak"`
}

type operationSamples struct {
	stats   OperationStats
	latency []float64
}

type measurements struct {
	mu     sync.Mutex
	ops    map[string]*operationSamples
	window map[string]*operationSamples
}

func (m *measurements) record(name string, start time.Time, bytes int64, rejected bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.ops == nil {
		m.ops = make(map[string]*operationSamples)
	}
	latency := float64(time.Since(start)) / float64(time.Millisecond)
	recordOperation(m.ops, name, latency, bytes, rejected, err)
	if m.window != nil {
		recordOperation(m.window, name, latency, bytes, rejected, err)
	}
}

func recordOperation(ops map[string]*operationSamples, name string, latency float64, bytes int64, rejected bool, err error) {
	s := ops[name]
	if s == nil {
		s = &operationSamples{}
		ops[name] = s
	}
	s.stats.Attempts++
	s.latency = append(s.latency, latency)
	if err != nil {
		s.stats.Failures++
		return
	}
	if rejected {
		s.stats.ExpectedRejections++
	} else {
		s.stats.Successes++
		s.stats.PayloadBytes += bytes
	}
}

func (m *measurements) finish(cycle int, start time.Time, count int64, reason string) LoadReport {
	m.mu.Lock()
	defer m.mu.Unlock()
	duration := time.Since(start)
	r := LoadReport{Cycle: cycle, DurationMS: float64(duration) / float64(time.Millisecond), Workflows: count, StopReason: reason, Operations: make(map[string]OperationStats)}
	var attempts, bytes int64
	for name, sample := range m.ops {
		sort.Float64s(sample.latency)
		s := sample.stats
		s.P50MS, s.P95MS, s.P99MS = percentile(sample.latency, .50), percentile(sample.latency, .95), percentile(sample.latency, .99)
		s.MaxMS = percentile(sample.latency, 1)
		r.Operations[name] = s
		attempts += s.Attempts
		bytes += s.PayloadBytes
	}
	if duration > 0 {
		r.RequestsPerSecond = float64(attempts) / duration.Seconds()
		r.PayloadBytesPerSecond = float64(bytes) / duration.Seconds()
	}
	return r
}

func percentile(values []float64, fraction float64) float64 {
	if len(values) == 0 {
		return 0
	}
	index := max(0, int(math.Ceil(float64(len(values))*fraction))-1)
	return values[min(index, len(values)-1)]
}

// WriteReport refuses replacement and never serializes credentials or headers.
func WriteReport(path string, report Report) error {
	return writeQualificationEvidence(path, report)
}

func writeQualificationEvidence(path string, report any) error {
	body, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if len(body) > api.ObjectQualificationReportBytes {
		return errors.New("qualification report exceeds its size limit")
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	if _, err = f.Write(append(body, '\n')); err != nil {
		return err
	}
	return f.Sync()
}
