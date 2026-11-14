package api

// DataPipelineSetupRequest is immutable intent for the durable setup owner.
type DataPipelineSetupRequest struct {
	Name                   string               `json:"name"`
	SourceConnectionID     string               `json:"source_connection_id"`
	DatabaseID             string               `json:"database_id,omitempty"`
	Region                 string               `json:"region,omitempty"`
	Tables                 []string             `json:"tables"`
	ReadSubjects           []string             `json:"read_subjects,omitempty"`
	Serving                *DataPipelineServing `json:"serving,omitempty"`
	FreshnessTargetSeconds int64                `json:"freshness_target_seconds,omitempty"`
	DataAPIName            string               `json:"data_api_name,omitempty"`
	AuthAppID              string               `json:"auth_app_id,omitempty"`
}

// DataPipelineSetupPlan describes resources, not an invoice or capacity grant.
type DataPipelineSetupPlan struct {
	Name                   string               `json:"name"`
	CreatesMirror          bool                 `json:"creates_mirror"`
	CreatesDataAPI         bool                 `json:"creates_data_api"`
	ExistingSetupID        string               `json:"existing_setup_id,omitempty"`
	DatabaseID             string               `json:"database_id,omitempty"`
	Region                 string               `json:"region,omitempty"`
	StorageLimitBytes      int64                `json:"storage_limit_bytes,omitempty"`
	RestoreWindowSeconds   int64                `json:"restore_window_seconds,omitempty"`
	FreshnessTargetSeconds int64                `json:"freshness_target_seconds,omitempty"`
	ConnectionCheck        *DataConnectionCheck `json:"connection_check,omitempty"`
	BillingNotes           []string             `json:"billing_notes"`
}

// DataPipelineSetup contains customer-visible recovery IDs and no credentials.
type DataPipelineSetup struct {
	ID                string                   `json:"id"`
	Request           DataPipelineSetupRequest `json:"request"`
	DatabaseID        string                   `json:"database_id"`
	PipelineID        string                   `json:"pipeline_id,omitempty"`
	State             string                   `json:"state"`
	Phase             string                   `json:"phase"`
	Reason            string                   `json:"reason,omitempty"`
	Revision          int64                    `json:"revision"`
	DataAPIAppID      string                   `json:"data_api_app_id,omitempty"`
	DeploymentID      string                   `json:"deployment_id,omitempty"`
	Endpoint          string                   `json:"endpoint,omitempty"`
	Region            string                   `json:"region,omitempty"`
	StorageLimitBytes int64                    `json:"storage_limit_bytes,omitempty"`
	Progress          string                   `json:"progress"`
	Recovery          string                   `json:"recovery,omitempty"`
	CreatedAt         string                   `json:"created_at"`
	UpdatedAt         string                   `json:"updated_at"`
}

type DataPipelineSetupList struct {
	Items []DataPipelineSetup `json:"items"`
}

type DataPipelineSetupControlRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	Action           string `json:"action"`
}
