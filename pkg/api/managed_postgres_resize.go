package api

// ResizeManagedPostgresDatabaseRequest changes the service class, the
// storage limit or both. An omitted class keeps the class; an omitted or
// unchanged storage limit keeps the limit. Storage only grows. Storage
// growth is a preview: outside FAAS_MANAGED_POSTGRES_STORAGE_RESIZE_ACCOUNTS
// the field is unknown, and it is not in the public spec.
type ResizeManagedPostgresDatabaseRequest struct {
	RequestID         string `json:"request_id"`
	ServiceClass      string `json:"service_class,omitempty"`
	StorageLimitBytes int64  `json:"storage_limit_bytes,omitempty"`
}

// ManagedPostgresResize exposes only logical identity and observed progress.
type ManagedPostgresResize struct {
	ID          string `json:"id"`
	DatabaseID  string `json:"database_id"`
	FromClass   string `json:"from_class"`
	TargetClass string `json:"target_class"`
	// The storage limits are shown only to accounts in the storage preview.
	FromStorageLimitBytes          int64  `json:"from_storage_limit_bytes,omitempty"`
	TargetStorageLimitBytes        int64  `json:"target_storage_limit_bytes,omitempty"`
	Generation                     int64  `json:"generation"`
	State                          string `json:"state"`
	ConnectionInterruptionExpected bool   `json:"connection_interruption_expected"`
	LastErrorCode                  string `json:"last_error_code,omitempty"`
	CreatedAt                      string `json:"created_at"`
	CompletedAt                    string `json:"completed_at,omitempty"`
}
