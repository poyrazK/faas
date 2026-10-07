// adr: 590
package s3gateway

import (
	"context"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/state"
)

type interruptedInitiationStore struct {
	*state.MemStore
	activationFailures atomic.Int32
}

func (s *interruptedInitiationStore) ActivateObjectMultipartUpload(ctx context.Context, id, token, providerID string) error {
	if s.activationFailures.CompareAndSwap(1, 0) {
		return state.ErrConflict
	}
	return s.MemStore.ActivateObjectMultipartUpload(ctx, id, token, providerID)
}

func TestMultipartInitiationSDKRecoveryUnderHold(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(map[bool]string{false: "activation interrupted", true: "native reply lost"}[lost], func(t *testing.T) {
			st := &interruptedInitiationStore{MemStore: state.NewMemStore()}
			st.activationFailures.Store(1)
			var clock atomic.Int64
			clock.Store(time.Now().UnixNano())
			st.SetClockForTest(func() time.Time { return time.Unix(0, clock.Load()) })
			var creates, lists atomic.Int32
			origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				if r.Method != http.MethodPost || !r.URL.Query().Has("uploads") {
					lists.Add(1)
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				creates.Add(1)
				if lost {
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `<Error><Code>SlowDown</Code></Error>`)
					return
				}
				_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>native-original</UploadId></InitiateMultipartUploadResult>`)
			})
			f := newMultipartCopyIntegrationWithProvider(t, st, origin)
			request := &awss3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("original"), ContentType: aws.String("text/plain")}
			if _, err := f.client.CreateMultipartUpload(t.Context(), request); err == nil {
				t.Fatal("initial interruption was hidden")
			}
			// The replacement request must resume the recorded reply or remain
			// uncertain; neither path may list/adopt or create another native MPU.
			clock.Add((state.ObjectMultipartLeaseDuration + time.Second).Nanoseconds())
			if _, err := st.BeginObjectBucketMutation(t.Context(), f.bucket, state.ObjectBucketMutationRequest); err != nil {
				t.Fatal(err)
			}
			hold, err := st.AcquireObjectBucketWriteFence(t.Context(), f.bucket, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			out, err := f.client.CreateMultipartUpload(t.Context(), request)
			if lost {
				assertSDKErrorCode(t, err, "ServiceUnavailable")
			} else if err != nil || aws.ToString(out.UploadId) == "" || aws.ToString(out.UploadId) == "native-original" {
				t.Fatal("positive recovery failed or exposed native identity", out, err)
			}
			if creates.Load() != 1 || lists.Load() != 0 {
				t.Fatal("recovery dispatched native requests", creates.Load(), lists.Load())
			}
			hold, err = st.ReadObjectBucketWriteFence(t.Context(), f.bucket, hold.Token)
			if err != nil || hold.Requests != 2 || hold.Multipart != 1 {
				t.Fatal("recovery retired original or unrelated receipt", hold, err)
			}
		})
	}
}
