package state

import (
	"context"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

// ObjectMultipartInitiation preserves once-only dispatch and a positive reply
// separately from activation. Unknown replies stay outstanding indefinitely.
type ObjectMultipartInitiation struct {
	Receipt          ObjectBucketMutation
	Dispatched       bool
	ProviderUploadID string
	DispatchToken    string
}

type ObjectMultipartInitiationStore interface {
	ReadObjectMultipartInitiation(context.Context, ObjectMultipartUpload) (ObjectMultipartInitiation, error)
	DispatchObjectMultipartInitiation(context.Context, ObjectMultipartUpload) error
	ObserveObjectMultipartInitiation(context.Context, ObjectMultipartUpload, string) error
}

func validMultipartInitiation(u ObjectMultipartUpload) bool {
	return u.State == ObjectMultipartInitiating && u.ProviderUploadID == "" && len(u.LeaseToken) <= 128
}

func validMultipartInitiationResult(id string) bool {
	if strings.TrimSpace(id) == "" || len(id) > api.ObjectProviderUploadIDMaxBytes || !utf8.ValidString(id) {
		return false
	}
	for _, c := range id {
		if c < 32 || c == 127 {
			return false
		}
	}
	return true
}
