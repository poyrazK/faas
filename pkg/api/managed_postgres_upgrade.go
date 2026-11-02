package api

// UpgradeManagedPostgresDatabaseRequest upgrades a database in place to a
// later PostgreSQL major. Nothing else changes with it.
type UpgradeManagedPostgresDatabaseRequest struct {
	RequestID     string `json:"request_id"`
	PostgresMajor int    `json:"postgres_major"`
}

// ManagedPostgresMajorUpgrade exposes only logical identity and observed
// progress. State is pending, succeeded or failed. LastErrorCode is the
// latest retry's error while pending; once failed, why the upgrade failed:
// upgrade_refused (PostgreSQL refused to upgrade the cluster, which stays
// on its major) or upgrade_failed (restore the database to a point before
// the upgrade).
type ManagedPostgresMajorUpgrade struct {
	ID                             string `json:"id"`
	DatabaseID                     string `json:"database_id"`
	FromPostgresMajor              int    `json:"from_postgres_major"`
	TargetPostgresMajor            int    `json:"target_postgres_major"`
	Generation                     int64  `json:"generation"`
	State                          string `json:"state"`
	ConnectionInterruptionExpected bool   `json:"connection_interruption_expected"`
	LastErrorCode                  string `json:"last_error_code,omitempty"`
	CreatedAt                      string `json:"created_at"`
	CompletedAt                    string `json:"completed_at,omitempty"`
}
