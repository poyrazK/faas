package api

type ChangeManagedPostgresComputePolicyRequest struct {
	RequestID   string `json:"request_id"`
	ScaleToZero *bool  `json:"scale_to_zero"`
}

type ManagedPostgresComputePolicyChange struct {
	ID                             string `json:"id"`
	DatabaseID                     string `json:"database_id"`
	FromScaleToZero                bool   `json:"from_scale_to_zero"`
	TargetScaleToZero              bool   `json:"target_scale_to_zero"`
	Generation                     int64  `json:"generation"`
	State                          string `json:"state"`
	ConnectionInterruptionExpected bool   `json:"connection_interruption_expected"`
	LastErrorCode                  string `json:"last_error_code,omitempty"`
	CreatedAt                      string `json:"created_at"`
	CompletedAt                    string `json:"completed_at,omitempty"`
}
