package state

import (
	"context"
	"encoding/hex"
	"strings"
	"unicode/utf8"

	"github.com/onebox-faas/faas/pkg/api"
)

const (
	ObjectInventoryCurrent     = "current"
	ObjectInventoryAllVersions = "all_versions"
)

type ObjectVersionAccountingStatus struct {
	Scope            string
	VersionsObserved bool
	NativeScanActive bool
}

// The immutable identity hash binds key and native version. Stage rows dedupe
// across pages without retaining an unbounded in-memory set in the worker.
type ObjectVersionInventoryRecord struct {
	Identity string `json:"identity"`
	Bytes    int64  `json:"bytes"`
}

type ObjectVersionInventoryStore interface {
	ObjectVersionAccountingStatus(context.Context, string, string) (ObjectVersionAccountingStatus, error)
	StageObjectVersionInventoryPage(context.Context, string, string, string, []ObjectVersionInventoryRecord) (ObjectCapacityReconciliation, error)
}

func validVersionInventoryPage(cursor string, items []ObjectVersionInventoryRecord) bool {
	if len(cursor) > api.ObjectVersionInventoryCursorMaxBytes || !utf8.ValidString(cursor) || strings.ContainsRune(cursor, 0) || len(items) > api.ObjectVersionInventoryPageSize || len(items) == 0 && cursor != "" {
		return false
	}
	seen := map[string]bool{}
	for _, item := range items {
		decoded, err := hex.DecodeString(item.Identity)
		if err != nil || len(decoded) != 32 || strings.ToLower(item.Identity) != item.Identity || seen[item.Identity] || item.Bytes < 0 || item.Bytes > api.MaxObjectUploadBytes {
			return false
		}
		seen[item.Identity] = true
	}
	return true
}

func versionAdmissionMode(s ObjectUsageSnapshot, bucket string) (all bool) {
	for _, u := range s.Buckets {
		if u.Bucket.ID == bucket {
			return u.InventoryScope == ObjectInventoryAllVersions
		}
	}
	return false
}

func nativeGrantBytes(all bool, size int64) int64 {
	if all {
		return size
	}
	return 0
}
