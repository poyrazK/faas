package object

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectchecksum"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type multipartRecoveryHost struct{ receiptRecoveryHost }

func (multipartRecoveryHost) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
func (multipartRecoveryHost) EmitAudit(context.Context, string, *string, map[string]any) {}

func TestOwnedMultipartGatewayRecoveryAfterLostCompletion(t *testing.T) {
	testOwnedMultipartGatewayRecovery(t, false)
}

func TestOwnedMultipartGatewayRecoveryAfterSwitchover(t *testing.T) {
	testOwnedMultipartGatewayRecovery(t, true)
}

func TestOwnedMultipartGatewaySettlementAcknowledgements(t *testing.T) {
	testOwnedMultipartGatewayRecovery(t, false, true)
}

func testOwnedMultipartGatewayRecovery(t *testing.T, switchover bool, recovery ...bool) {
	settlements := len(recovery) != 0 && recovery[0]
	for _, scenario := range []string{"overwritten", "deleted", "rejected", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "data")
			node, err := objectnode.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if node != nil {
					node.Close()
				}
			}()
			if err = node.CreateBucket("physical"); err != nil {
				t.Fatal(err)
			}
			var follower string
			if switchover || settlements {
				node, follower = prepareOwnedSwitchover(t, node, root)
			}
			if settlements {
				if _, err = node.EnableRecoveryRetention(); err != nil {
					t.Fatal(err)
				}
			}
			makeHandler := func() *s3gateway.DiskHandler {
				h, e := s3gateway.NewDiskHandler(s3gateway.DiskConfig{Store: node, AccessKeyID: "node-key", SecretAccessKey: "node-secret", Region: "us-east-1", MinFreeBytes: 1})
				if e != nil {
					t.Fatal(e)
				}
				return h
			}
			native := makeHandler()
			var mu sync.Mutex
			blockProof, dropCompletion := true, true
			partPuts, completions := 0, 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Header.Get(objectstorage.OwnedNodeMultipartReceiptHeader) != "" && blockProof {
					w.WriteHeader(503)
					return
				}
				if r.Method == http.MethodPut && r.URL.Query().Has("uploadId") {
					partPuts++
				}
				if r.Method != http.MethodPost || !r.URL.Query().Has("uploadId") {
					native.ServeHTTP(w, r)
					return
				}
				completions++
				if !dropCompletion {
					native.ServeHTTP(w, r)
					return
				}
				dropCompletion = false
				if scenario != "absent" {
					recorder := httptest.NewRecorder()
					native.ServeHTTP(recorder, r)
					want := 200
					if scenario == "rejected" {
						want = 412
					}
					if recorder.Code != want {
						t.Error("private completion", recorder.Code, recorder.Body.String())
					}
				}
				connection, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				connection.Close()
			}))
			defer upstream.Close()
			st, registry, bucket, _ := receiptRecoverySettlementFixture(t, upstream.URL, settlements, 2*api.DefaultObjectUploadBytes)
			var clock atomic.Int64
			clock.Store(time.Now().UnixNano())
			st.SetClockForTest(func() time.Time { return time.Unix(0, clock.Load()).UTC() })
			server := httptest.NewUnstartedServer(nil)
			gateway, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: st, RequestMetrics: st, Host: server.Listener.Addr().String(), Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, HTTPClient: upstream.Client(), OpenSecret: func([]byte) (string, error) { return "customer-secret", nil }})
			if err != nil {
				t.Fatal(err)
			}
			server.Config.Handler = gateway
			server.Start()
			defer server.Close()
			client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("GREGreceiptcustomer", "customer-secret", ""), HTTPClient: server.Client(), RetryMaxAttempts: 1})
			create := &s3.CreateMultipartUploadInput{Bucket: aws.String("assets"), Key: aws.String("key"), ContentType: aws.String("text/plain")}
			policy := objectchecksum.Policy{}
			if scenario == "deleted" {
				policy = objectchecksum.Policy{Algorithm: "SHA256", Type: objectchecksum.Composite}
				create.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
				create.ChecksumType = types.ChecksumTypeComposite
			}
			created, err := client.CreateMultipartUpload(t.Context(), create)
			if err != nil {
				t.Fatal(err)
			}
			id := aws.ToString(created.UploadId)
			selected := make([]types.CompletedPart, 0, 2)
			bodies := [][]byte{bytes.Repeat([]byte("x"), int(api.MinMultipartPartBytes)), []byte("tail")}
			for i, body := range bodies {
				part := &s3.UploadPartInput{Bucket: create.Bucket, Key: create.Key, UploadId: created.UploadId, PartNumber: aws.Int32(int32(i + 1)), Body: bytes.NewReader(body)}
				completed := types.CompletedPart{PartNumber: part.PartNumber}
				if !policy.Empty() {
					h := objectchecksum.NewHash(policy.Algorithm)
					h.Write(body)
					value := base64.StdEncoding.EncodeToString(h.Sum(nil))
					part.ChecksumSHA256 = aws.String(value)
					part.ChecksumAlgorithm = types.ChecksumAlgorithmSha256
					completed.ChecksumSHA256 = aws.String(value)
				}
				ack, e := client.UploadPart(t.Context(), part)
				if e != nil {
					t.Fatal(e)
				}
				completed.ETag = ack.ETag
				completed.ChecksumCRC32, completed.ChecksumCRC32C, completed.ChecksumCRC64NVME = ack.ChecksumCRC32, ack.ChecksumCRC32C, ack.ChecksumCRC64NVME
				completed.ChecksumSHA1, completed.ChecksumSHA256 = ack.ChecksumSHA1, ack.ChecksumSHA256
				selected = append(selected, completed)
			}
			if scenario == "rejected" {
				if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("prior"), 5, objectnode.PutOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			request := &s3.CompleteMultipartUploadInput{Bucket: create.Bucket, Key: create.Key, UploadId: created.UploadId, MultipartUpload: &types.CompletedMultipartUpload{Parts: selected}}
			if scenario == "rejected" {
				request.IfNoneMatch = aws.String("*")
			}
			if _, err = client.CompleteMultipartUpload(t.Context(), request); err == nil {
				t.Fatal("lost response and unavailable proof incorrectly acknowledged")
			}
			pending, err := st.GetObjectMultipartUpload(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID, id)
			if err != nil || !pending.CompletionDispatched || pending.State != state.ObjectMultipartCompleting && pending.State != state.ObjectMultipartCompletingConditional {
				t.Fatal("completion admission lost", pending, err)
			}
			if scenario == "overwritten" {
				_, err = node.Put(t.Context(), "physical", "key", strings.NewReader("later"), 5, objectnode.PutOptions{})
			} else if scenario == "absent" {
				err = node.AbortMultipart("physical", "key", pending.ProviderUploadID)
			} else {
				err = node.Delete("physical", "key")
			}
			if err != nil {
				t.Fatal(err)
			}
			nextDirectory := root
			if switchover {
				promoteOwnedSwitchover(t, follower, upstream.URL)
				nextDirectory = follower
			}
			mu.Lock()
			if err = node.Close(); err == nil {
				node, err = objectnode.Open(nextDirectory)
			}
			if err != nil {
				mu.Unlock()
				t.Fatal(err)
			}
			native = makeHandler()
			blockProof = false
			mu.Unlock()
			if _, err = st.AcquireObjectBucketWriteFence(t.Context(), bucket, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			clock.Add(int64(time.Hour))
			service := Service{Host: multipartRecoveryHost{receiptRecoveryHost{store: st, registry: registry}}}
			if err = service.ReconcileObjectMultipartUploads(t.Context(), nil); err != nil {
				t.Fatal(err)
			}
			done, err := st.GetObjectMultipartUpload(t.Context(), bucket.AccountID, bucket.AppID, bucket.ID, id)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "absent":
				if done.State != state.ObjectMultipartCompleting || done.CompletionETag != "" {
					t.Fatal("missing proof settled completion", done)
				}
			case "rejected":
				if done.State != state.ObjectMultipartAborting || done.CompletionErrorCode != "precondition_failed" {
					t.Fatal("durable rejection not retained", done)
				}
			default:
				if done.State != state.ObjectMultipartCompleted || done.CompletionETag == "" || done.SizeBytes != api.MinMultipartPartBytes+4 {
					t.Fatal("completion not recovered", done)
				}
			}
			if settlements && scenario != "absent" {
				if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil {
					t.Fatal(err)
				}
				proof, proofErr := node.MultipartReceipt("physical", "key", id)
				if proofErr != nil || (proof.SettledAt == nil) != (scenario == "rejected") {
					t.Fatal("multipart proof acknowledgement did not follow terminal settlement", proof, proofErr)
				}
				if scenario == "rejected" {
					clock.Add(int64(time.Hour))
					if err = service.ReconcileObjectMultipartUploads(t.Context(), nil); err != nil {
						t.Fatal(err)
					}
					if err = service.ReconcileObjectNodeSettlements(t.Context(), nil); err != nil {
						t.Fatal(err)
					}
					proof, proofErr = node.MultipartReceipt("physical", "key", id)
					if proofErr != nil || proof.SettledAt == nil {
						t.Fatal("terminal rejection was not acknowledged after verified abort", proof, proofErr)
					}
				}
			}
			mu.Lock()
			writes, posts := partPuts, completions
			mu.Unlock()
			if writes != 2 || scenario != "absent" && posts != 1 {
				t.Fatal("recovery replayed completed bytes or assembly", writes, posts)
			}
			_, file, err := node.Get("physical", "key")
			if scenario == "overwritten" {
				if err != nil {
					t.Fatal(err)
				}
				body, e := io.ReadAll(file)
				file.Close()
				if e != nil || string(body) != "later" {
					t.Fatal("recovery replaced later bytes", e)
				}
			} else if !errors.Is(err, objectnode.ErrNoObject) {
				if file != nil {
					file.Close()
				}
				t.Fatal("recovery resurrected bytes", err)
			}
		})
	}
}
