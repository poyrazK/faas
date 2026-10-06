package state

// EnvironmentQualificationSnapshot describes one completed warm capture of the
// original qualification VM. It grants no readiness, restore, or activation
// authority. CaptureID is the host's incoming generation, distinct from its
// physical NativeGeneration. All objects belong to that immutable namespace.
type EnvironmentQualificationSnapshot struct {
	CaptureID         string `json:"capture_id"`
	NativeGeneration  string `json:"native_generation"`
	KernelBootID      string `json:"kernel_boot_id"`
	StorageKey        string `json:"storage_key"`
	VMStateStorageKey string `json:"vmstate_storage_key"`
	DriveStorageKey   string `json:"drive_storage_key"`
	BackingStorageKey string `json:"backing_storage_key"`
	MemBytes          int64  `json:"mem_bytes"`
	VMStateBytes      int64  `json:"vmstate_bytes"`
	StoredBytes       int64  `json:"stored_bytes"`
}
