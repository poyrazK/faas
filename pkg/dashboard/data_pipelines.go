package dashboard

import "github.com/onebox-faas/faas/pkg/api"

type DataPipelinesData struct {
	Alerts             []DataPipelineAlerts
	Readers            []DataPipelineReaders
	DataAPIEnabled     bool
	AuthApps           []DataPipelineAuthApp
	SetupEnabled       bool
	Setups             []DataPipelineSetupItem
	Enabled            bool
	CSRFToken          string
	ConnectionID       string
	ConnectionRevision int64
	RefreshSeconds     int
	Connections        []api.DataConnection
	Check              *api.DataConnectionCheck
	Pipelines          []api.DataPipeline
	SchemaChanges      []DataPipelineSchemaChange
	TableSelections    []DataPipelineTableSelection
	Verifications      []DataPipelineVerificationItem
	Databases          []ManagedPostgresDatabaseItem
	Message            string
}

type DataPipelineAlerts struct {
	PipelineID, Name, IncidentID, IncidentCode string
	Revision                                   int64
	Enabled                                    bool
	History                                    []DataPipelineAlertDelivery
}

type DataPipelineAlertDelivery struct {
	ID, IncidentID, Kind, Code, Status, Result, OccurredAt string
	Attempts, ResponseCode                                 int
}

type DataPipelineTableSelection struct {
	PipelineID, ConnectionID, Name string
	Revision                       int64
	Tables, Choices                []string
	Removable                      []string
	CanRequest, CanDiscard         bool
}

// DataPipelineSetupItem contains only customer-visible workflow metadata.
type DataPipelineSetupItem struct {
	ID, Name, ConnectionID, DatabaseID, PipelineID    string
	DataAPIName, DataAPIAppID, DeploymentID, Endpoint string
	State, Phase, Progress, Remediation, UpdatedAt    string
	Revision                                          int64
	Tables                                            []string
}

// DataPipelineAuthApp is an owned, configured Auth app choice.
type DataPipelineAuthApp struct{ ID, Name string }

type DataPipelineSchemaChange struct {
	PipelineID, Name, ID, State, Reason                                     string
	Revision                                                                int64
	Supported, CanApprove, CanRetry, TableAddition, TableRemoval            bool
	Changes                                                                 []DataPipelineSchemaColumn
	Refreshes                                                               []DataPipelineSchemaRefresh
	CanRecover, CanApproveRecovery                                          bool
	RecoveryTable, RecoveryID, RecoveryState, RecoveryStage                 string
	RecoveryChoices                                                         []string
	Copies                                                                  []DataPipelineTableCopy
	CancellationTable, CancellationID, CancellationState, CancellationStage string
	CancellationChoices                                                     []string
	CanApproveCancellation, CanDiscardCancellation                          bool
}

type DataPipelineTableCopy struct {
	Table, Phase, UpdatedAt              string
	Rows, Bytes                          int64
	WorkerActive, CountersKnown, Stalled bool
}
type DataPipelineSchemaColumn struct {
	Table, Column, Kind, BeforeType, AfterType, Reason string
	Safe                                               bool
}
type DataPipelineSchemaRefresh struct{ AppID, Slug, WakeID, Status string }

// Verification metadata deliberately excludes range boundaries and row keys.
type DataPipelineVerificationItem struct {
	Restarts                                          int
	PipelineID, ID, Name, State, Phase, Table, Reason string
	StartedAt, UpdatedAt, CompletedAt                 string
	Revision                                          int64
	Rows, Bytes, Ranges, DirtyRanges                  int64
	BackfilledRows                                    int64
	TablesCompleted, TablesTotal                      int
	CanCancel, CanRetry                               bool
	TableAddition                                     bool
	TableRepair                                       bool
	ScopeTables                                       []string
}

// Reader previews contain JWT subjects and table names, never credentials.
type DataPipelineReaders struct {
	PipelineID, Name, ID, State, CurrentInput string
	Revision                                  int64
	Current, Added, Removed, Tables           []string
	CanRequest, CanApprove, CanDiscard        bool
}
