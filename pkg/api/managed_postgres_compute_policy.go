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

// SetManagedPostgresSuspendTimeoutRequest sets how long a scale-to-zero
// database stays up after its last activity. Seconds is 60..3600; null
// returns the database to the platform default.
type SetManagedPostgresSuspendTimeoutRequest struct {
	Seconds *int64 `json:"seconds"`
}

// ManagedPostgresWakeWithApp is whether a database starts when an app bound
// to it wakes (ADR-20261125105038).
type ManagedPostgresWakeWithApp struct {
	DatabaseID string `json:"database_id"`
	Enabled    bool   `json:"enabled"`
}

// SetManagedPostgresWakeWithAppRequest turns starting with the app on or off.
type SetManagedPostgresWakeWithAppRequest struct {
	Enabled bool `json:"enabled"`
}

// ManagedPostgresSuspendTimeout is a database's suspend timeout.
type ManagedPostgresSuspendTimeout struct {
	DatabaseID string `json:"database_id"`
	Seconds    int64  `json:"seconds"`
	// Custom is true when the database has its own value.
	Custom bool `json:"custom"`
	// Applied is false while the compute is running: it takes the value
	// the next time it is suspended.
	Applied bool `json:"applied"`
}
