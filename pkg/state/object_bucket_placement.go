package state

import (
	"context"
	"errors"
)

// ErrObjectStorageRegionFull reports that no backend in a region's placement
// pool has room for a new bucket (ADR-20261029174627).
var ErrObjectStorageRegionFull = errors.New("object storage region has no backend with room for a new bucket")

// ObjectBucketPlacement is one backend that may receive a new bucket, in the
// operator's fill order. MaxBuckets 0 is no ceiling.
type ObjectBucketPlacement struct {
	BackendID          string
	BackendFingerprint string
	MaxBuckets         int64
}

// ObjectBucketPlacementCount is a backend's live buckets: all of them and
// those of one account.
type ObjectBucketPlacementCount struct {
	Buckets        int64
	AccountBuckets int64
}

// ChooseObjectBucketPlacement keeps an account's buckets together, because a
// cross-bucket copy needs one backend: among the candidates with room, the
// one holding most of the account's buckets wins, and ties (including an
// account without buckets) go to the first in fill order. It reports false
// when no candidate has room.
func ChooseObjectBucketPlacement(candidates []ObjectBucketPlacement, counts map[string]ObjectBucketPlacementCount) (ObjectBucketPlacement, bool) {
	var best ObjectBucketPlacement
	var bestAccount int64
	found := false
	for _, c := range candidates {
		n := counts[c.BackendID]
		if c.MaxBuckets > 0 && n.Buckets >= c.MaxBuckets {
			continue
		}
		if !found || n.AccountBuckets > bestAccount {
			best, bestAccount, found = c, n.AccountBuckets, true
		}
	}
	return best, found
}

// ObjectBucketPlacementStore places new buckets across a region's backends
// (ADR-20261029174627).
type ObjectBucketPlacementStore interface {
	// ReserveObjectBucketPlaced is ReserveObjectBucketWithResult on the
	// backend ChooseObjectBucketPlacement picks among candidates. The counts
	// are taken under one lock per region, so concurrent creations never pass
	// a ceiling. b's backend fields are ignored; an existing bucket with b's
	// name is returned unchanged. ErrObjectStorageRegionFull when none of
	// candidates has room.
	ReserveObjectBucketPlaced(ctx context.Context, b ObjectBucket, appLimit int, candidates []ObjectBucketPlacement) (ObjectBucket, bool, error)
	// ObjectBucketPlacementCounts counts the live buckets on backendIDs, and
	// accountID's among them ("" counts none).
	ObjectBucketPlacementCounts(ctx context.Context, accountID string, backendIDs []string) (map[string]ObjectBucketPlacementCount, error)
}
