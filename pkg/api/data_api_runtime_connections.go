package api

import "time"

// DataAPIRuntimeConnectionReport is the private workload-only reporting contract.
type DataAPIRuntimeConnectionReport struct {
	Version              int       `json:"version"`
	BindingID            string    `json:"binding_id"`
	DatabaseIdentity     string    `json:"database_identity"`
	DatabaseGeneration   int64     `json:"database_generation"`
	CredentialGeneration int64     `json:"credential_generation"`
	Role                 string    `json:"role"`
	State                string    `json:"state"`
	RuntimeModel         string    `json:"runtime_model"`
	EngineConnections    int32     `json:"engine_connections"`
	RefreshConnections   int32     `json:"refresh_connections"`
	ObservedAt           time.Time `json:"observed_at"`
}
