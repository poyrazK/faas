// adr: 590
package s3gateway

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestMultipartIndependentWriterReceiptsMem(t *testing.T) {
	multipartIndependentWriterReceipts(t, func(t *testing.T) multipartCopyIntegrationStore { return state.NewMemStore() })
}

func TestMultipartIndependentWriterReceiptsPG(t *testing.T) {
	multipartIndependentWriterReceipts(t, func(t *testing.T) multipartCopyIntegrationStore {
		st, _ := multipartCopyPGStore(t)
		return st
	})
}

func multipartIndependentWriterReceipts(t *testing.T, newStore func(*testing.T) multipartCopyIntegrationStore) {
	for _, copyPart := range []bool{false, true} {
		kind := "put"
		if copyPart {
			kind = "copy"
		}
		for _, outcome := range []string{"validated", "missing-etag", "invalid-etag", "duplicate-etag", "async", "lost-reply", "rejected"} {
			if !copyPart && outcome == "rejected" || copyPart && outcome == "duplicate-etag" {
				continue // HTTP status alone is not qualified negative proof.
			}
			t.Run(kind+"/"+outcome, func(t *testing.T) {
				st := newStore(t)
				fences := st.(state.ObjectBucketWriteFenceStore)
				var f *multipartCopyIntegration
				var writes atomic.Int32
				holds := make(chan state.ObjectBucketWriteFence, 1)
				origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/xml")
					switch {
					case r.Method == http.MethodPost && q.Has("uploads"):
						_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>native-part</UploadId></InitiateMultipartUploadResult>`)
					case r.Method == http.MethodHead:
						w.Header().Set("Content-Length", "10")
						w.Header().Set("ETag", `"source"`)
					case r.Method == http.MethodPut && q.Get("uploadId") == "native-part":
						writes.Add(1)
						// The hold races the dispatched writer. Its receipt must
						// already exist before the provider receives any write.
						hold, err := fences.AcquireObjectBucketWriteFence(r.Context(), f.bucket, uuid.NewString())
						if err != nil || hold.Requests != 3 || hold.Multipart != 1 {
							t.Error("provider write escaped custody", hold, err)
							w.WriteHeader(500)
							return
						}
						holds <- hold
						_, _ = io.Copy(io.Discard, r.Body)
						if outcome == "lost-reply" {
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close()
							return
						}
						if outcome == "rejected" {
							w.WriteHeader(412)
							_, _ = io.WriteString(w, `<Error><Code>PreconditionFailed</Code></Error>`)
							return
						}
						etag := `"part"`
						if outcome == "missing-etag" {
							etag = ""
						} else if outcome == "invalid-etag" {
							etag = strings.Repeat("x", api.MaxObjectWriteETagBytes+1)
						}
						if !copyPart {
							w.Header().Set("ETag", etag)
							if outcome == "duplicate-etag" {
								w.Header().Add("ETag", `"other"`)
							}
						}
						if outcome == "async" {
							w.WriteHeader(http.StatusAccepted)
						}
						if copyPart {
							_, _ = io.WriteString(w, `<CopyPartResult><ETag>`+etag+`</ETag></CopyPartResult>`)
						}
					default:
						t.Error("unexpected provider request", r.Method, r.URL.Path)
						w.WriteHeader(500)
					}
				})
				f = newMultipartCopyIntegrationWithProvider(t, st, origin)
				id := f.initiate(t, "destination")
				if _, err := fences.BeginObjectBucketMutation(t.Context(), f.bucket, state.ObjectBucketMutationRequest); err != nil {
					t.Fatal(err)
				}
				var err error
				if copyPart {
					_, err = f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: aws.String("assets/source")})
				} else {
					_, err = f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), ContentLength: aws.Int64(3), Body: bytes.NewReader([]byte("abc"))})
				}
				if (err == nil) != (outcome == "validated") {
					t.Fatal("unexpected acknowledgment", err)
				}
				var hold state.ObjectBucketWriteFence
				select {
				case hold = <-holds:
				default:
					t.Fatal("provider did not observe durable receipt")
				}
				expected := int64(3)
				if outcome == "validated" || outcome == "rejected" {
					expected = 2 // Parent and unrelated writer stay outstanding.
				}
				observed, err := fences.ReadObjectBucketWriteFence(t.Context(), f.bucket, hold.Token)
				if err != nil || observed.Requests != expected || observed.Multipart != 1 || writes.Load() != 1 {
					t.Fatal("independent writer drained without proof or erased another receipt", observed, err, writes.Load())
				}
				// A held retry may not admit another independent provider write.
				_, err = f.client.UploadPart(t.Context(), &awss3.UploadPartInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(2), ContentLength: aws.Int64(3), Body: bytes.NewReader([]byte("abc"))})
				if err == nil || writes.Load() != 1 {
					t.Fatal("held retry dispatched a new part", err, writes.Load())
				}
			})
		}
	}
}
