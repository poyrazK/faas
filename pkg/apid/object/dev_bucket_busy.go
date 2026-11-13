package object

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// devBusyKeysShown caps how many pending keys a teardown refusal names.
const devBusyKeysShown = 3

// DevBucketBusyProblem explains a developer-session teardown that a pending
// write blocked. Each issued signed upload URL reserves its bucket until it is
// used or expires, and a bucket with a pending write cannot be deleted, so the
// refusal names the pending keys and the time by which every one has expired.
// It returns nil when no pending write explains the conflict, and an error
// when the lookup failed; either way the caller keeps the generic refusal.
// The caller has authorized the account for app.
func (s *Service) DevBucketBusyProblem(ctx context.Context, app state.App) (*api.Problem, error) {
	buckets, ok := s.Host.Store().(state.ObjectBucketStore)
	receipts, receiptsOK := s.Host.Store().(state.ObjectWriteReceiptStore)
	if !ok || !receiptsOK {
		return nil, nil
	}
	list, err := buckets.ListObjectBuckets(ctx, app.AccountID, app.ID)
	if err != nil {
		return nil, fmt.Errorf("list developer session buckets: %w", err)
	}
	for _, bucket := range list {
		page, err := receipts.ListObjectWriteReceipts(ctx, app.AccountID, app.ID, bucket.ID, "pending", api.ObjectWriteReceiptPageMax, "")
		if err != nil {
			return nil, fmt.Errorf("list pending writes for bucket %s: %w", bucket.ID, err)
		}
		if len(page.Items) != 0 {
			return devBucketBusyProblem(app.Slug, bucket, page.Items), nil
		}
	}
	return nil, nil
}

func devBucketBusyProblem(appSlug string, bucket state.ObjectBucket, pending []api.ObjectWriteReceipt) *api.Problem {
	keys := make([]string, 0, devBusyKeysShown)
	var latest time.Time
	for _, w := range pending {
		if len(keys) < devBusyKeysShown {
			keys = append(keys, w.Key)
		}
		if w.CreatedAt.After(latest) {
			latest = w.CreatedAt
		}
	}
	named := strings.Join(keys, ", ")
	if more := len(pending) - len(keys); more > 0 {
		named += fmt.Sprintf(" and %d more", more)
	}
	clears := latest.Add(api.MaxObjectSignedURLTTL).UTC().Format("15:04 MST")
	detail := fmt.Sprintf("Bucket %q has %d pending upload(s): %s. An issued signed upload URL holds the bucket until it is used or expires, at most %d minutes after it was issued, so the developer environment cannot be deleted yet. Retry after %s; `gregale bucket writes list %s %s` shows the pending writes.",
		bucket.Name, len(pending), named, int(api.MaxObjectSignedURLTTL/time.Minute), clears, appSlug, bucket.ID)
	return api.NewProblem(http.StatusConflict, "object_storage_conflict", "Bucket has pending uploads", detail)
}
