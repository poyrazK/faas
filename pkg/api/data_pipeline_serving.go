package api

// DataPipelineServing is an explicit, versioned serving contract. Columns is
// immutable after creation; membership changes use the reader approval flow.
type DataPipelineServing struct {
	Version      int                       `json:"version"`
	Columns      map[string][]string       `json:"columns,omitempty"`
	TenantAccess *DataPipelineTenantAccess `json:"tenant_access,omitempty"`
}

type DataPipelineTenantAccess struct {
	Column      string                         `json:"column"`
	Memberships []DataPipelineTenantMembership `json:"memberships"`
}

// Memberships are account-owner intent, never caller-supplied JWT tenant claims.
type DataPipelineTenantMembership struct {
	Subject string `json:"subject"`
	Tenant  string `json:"tenant"`
}

type DataPipelineTenantChange struct {
	ID              string                         `json:"id"`
	State           string                         `json:"state"`
	PreviewRevision int64                          `json:"preview_revision"`
	Before          []DataPipelineTenantMembership `json:"before"`
	After           []DataPipelineTenantMembership `json:"after"`
}

type DataPipelineTenantReadersRequest struct {
	ExpectedRevision int64                          `json:"expected_revision"`
	Action           string                         `json:"action"`
	ChangeID         string                         `json:"change_id,omitempty"`
	Memberships      []DataPipelineTenantMembership `json:"memberships,omitempty"`
}
