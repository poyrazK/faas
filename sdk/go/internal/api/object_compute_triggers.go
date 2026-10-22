package api

// Wire DTOs mirror pkg/api; server-only reconciliation helpers remain there.
// ObjectStorageComputeTrigger targets only the app owning the bucket.
type ObjectStorageComputeTrigger struct {
	ID     string   `json:"id" yaml:"id"`
	Events []string `json:"events,omitempty" yaml:"events,omitempty"`
	Prefix string   `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Suffix string   `json:"suffix,omitempty" yaml:"suffix,omitempty"`
}

type ReconcileObjectStorageComputeTriggersRequest struct {
	ExpectedRevision *int64                        `json:"expected_revision"`
	Triggers         []ObjectStorageComputeTrigger `json:"triggers"`
}
