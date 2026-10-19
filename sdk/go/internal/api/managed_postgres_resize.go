package api

type ResizeManagedPostgresDatabaseRequest struct {
	RequestID    string `json:"request_id"`
	ServiceClass string `json:"service_class"`
}

// ManagedPostgresResize exposes only logical identity and observed progress.
type ManagedPostgresResize struct {
	ID                             string `json:"id"`
	DatabaseID                     string `json:"database_id"`
	FromClass                      string `json:"from_class"`
	TargetClass                    string `json:"target_class"`
	Generation                     int64  `json:"generation"`
	State                          string `json:"state"`
	ConnectionInterruptionExpected bool   `json:"connection_interruption_expected"`
	LastErrorCode                  string `json:"last_error_code,omitempty"`
	CreatedAt                      string `json:"created_at"`
	CompletedAt                    string `json:"completed_at,omitempty"`
}
