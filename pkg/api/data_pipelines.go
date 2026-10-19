package api

// DataConnection never includes a connection URL or credential material.
type DataConnection struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Revision  int64  `json:"revision"`
	CreatedAt string `json:"created_at"`
}

type DataConnectionList struct {
	Items []DataConnection `json:"items"`
}
type CreateDataConnectionRequest struct {
	Name          string `json:"name"`
	ConnectionURL string `json:"connection_url"`
}
type RotateDataConnectionRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ConnectionURL    string `json:"connection_url"`
}

// DataPipeline is an eventually reconciled intent. State is an observation;
// desired_state records the requested action and observed_at bounds freshness.
type DataPipeline struct {
	AppliedThroughAt   string                `json:"applied_through_at,omitempty"`
	Health             *DataPipelineHealth   `json:"health,omitempty"`
	Capacity           *DataPipelineCapacity `json:"capacity,omitempty"`
	ObservedRevision   int64                 `json:"observed_revision"`
	ObservationFresh   bool                  `json:"observation_fresh"`
	ID                 string                `json:"id"`
	Name               string                `json:"name"`
	SourceConnectionID string                `json:"source_connection_id"`
	DatabaseID         string                `json:"database_id"`
	Tables             []string              `json:"tables"`
	ReadSubjects       []string              `json:"read_subjects"`
	DesiredState       string                `json:"desired_state"`
	State              string                `json:"state"`
	Revision           int64                 `json:"revision"`
	Generation         int64                 `json:"generation"`
	Reason             string                `json:"reason,omitempty"`
	TablesReady        int                   `json:"tables_ready"`
	RetainedWALBytes   *int64                `json:"retained_wal_bytes,omitempty"`
	ObservedAt         string                `json:"observed_at,omitempty"`
	Verification       string                `json:"verification"`
	VerifiedAt         string                `json:"verified_at,omitempty"`
	CreatedAt          string                `json:"created_at"`
}
type DataPipelineList struct {
	Items []DataPipeline `json:"items"`
}
type CreateDataPipelineRequest struct {
	Name               string   `json:"name"`
	SourceConnectionID string   `json:"source_connection_id"`
	DatabaseID         string   `json:"database_id"`
	Tables             []string `json:"tables"`
	ReadSubjects       []string `json:"read_subjects"`
}
type ControlDataPipelineRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Action           string `json:"action"`
}

// DataPipelineHealth is evaluated when status is read, so expired evidence never
// remains green merely because the worker stopped. It does not estimate time lag.
type DataPipelineHealth struct {
	State                string `json:"state"`
	Code                 string `json:"code"`
	Message              string `json:"message"`
	Recovery             string `json:"recovery"`
	Action               string `json:"action,omitempty"`
	RequiresConfirmation bool   `json:"requires_confirmation"`
}
