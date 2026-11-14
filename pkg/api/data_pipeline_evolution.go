package api

// SchemaReview contains metadata for the worker's exact source-definition
// approval. Row data and provider credentials never enter this contract.
type DataPipelineSchemaReview struct {
	ID              string                           `json:"id"`
	State           string                           `json:"state"`
	Supported       bool                             `json:"supported"`
	Changes         []DataPipelineSchemaColumnChange `json:"changes"`
	RequestedTables []string                         `json:"requested_tables,omitempty"`
}

type DataPipelineSchemaColumnChange struct {
	Table      string `json:"table"`
	Column     string `json:"column"`
	Kind       string `json:"kind"`
	BeforeType string `json:"before_type,omitempty"`
	AfterType  string `json:"after_type,omitempty"`
	Reason     string `json:"reason"`
	Safe       bool   `json:"safe"`
}

type DataPipelineSchemaApprovalRequest struct {
	ExpectedRevision int64  `json:"expected_revision"`
	ChangeID         string `json:"change_id"`
	Retry            bool   `json:"retry,omitempty"`
}

// Auth organizations are mapped by the owner. JWT organization claims never
// grant access. BindingID and DatabaseID are resolved by the control plane.
type DataPipelineAuthOrganizations struct {
	AuthAppID  string                           `json:"auth_app_id"`
	BindingID  string                           `json:"binding_id,omitempty"`
	DatabaseID string                           `json:"database_id,omitempty"`
	Mappings   []DataPipelineOrganizationTenant `json:"mappings"`
}

type DataPipelineOrganizationTenant struct {
	OrganizationID string `json:"organization_id"`
	Tenant         string `json:"tenant"`
}

// A relationship joins selected source columns to the target's complete primary
// key. The worker supplies SQL and enforces tenant equality on both sides.
type DataPipelineRelationship struct {
	Name          string   `json:"name"`
	SourceTable   string   `json:"source_table"`
	TargetTable   string   `json:"target_table"`
	SourceColumns []string `json:"source_columns"`
	TargetColumns []string `json:"target_columns"`
}

type DataPipelineEvolutionRequest struct {
	ExpectedRevision  int64                          `json:"expected_revision"`
	Action            string                         `json:"action"`
	ChangeID          string                         `json:"change_id,omitempty"`
	Kind              string                         `json:"kind,omitempty"`
	Columns           map[string][]string            `json:"columns,omitempty"`
	AuthOrganizations *DataPipelineAuthOrganizations `json:"auth_organizations,omitempty"`
	Relationships     []DataPipelineRelationship     `json:"relationships,omitempty"`
}

type DataPipelineEvolution struct {
	Retry           int                  `json:"retry,omitempty"`
	ID              string               `json:"id"`
	State           string               `json:"state"`
	Kind            string               `json:"kind"`
	Generation      int64                `json:"generation"`
	PreviewRevision int64                `json:"preview_revision"`
	Before          *DataPipelineServing `json:"before,omitempty"`
	After           *DataPipelineServing `json:"after"`
	AddedTables     []string             `json:"added_tables,omitempty"`
}
