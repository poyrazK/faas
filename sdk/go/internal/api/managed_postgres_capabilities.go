package api

// ManagedPostgresCapabilities describes a region's configured support after
// plan limits. ProvisioningEnabled includes qualification/canary gates; budget,
// quota and usage admission are still checked when a resource is reserved.
type ManagedPostgresCapabilities struct {
	ContractVersion      int      `json:"contract_version"`
	Region               string   `json:"region"`
	ProvisioningEnabled  bool     `json:"provisioning_enabled"`
	DatabaseLimit        int      `json:"database_limit"`
	PostgresMajors       []int    `json:"postgres_majors"`
	ServiceClasses       []string `json:"service_classes"`
	Availability         []string `json:"availability"`
	CredentialAccess     []string `json:"credential_access"`
	ScaleToZero          bool     `json:"scale_to_zero"`
	AlwaysOn             bool     `json:"always_on"`
	PooledConnections    bool     `json:"pooled_connections"`
	PointInTimeRestore   bool     `json:"point_in_time_restore"`
	ClassResize          bool     `json:"class_resize"`
	StorageLimitBytes    int64    `json:"storage_limit_bytes"`
	RestoreWindowSeconds int64    `json:"restore_window_seconds"`
}
