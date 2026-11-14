package api

type DataPipelineIndexOrder struct {
	Column     string `json:"column"`
	Descending bool   `json:"descending,omitempty"`
}

// DataPipelineIndexRequest accepts query shape, never SQL or filter values.
type DataPipelineIndexRequest struct {
	ExpectedRevision int64                    `json:"expected_revision"`
	Action           string                   `json:"action"`
	AdviceID         string                   `json:"advice_id,omitempty"`
	Table            string                   `json:"table,omitempty"`
	Filters          []string                 `json:"filters,omitempty"`
	OrderBy          []DataPipelineIndexOrder `json:"order_by,omitempty"`
	MaxIndexBytes    int64                    `json:"max_index_bytes,omitempty"`
}

type DataPipelineIndexKey struct {
	Column           string `json:"column"`
	Descending       bool   `json:"descending,omitempty"`
	TenantExpression bool   `json:"tenant_expression,omitempty"`
}

// DataPipelineIndexAdvice is a frozen owner-approved recommendation.
type DataPipelineIndexAdvice struct {
	ID                  string                 `json:"id"`
	State               string                 `json:"state"`
	PreviewRevision     int64                  `json:"preview_revision"`
	Table               string                 `json:"table"`
	Keys                []DataPipelineIndexKey `json:"keys"`
	IndexName           string                 `json:"index_name,omitempty"`
	Statement           string                 `json:"statement,omitempty"`
	TableBytes          int64                  `json:"table_bytes,omitempty"`
	EstimatedIndexBytes *int64                 `json:"estimated_index_bytes,omitempty"`
	StorageBudgetBytes  int64                  `json:"storage_budget_bytes,omitempty"`
	AppliedBytes        *int64                 `json:"applied_bytes,omitempty"`
	CanApply            bool                   `json:"can_apply"`
	Reason              string                 `json:"reason,omitempty"`
	Message             string                 `json:"message"`
}
