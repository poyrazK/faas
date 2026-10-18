package object

import (
	"context"
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
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/s3gateway"
	"github.com/onebox-faas/faas/pkg/state"
)

type receiptRecoveryHost struct {
	Host
	store    *state.MemStore
	registry *objectstorage.Registry
}

func (h receiptRecoveryHost) Store() state.Store                     { return h.store }
func (h receiptRecoveryHost) ObjectStorage() *objectstorage.Registry { return h.registry }
func (h receiptRecoveryHost) S3RuntimeEnabled() bool                 { return false }
func (h receiptRecoveryHost) Logger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// Exercise the real customer gateway, private SigV4 node, S3 adapter and apid
// worker. Losing the provider response leaves a dispatched admission in state.
func TestOwnedReceiptGatewayRecoveryAfterRestart(t *testing.T) {
	testOwnedReceiptGatewayRecovery(t, false, false)
}

func TestOwnedReceiptGatewayRecoveryAfterSwitchover(t *testing.T) {
	testOwnedReceiptGatewayRecovery(t, true, false)
}

func TestOwnedReplicaDurabilityReceiptGatewayRecovery(t *testing.T) {
	testOwnedReceiptGatewayRecovery(t, false, true)
}

func testOwnedReceiptGatewayRecovery(t *testing.T, switchover, replica bool) {
	for _, scenario := range []string{"overwritten", "deleted", "rejected", "absent"} {
		t.Run(scenario, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "data")
			node, err := objectnode.Open(root)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if node != nil {
					_ = node.Close()
				}
			}()
			if err = node.CreateBucket("physical"); err != nil {
				t.Fatal(err)
			}
			var follower string
			if switchover || replica {
				node, follower = prepareOwnedSwitchover(t, node, root)
			}
			if replica {
				node = configureOwnedReplica(t, node, root, follower)
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
			var receipt string
			puts := 0
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				if r.Method != http.MethodPut {
					native.ServeHTTP(w, r)
					return
				}
				puts++
				receipt = r.Header.Get(objectnode.ReceiptMetadataHeader)
				if receipt == "" {
					receipt = r.URL.Query().Get(objectnode.ReceiptMetadataHeader)
				}
				if scenario != "absent" {
					recorder := httptest.NewRecorder()
					request := r
					if replica {
						wait, cancel := context.WithTimeout(r.Context(), 200*time.Millisecond)
						defer cancel()
						request = r.WithContext(wait)
					}
					native.ServeHTTP(recorder, request)
					want := http.StatusOK
					if scenario == "rejected" {
						want = http.StatusPreconditionFailed
					}
					if replica {
						want = http.StatusServiceUnavailable
					}
					if recorder.Code != want {
						t.Error("unexpected private write outcome", recorder.Code, recorder.Body.String())
					}
				}
				connection, _, e := w.(http.Hijacker).Hijack()
				if e != nil {
					t.Error(e)
					return
				}
				_ = connection.Close() // Lose the acknowledgment after the node has decided.
			}))
			defer upstream.Close()
			st, registry, bucket, subject := receiptRecoveryFixture(t, upstream.URL)
			var clock atomic.Int64
			clock.Store(time.Now().UnixNano())
			st.SetClockForTest(func() time.Time { return time.Unix(0, clock.Load()).UTC() })
			if scenario == "rejected" {
				if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("prior"), 5, objectnode.PutOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			server := httptest.NewUnstartedServer(nil)
			gateway, err := s3gateway.New(s3gateway.Config{Registry: registry, Store: st, RequestMetrics: st, Host: server.Listener.Addr().String(), Region: "us-east-1", SpoolDir: t.TempDir(), MinSpoolFreeBytes: 1, HTTPClient: upstream.Client(), OpenSecret: func([]byte) (string, error) { return "customer-secret", nil }})
			if err != nil {
				t.Fatal(err)
			}
			server.Config.Handler = gateway
			server.Start()
			defer server.Close()
			client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String(server.URL), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider("GREGreceiptcustomer", "customer-secret", ""), HTTPClient: server.Client(), RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, ResponseChecksumValidation: aws.ResponseChecksumValidationWhenRequired})
			input := &s3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("key"), Body: strings.NewReader("original"), ContentType: aws.String("text/plain")}
			if scenario == "rejected" {
				input.IfNoneMatch = aws.String("*")
			}
			if _, err = client.PutObject(t.Context(), input); err == nil {
				t.Fatal("lost response incorrectly acknowledged")
			}
			mu.Lock()
			receiptID := receipt
			mu.Unlock()
			if _, err = uuid.Parse(receiptID); err != nil {
				t.Fatal("gateway did not bind its durable receipt", err)
			}
			pending, err := st.GetObjectUploadReceipt(t.Context(), bucket.AccountID, bucket.AppID, "", subject, receiptID)
			if err != nil || pending.Status != "pending" || pending.WritePhase != state.ObjectUploadDispatched {
				t.Fatal("uncertainty did not retain admission", pending, err)
			}
			original, proofErr := node.Receipt("physical", "key", receiptID)
			if scenario != "absent" && proofErr != nil {
				t.Fatal(proofErr)
			}
			if scenario == "overwritten" || scenario == "absent" {
				if _, err = node.Put(t.Context(), "physical", "key", strings.NewReader("later"), 5, objectnode.PutOptions{}); err != nil {
					t.Fatal(err)
				}
			} else if err = node.Delete("physical", "key"); err != nil {
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
			mu.Unlock()
			// A write fence and disabled runtime enrollment must not stop recovery.
			if _, err = st.AcquireObjectBucketWriteFence(t.Context(), bucket, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			clock.Add(int64(time.Hour))
			service := Service{Host: receiptRecoveryHost{store: st, registry: registry}}
			outcome := ""
			if replica {
				before, err := st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				probe, stop := context.WithTimeout(t.Context(), 80*time.Millisecond)
				err = service.ReconcileObjectUploads(probe, func(_, result string) { outcome = result })
				stop()
				if err != nil && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatal(err)
				}
				unconfirmed, err := st.GetObjectUploadReceipt(t.Context(), bucket.AccountID, bucket.AppID, "", subject, receiptID)
				if err != nil || unconfirmed.Status != "pending" || unconfirmed.WritePhase != state.ObjectUploadDispatched || outcome != "deferred" {
					t.Fatal("local receipt settled replica admission", unconfirmed, outcome, err)
				}
				after, err := st.ObjectUsage(t.Context(), bucket.AccountID, time.Now())
				if err != nil || len(after.Buckets) != 1 || after.Buckets[0].GrantedBytes != before.Buckets[0].GrantedBytes || after.Buckets[0].GrantedKeys != before.Buckets[0].GrantedKeys {
					t.Fatal("unconfirmed outcome released capacity", before, after, err)
				}
				confirmOwnedReplica(t, follower, upstream.URL)
				clock.Add(int64(time.Minute))
			}
			if err = service.ReconcileObjectUploads(t.Context(), func(_, result string) { outcome = result }); err != nil {
				t.Fatal(err)
			}
			done, err := st.GetObjectUploadReceipt(t.Context(), bucket.AccountID, bucket.AppID, "", subject, receiptID)
			if err != nil {
				t.Fatal(err)
			}
			want, phase := "completed", state.ObjectUploadSettled
			switch scenario {
			case "rejected":
				want = "failed"
				if done.ErrorCode != "provider_write_rejected" {
					t.Fatal(done)
				}
			case "absent":
				want, phase = "pending", state.ObjectUploadDispatched
				if outcome != "deferred" || done.ErrorCode != "provider_write_uncertain" {
					t.Fatal("missing proof settled admission", done, outcome)
				}
			}
			if done.Status != want || done.WritePhase != phase || want != "pending" && outcome != want || want == "completed" && done.ETag != original.Object.ETag {
				t.Fatal("wrong recovery settlement", done, outcome)
			}
			mu.Lock()
			attempts := puts
			mu.Unlock()
			if attempts != 1 {
				t.Fatal("recovery replayed an upload", attempts)
			}
			if switchover || replica {
				checkOwnedSwitchoverCapacity(t, st, registry, bucket, scenario == "absent", scenario == "overwritten" || scenario == "absent")
			}
			_, file, err := node.Get("physical", "key")
			if scenario == "deleted" || scenario == "rejected" {
				if !errors.Is(err, objectnode.ErrNoObject) {
					t.Fatal("recovery resurrected an object", err)
				}
			} else if err != nil {
				t.Fatal(err)
			} else {
				body, readErr := io.ReadAll(file)
				file.Close()
				if readErr != nil || string(body) != "later" {
					t.Fatal("recovery changed later bytes", string(body), readErr)
				}
			}
		})
	}
}

