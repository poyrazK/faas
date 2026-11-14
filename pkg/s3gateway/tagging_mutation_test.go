package s3gateway

import (
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestTaggingMutationBudgetDoesNotLeakWriters(t *testing.T) {
	for _, pg := range []bool{false, true} {
		name := "memory"
		if pg {
			name = "postgres"
		}
		t.Run(name, func(t *testing.T) {
			for _, method := range []string{http.MethodPut, http.MethodDelete} {
				t.Run(method, func(t *testing.T) {
					var st multipartCopyIntegrationStore = state.NewMemStore()
					if pg {
						st, _ = multipartCopyPGStore(t)
					}
					calls := 0
					f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						calls++
						if r.Method == http.MethodDelete {
							w.WriteHeader(http.StatusNoContent)
						}
					}), objectstorage.Config{}, 0)
					configureRequestBudget(t, f, 1)
					call := func() error {
						if method == http.MethodPut {
							_, err := f.client.PutObjectTagging(t.Context(), &awss3.PutObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String("key"), Tagging: &types.Tagging{TagSet: []types.Tag{{Key: aws.String("team"), Value: aws.String("storage")}}}})
							return err
						}
						_, err := f.client.DeleteObjectTagging(t.Context(), &awss3.DeleteObjectTaggingInput{Bucket: aws.String("assets"), Key: aws.String("key")})
						return err
					}
					if err := call(); err != nil {
						t.Fatal("last allowed mutation", err)
					}
					for range 2 {
						assertConfigurationBudgetError(t, call())
					}
					usage, err := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
					if err != nil || calls != 1 || state.SummarizeObjectUsage(usage, f.handler.registry.Accounting, time.Now()).RequestCount != 1 {
						t.Fatal("request budget mismatch", calls, usage, err)
					}
					fence, err := st.(state.ObjectBucketWriteFenceStore).AcquireObjectBucketWriteFence(t.Context(), f.bucket, uuid.NewString())
					if err != nil || fence.Requests != 0 {
						t.Fatal("rejected tagging blocked capture", fence, err)
					}
				})
			}
		})
	}
}
