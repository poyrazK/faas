package api

import "time"

// ObjectWriteReceipt is a customer projection of a tracked write. Completion
// proves that this attempt committed; it does not assert the current key value.
type ObjectWriteReceipt struct {
	ID          string    `json:"id"`
	BucketID    string    `json:"bucket_id"`
	Key         string    `json:"key"`
	Operation   string    `json:"operation"`
	Bytes       int64     `json:"bytes"`
	ContentType string    `json:"content_type"`
	ETag        string    `json:"etag"`
	Status      string    `json:"status"`
	ErrorCode   string    `json:"error_code,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
}

type ObjectWriteReceiptList struct {
	Items      []ObjectWriteReceipt `json:"items"`
	NextCursor string               `json:"next_cursor,omitempty"`
}

// ParseObjectWriteReceiptPage validates the public listing options. The empty
// status defaults to pending; all includes completed and failed attempts.
func ParseObjectWriteReceiptPage(status string, limit int, cursor string) (string, int, bool) {
	if status == "" {
		status = "pending"
	}
	if limit == 0 {
		limit = ObjectWriteReceiptPageDefault
	}
	valid := status == "pending" || status == "completed" || status == "failed" || status == "all"
	return status, limit, valid && limit > 0 && limit <= ObjectWriteReceiptPageMax && len(cursor) <= ObjectWriteReceiptCursorMaxBytes
}
