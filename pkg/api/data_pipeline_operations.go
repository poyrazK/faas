package api

// DataPipelineOperations configures bounded scheduled work. Snapshot mode is
// chosen at creation and cannot be switched to or from continuous replication.
type DataPipelineOperations struct {
	Version  int                           `json:"version"`
	Snapshot *DataPipelineSnapshotSchedule `json:"snapshot,omitempty"`
	Quality  *DataPipelineQualitySchedule  `json:"quality,omitempty"`
	Workload *DataPipelineWorkload         `json:"workload,omitempty"`
}

type DataPipelineSnapshotSchedule struct {
	EverySeconds int64 `json:"every_seconds"`
	MaxRows      int64 `json:"max_rows,omitempty"`
	MaxBytes     int64 `json:"max_bytes,omitempty"`
}

type DataPipelineWorkload struct {
	Window             *DataPipelineWorkWindow `json:"window,omitempty"`
	QueryTimeoutMillis int64                   `json:"query_timeout_millis,omitempty"`
	BatchesPerTick     int                     `json:"batches_per_tick,omitempty"`
}

// Windows use UTC minutes after midnight; a start after end crosses midnight.
type DataPipelineWorkWindow struct {
	StartMinuteUTC int `json:"start_minute_utc"`
	EndMinuteUTC   int `json:"end_minute_utc"`
}

type DataPipelineQualitySchedule struct {
	EverySeconds int64                     `json:"every_seconds"`
	Rules        []DataPipelineQualityRule `json:"rules"`
}

// Rules are structured; no caller SQL, row values or executable expressions.
// Numeric bounds are decimal strings to preserve exact business values.
type DataPipelineQualityRule struct {
	Name             string   `json:"name"`
	Kind             string   `json:"kind"`
	Table            string   `json:"table"`
	Columns          []string `json:"columns"`
	Minimum          string   `json:"minimum,omitempty"`
	Maximum          string   `json:"maximum,omitempty"`
	ReferenceTable   string   `json:"reference_table,omitempty"`
	ReferenceColumns []string `json:"reference_columns,omitempty"`
}

type DataPipelineOperationsRequest struct {
	ExpectedRevision int64                   `json:"expected_revision"`
	Action           string                  `json:"action"`
	Config           *DataPipelineOperations `json:"config,omitempty"`
}

type DataPipelineOperationsStatus struct {
	Snapshot *DataPipelineSnapshotStatus `json:"snapshot,omitempty"`
	Quality  *DataPipelineQualityStatus  `json:"quality,omitempty"`
}

type DataPipelineSnapshotStatus struct {
	RunID      string `json:"run_id"`
	State      string `json:"state"`
	Reason     string `json:"reason,omitempty"`
	StartedAt  string `json:"started_at,omitempty"`
	NextRunAt  string `json:"next_run_at,omitempty"`
	CapturedAt string `json:"captured_at,omitempty"`
	Rows       int64  `json:"rows"`
	Bytes      int64  `json:"bytes"`
}

type DataPipelineQualityStatus struct {
	State      string                      `json:"state"`
	CheckedAt  string                      `json:"checked_at"`
	NextRunAt  string                      `json:"next_run_at"`
	Generation int64                       `json:"generation"`
	Results    []DataPipelineQualityResult `json:"results"`
}

type DataPipelineQualityResult struct {
	Name              string `json:"name"`
	State             string `json:"state"`
	Violations        int64  `json:"violations"`
	CountIsLowerBound bool   `json:"count_is_lower_bound"`
}
