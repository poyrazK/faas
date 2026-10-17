package objectinventory

import "time"

const CountersKind = "bucket-counters"

// Totals describe currently committed objects and selected parts of active or
// frozen multipart sessions. They exclude reservations and unreachable bytes.
type Totals struct {
	Objects          int64 `json:"objects"`
	CommittedBytes   int64 `json:"committed_bytes"`
	MultipartUploads int64 `json:"multipart_uploads"`
	MultipartParts   int64 `json:"multipart_parts"`
	MultipartBytes   int64 `json:"multipart_bytes"`
}

type Counters struct {
	Version    int       `json:"version"`
	Kind       string    `json:"kind"`
	Bucket     string    `json:"bucket"`
	Generation string    `json:"generation"`
	Snapshot   string    `json:"snapshot"`
	ObservedAt time.Time `json:"observed_at"`
	AuditedAt  time.Time `json:"audited_at"`
	Totals
}

func (c *Counters) UnmarshalJSON(data []byte) error {
	type plain Counters
	return decode(data, (*plain)(c), "version", "kind", "bucket", "generation", "snapshot", "observed_at", "audited_at", "objects", "committed_bytes", "multipart_uploads", "multipart_parts", "multipart_bytes")
}
