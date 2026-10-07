// adr: 590
package s3gateway

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type observingPartCopyJournal struct {
	state.ObjectMultipartUploadStore
	state.ObjectMultipartTransferStore
	state.ObjectMultipartPartMutationStore
	state.ObjectMultipartPartCopyMutationStore
	dispatched chan state.ObjectBucketMutation
}

func (s *observingPartCopyJournal) DispatchObjectMultipartPartCopyMutation(ctx context.Context, b state.ObjectBucket, id string, part int32, token string, i state.ObjectMultipartPartCopyIntent) (state.ObjectBucketMutation, error) {
	r, err := s.ObjectMultipartPartCopyMutationStore.DispatchObjectMultipartPartCopyMutation(ctx, b, id, part, token, i)
	if err == nil {
		s.dispatched <- r
	}
	return r, err
}

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
				copyReceipts := make(chan state.ObjectBucketMutation, 1)
				origin := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/xml")
					switch {
					case r.Method == http.MethodPost && q.Has("uploads"):
						_, _ = io.WriteString(w, `<InitiateMultipartUploadResult><UploadId>native-part</UploadId></InitiateMultipartUploadResult>`)
					case r.Method == http.MethodHead:
						w.Header().Set("Content-Length", strconv.FormatInt(api.MinMultipartPartBytes+10, 10))
						w.Header().Set("ETag", `"source"`)
					case r.Method == http.MethodPut && q.Get("uploadId") == "native-part":
						writes.Add(1)
						if copyPart {
							select {
							case receipt := <-copyReceipts:
								intent, err := st.(state.ObjectMultipartPartCopyMutationStore).ReadObjectMultipartPartCopyIntent(r.Context(), receipt)
								if err != nil || intent.SourceKey != "source" || intent.SourceETag != `"source"` || intent.SourcePhysicalName != f.bucket.PhysicalName || intent.SourceSize != api.MinMultipartPartBytes+10 || intent.ExpectedSize != 4 || !intent.HasRange || intent.RangeFirst != 1 || intent.RangeLast != 4 || intent.ProviderUploadID != "native-part" || intent.DestinationKey != "destination" {
									t.Error("copy reached IO without exact durable intent", intent, err)
									w.WriteHeader(500)
									return
								}
							default:
								t.Error("copy dispatch intent missing before IO")
								w.WriteHeader(500)
								return
							}
						}

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
				if copyPart {
					f.handler.multipartStore = &observingPartCopyJournal{ObjectMultipartUploadStore: st, ObjectMultipartTransferStore: st, ObjectMultipartPartMutationStore: st.(state.ObjectMultipartPartMutationStore), ObjectMultipartPartCopyMutationStore: st.(state.ObjectMultipartPartCopyMutationStore), dispatched: copyReceipts}
				}

				if _, err := fences.BeginObjectBucketMutation(t.Context(), f.bucket, state.ObjectBucketMutationRequest); err != nil {
					t.Fatal(err)
				}
				var err error
				if copyPart {
					_, err = f.client.UploadPartCopy(t.Context(), &awss3.UploadPartCopyInput{Bucket: aws.String("assets"), Key: aws.String("destination"), UploadId: aws.String(id), PartNumber: aws.Int32(1), CopySource: aws.String("assets/source"), CopySourceRange: aws.String("bytes=1-4")})
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
