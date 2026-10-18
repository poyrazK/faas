package objectstorage

import (
	"context"
	"encoding/json"
	"net/url"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectinventory"
)

func (p *S3) GetOwnedUsageCounters(ctx context.Context, bucket string) (objectinventory.Counters, error) {
	if !p.ownedNodeReceipts {
		return objectinventory.Counters{}, ErrConfiguration
	}
	if !objectinventory.ValidBucket(bucket) {
		return objectinventory.Counters{}, ErrInvalid
	}
	body, err := p.ownedInventoryGET(ctx, bucket, url.Values{objectinventory.Query: {objectinventory.CountersKind}})
	if err != nil {
		return objectinventory.Counters{}, err
	}
	var result objectinventory.Counters
	if json.Unmarshal(body, &result) != nil || !validOwnedUsageCounters(result, bucket, time.Now().UTC()) {
		return objectinventory.Counters{}, ErrUnavailable
	}
	return result, nil
}

func validOwnedUsageCounters(c objectinventory.Counters, bucket string, now time.Time) bool {
	maxAge := time.Duration(api.ObjectStorageInventoryMaxAgeSeconds) * time.Second
	if c.Version != objectinventory.Version || c.Kind != objectinventory.CountersKind || c.Bucket != bucket || !validInventoryUUID(c.Generation) || !validInventorySnapshot(c.Snapshot) || c.ObservedAt.IsZero() || c.ObservedAt.After(now) || now.Sub(c.ObservedAt) > maxAge || c.AuditedAt.IsZero() || c.AuditedAt.After(c.ObservedAt) || now.Sub(c.AuditedAt) > maxAge {
		return false
	}
	for _, value := range []int64{c.Objects, c.CommittedBytes, c.MultipartUploads, c.MultipartParts, c.MultipartBytes} {
		if value < 0 || value > api.MaxObjectStoragePolicyValue {
			return false
		}
	}
	return (c.Objects != 0 || c.CommittedBytes == 0) && (c.MultipartUploads != 0 || c.MultipartParts == 0 && c.MultipartBytes == 0) && (c.MultipartParts != 0 || c.MultipartBytes == 0)
}

// Only explicitly opted-in ordinary accounting uses counters. Fenced capacity
// recovery continues to enumerate and validate authoritative source records.
func CompleteOwnedUsageCounters(ctx context.Context, provider Provider, bucket string, record func(context.Context) error) (bytes, keys int64, owned bool, err error) {
	p, ok := provider.(*S3)
	if !ok || !p.ownedNodeUsageCounters {
		return 0, 0, false, nil
	}
	if !p.ownedNodeReceipts {
		return 0, 0, true, ErrConfiguration
	}
	if record != nil {
		if err := record(ctx); err != nil {
			return 0, 0, true, err
		}
	}
	counters, err := p.GetOwnedUsageCounters(ctx, bucket)
	if err != nil {
		return 0, 0, true, err
	}
	return counters.CommittedBytes, counters.Objects, true, nil
}
