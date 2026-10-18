package api

type ManagedPostgresDatabase struct {
	Health                  *ManagedPostgresHealth `json:"health,omitempty"`
	ID                      string                 `json:"id"`
	Name                    string                 `json:"name"`
	Region                  string                 `json:"region"`
	PostgresMajor           int                    `json:"postgres_major"`
	ServiceClass            string                 `json:"service_class"`
	Availability            string                 `json:"availability"`
	ScaleToZero             bool                   `json:"scale_to_zero"`
	StorageLimitBytes       int64                  `json:"storage_limit_bytes"`
	RestoreWindowSeconds    int64                  `json:"restore_window_seconds"`
	RestoreSourceDatabaseID string                 `json:"restore_source_database_id,omitempty"`
	RestorePointInTime      string                 `json:"restore_point_in_time,omitempty"`
	State                   string                 `json:"state"`
	LastErrorCode           string                 `json:"last_error_code,omitempty"`
	CreatedAt               string                 `json:"created_at"`
	UpdatedAt               string                 `json:"updated_at"`
	DeletedAt               string                 `json:"deleted_at,omitempty"`
}

type ManagedPostgresHealth struct {
	Enabled           bool   `json:"enabled"`
	Status            string `json:"status"`
	Fresh             bool   `json:"fresh"`
	ProviderStatus    string `json:"provider_status"`
	ComputeState      string `json:"compute_state"`
	StaleAfterSeconds int64  `json:"stale_after_seconds"`
	CheckedAt         string `json:"checked_at,omitempty"`
	LastSuccessAt     string `json:"last_success_at,omitempty"`
	LastErrorCode     string `json:"last_error_code,omitempty"`
}

type ManagedPostgresDatabaseList struct {
	Items []ManagedPostgresDatabase `json:"items"`
}

type CreateManagedPostgresDatabaseRequest struct {
	Name                 string `json:"name"`
	Region               string `json:"region"`
	PostgresMajor        int    `json:"postgres_major,omitempty"`
	ServiceClass         string `json:"service_class,omitempty"`
	Availability         string `json:"availability,omitempty"`
	ScaleToZero          *bool  `json:"scale_to_zero,omitempty"`
	StorageLimitBytes    int64  `json:"storage_limit_bytes,omitempty"`
	RestoreWindowSeconds int64  `json:"restore_window_seconds,omitempty"`
}

type RestoreManagedPostgresDatabaseRequest struct {
	Name        string `json:"name"`
	PointInTime string `json:"point_in_time"`
}

type ManagedPostgresBinding struct {
	ID                   string `json:"id"`
	DatabaseID           string `json:"database_id"`
	AppID                string `json:"app_id"`
	Scope                string `json:"scope"`
	EnvironmentKey       string `json:"environment_key"`
	Access               string `json:"access"`
	CredentialGeneration int64  `json:"credential_generation"`
	RotationPending      bool   `json:"rotation_pending"`
	State                string `json:"state"`
	LastErrorCode        string `json:"last_error_code,omitempty"`
	CreatedAt            string `json:"created_at"`
	UpdatedAt            string `json:"updated_at"`
}

type ManagedPostgresBindingList struct {
	Items []ManagedPostgresBinding `json:"items"`
}

type CreateManagedPostgresBindingRequest struct {
	AppID          string `json:"app_id"`
	Scope          string `json:"scope"`
	EnvironmentKey string `json:"environment_key"`
	Access         string `json:"access"`
}
