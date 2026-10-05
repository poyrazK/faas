package api

// ObjectStorageEvent is the data in a gregale.storage CloudEvent. ReceiptID
// identifies the confirmed mutation, rather than the current contents of Key.
// VersionID uses the same owned public selector as object reads and deletions.
type ObjectStorageEvent struct {
	ReceiptID    string `json:"receipt_id"`
	BucketID     string `json:"bucket_id"`
	AppID        string `json:"app_id"`
	Key          string `json:"key"`
	Operation    string `json:"operation"`
	Cause        string `json:"cause"`
	VersionID    string `json:"version_id,omitempty"`
	DeleteMarker bool   `json:"delete_marker,omitempty"`
	ETag         string `json:"etag,omitempty"`
	SizeBytes    *int64 `json:"size_bytes,omitempty"`
}

const (
	ObjectEventSource              = "gregale.storage"
	ObjectEventCreated             = "object.created"
	ObjectEventRemoved             = "object.removed"
	ObjectEventDeleteMarkerCreated = "object.delete_marker.created"
)
