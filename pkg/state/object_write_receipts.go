package state

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectWriteReceiptStore provides bucket-authorized read-only diagnostics.
// Callers must enforce the bucket write grant before invoking these methods.
type ObjectWriteReceiptStore interface {
	GetObjectWriteReceipt(context.Context, string, string, string, string) (api.ObjectWriteReceipt, error)
	ListObjectWriteReceipts(context.Context, string, string, string, string, int, string) (api.ObjectWriteReceiptList, error)
}

func ViewObjectWriteReceipt(c ObjectUploadCompletion) api.ObjectWriteReceipt {
	operation := "upload"
	switch c.Origin {
	case "gateway":
		operation = "put"
	case "gateway_copy":
		operation = "copy"
	}
	code := ""
	if validTrackedUploadRetry(c.ErrorCode) || c.Status == "failed" && validTrackedUploadFinish(c) {
		code = c.ErrorCode
	}
	out := api.ObjectWriteReceipt{ID: c.ID, BucketID: c.BucketID, Key: c.Key, Operation: operation, Bytes: c.Bytes, ContentType: c.ContentType, ETag: c.ETag, Status: c.Status, ErrorCode: code, CreatedAt: c.CreatedAt}
	if !c.Encryption.Empty() {
		selection := c.Encryption.Clone().Selection
		out.Encryption = &selection
	}
	return out
}

type objectWriteReceiptCursor struct {
	Bucket  string    `json:"bucket"`
	Status  string    `json:"status"`
	Created time.Time `json:"created"`
	ID      string    `json:"id"`
}

func parseObjectWriteReceiptCursor(bucket, status, cursor string) (objectWriteReceiptCursor, error) {
	var c objectWriteReceiptCursor
	if cursor == "" {
		return c, nil
	}
	if len(cursor) > api.ObjectWriteReceiptCursorMaxBytes {
		return c, ErrConflict
	}
	data, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil || json.Unmarshal(data, &c) != nil || c.Bucket != bucket || c.Status != status || c.Created.IsZero() || c.Created.Year() < 1 || c.Created.Year() > 9999 {
		return c, ErrConflict
	}
	if id, err := uuid.Parse(c.ID); err != nil || id.String() != c.ID {
		return c, ErrConflict
	}
	return c, nil
}

func objectWriteReceiptPage(rows []ObjectUploadCompletion, bucket, status string, limit int) api.ObjectWriteReceiptList {
	page := api.ObjectWriteReceiptList{Items: make([]api.ObjectWriteReceipt, 0, min(limit, len(rows)))}
	for _, c := range rows[:min(limit, len(rows))] {
		page.Items = append(page.Items, ViewObjectWriteReceipt(c))
	}
	if len(rows) > limit {
		c := rows[limit-1]
		data, _ := json.Marshal(objectWriteReceiptCursor{Bucket: bucket, Status: status, Created: c.CreatedAt, ID: c.ID})
		page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
	}
	return page
}

func isTrackedWriteReceipt(c ObjectUploadCompletion) bool {
	return c.WritePhase == ObjectUploadPrepared || c.WritePhase == ObjectUploadDispatched || c.WritePhase == ObjectUploadSettled
}
