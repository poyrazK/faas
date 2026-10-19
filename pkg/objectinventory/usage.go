package objectinventory

import "time"

type Issue struct {
	Code     string `json:"code"`
	Path     string `json:"path"`
	Severity string `json:"severity"`
}

// Physical bytes are a filesystem sample: staged streams may grow or disappear
// during the scan. Catalog totals are serialized with publication. Neither is
// a byte-hour integral or a customer invoice.
type Usage struct {
	Version           int       `json:"version"`
	Snapshot          string    `json:"snapshot"`
	StartedAt         time.Time `json:"started_at"`
	FinishedAt        time.Time `json:"finished_at"`
	CatalogHealthy    bool      `json:"catalog_healthy"`
	PhysicalSample    bool      `json:"physical_sample"`
	Buckets           int64     `json:"buckets"`
	Objects           int64     `json:"objects"`
	CommittedBytes    int64     `json:"committed_bytes"`
	MultipartUploads  int64     `json:"multipart_uploads"`
	MultipartParts    int64     `json:"multipart_parts"`
	MultipartBytes    int64     `json:"multipart_bytes"`
	Receipts          int64     `json:"receipts"`
	ReceiptBytes      int64     `json:"receipt_bytes"`
	Sessions          int64     `json:"sessions"`
	Files             int64     `json:"files"`
	FileBytes         int64     `json:"file_bytes"`
	AllocatedBytes    int64     `json:"allocated_bytes"`
	DataBytes         int64     `json:"data_bytes"`
	StagingBytes      int64     `json:"staging_bytes"`
	TrashBytes        int64     `json:"trash_bytes"`
	MetadataBytes     int64     `json:"metadata_bytes"`
	UnreferencedBlobs int64     `json:"unreferenced_blobs"`
	UnreferencedBytes int64     `json:"unreferenced_bytes"`
	DiskBytes         uint64    `json:"disk_bytes"`
	AvailableBytes    uint64    `json:"available_bytes"`
	Errors            int64     `json:"errors"`
	Warnings          int64     `json:"warnings"`
	Issues            []Issue   `json:"issues"`
	IssuesTruncated   bool      `json:"issues_truncated"`
}
