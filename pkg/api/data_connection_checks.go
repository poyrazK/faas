package api

// DataConnectionCheck contains catalog metadata only. Checks run in the private
// worker, using the same connection policy and inspectors as replication.
type DataConnectionCheck struct {
	ID                 string                `json:"id"`
	ConnectionID       string                `json:"connection_id"`
	ConnectionRevision int64                 `json:"connection_revision"`
	DatabaseID         string                `json:"database_id,omitempty"`
	State              string                `json:"state"`
	Fresh              bool                  `json:"fresh"`
	Truncated          bool                  `json:"truncated"`
	Checks             []DataConnectionIssue `json:"checks"`
	Tables             []DataSourceTable     `json:"tables"`
	CreatedAt          string                `json:"created_at"`
	ObservedAt         string                `json:"observed_at,omitempty"`
	ExpiresAt          string                `json:"expires_at,omitempty"`
	Capacity           *DataPipelineCapacity `json:"capacity,omitempty"`
}

// Capacity state is ready, insufficient_capacity, or needs_qualification.
// Only fresh private observations can make a new allocation ready.
type DataPipelineCapacity struct {
	State       string                `json:"state"`
	Source      *DataEndpointCapacity `json:"source,omitempty"`
	Destination *DataEndpointCapacity `json:"destination,omitempty"`
}

type DataEndpointCapacity struct {
	State               string `json:"state"`
	UsableConnections   int64  `json:"usable_connections"`
	ReservedConnections int64  `json:"reserved_connections"`
	RequiredConnections int64  `json:"required_connections"`
	LimitingResource    string `json:"limiting_resource,omitempty"`
	ObservedAt          string `json:"observed_at,omitempty"`
	ExpiresAt           string `json:"expires_at,omitempty"`
}

type RequestDataConnectionCheck struct {
	ExpectedRevision int64  `json:"expected_revision"`
	DatabaseID       string `json:"database_id,omitempty"`
}

type DataConnectionIssue struct {
	Code        string `json:"code"`
	State       string `json:"state"`
	Message     string `json:"message"`
	Remediation string `json:"remediation,omitempty"`
	Column      string `json:"column,omitempty"`
}

type DataSourceTable struct {
	Name      string                `json:"name"`
	Supported bool                  `json:"supported"`
	Columns   []DataSourceColumn    `json:"columns"`
	Issues    []DataConnectionIssue `json:"issues"`
}

type DataSourceColumn struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Nullable   bool   `json:"nullable"`
	PrimaryKey bool   `json:"primary_key"`
}