func receiptRecoveryFixture(t *testing.T, endpoint string, maxBytes ...int64) (*state.MemStore, *objectstorage.Registry, state.ObjectBucket, string) {
	return receiptRecoverySettlementFixture(t, endpoint, false, maxBytes...)
}

func receiptRecoverySettlementFixture(t *testing.T, endpoint string, settlements bool, maxBytes ...int64) (*state.MemStore, *objectstorage.Registry, state.ObjectBucket, string) {
	t.Helper()
	st := state.NewMemStore()
	policy := api.ObjectStoragePolicy{MaxAccountBytes: 1000, MaxBucketBytes: 1000, MaxAccountKeys: 100, MaxMonthlyCostMillicents: 100, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1000, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}
	if len(maxBytes) != 0 {
		policy.MaxAccountBytes, policy.MaxBucketBytes = maxBytes[0], maxBytes[0]
	}
	registry, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "owned"}, Backends: []objectstorage.BackendConfig{{ID: "owned", Driver: "s3", Region: "us-east-1", S3Region: "us-east-1", Namespace: "test-node", Endpoint: endpoint, AllowHTTP: true, PathStyle: true, OwnedNodeReceipts: true, OwnedNodeSettlements: settlements, AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET"}}}, func(name string) string {
		switch name {
		case "KEY":
			return "node-key"
		case "SECRET":
			return "node-secret"
		default:
			return ""
		}
	}, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
	if err != nil {
		t.Fatal(err)
	}
	backend, err := registry.Default("us-east-1")
	if err != nil {
		t.Fatal(err)
	}
	account, err := st.CreateAccount(t.Context(), uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(t.Context(), state.App{AccountID: account.ID, Slug: "receipt", Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	bucket, err := st.ReserveObjectBucket(t.Context(), state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", PhysicalName: "physical", BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Region: "us-east-1"}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(t.Context(), account.ID, app.ID, bucket.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(t.Context(), bucket.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	bucket.State = "ready"
	subject := uuid.NewString()
	if _, err = st.CreateObjectS3Credential(t.Context(), state.ObjectS3Credential{ID: subject, AccountID: account.ID, BucketID: bucket.ID, AccessKeyID: "GREGreceiptcustomer", SecretSealed: []byte("sealed"), KID: "test", Label: "test", Permission: state.ObjectBucketPermissionReadWrite, Status: state.ObjectS3CredentialStatusActive}, 10); err != nil {
		t.Fatal(err)
	}
	if err = st.ClaimObjectInventory(t.Context(), bucket.ID, "initial"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectInventory(t.Context(), bucket.ID, "initial", 0, 0); err != nil {
		t.Fatal(err)
	}
	if err = st.RecordObjectUsageReport(t.Context(), api.ObjectStorageUsageReport{AccountID: account.ID, BackendID: backend.ID, BackendFingerprint: backend.Fingerprint, Source: "provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	return st, registry, bucket, subject
}
