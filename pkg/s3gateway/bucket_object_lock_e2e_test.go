package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 564
func TestBucketObjectLockSDKE2E(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			var st multipartCopyIntegrationStore
			var pool *pgxpool.Pool
			var offset atomic.Int64
			now := time.Now().UTC()
			if pg {
				st, pool = multipartCopyPGStore(t)
			} else {
				m := state.NewMemStore()
				m.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
				st = m
			}
			advance := func() {
				if pool == nil {
					offset.Add(int64(20 * time.Minute))
					return
				}
				if _, err := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state='propagating' THEN clock_timestamp()-interval '1 second' ELSE propagation_until END;UPDATE object_bucket_object_lock SET retry_at=clock_timestamp()`); err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			body := ""
			var calls, puts atomic.Int32
			f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				mu.Lock()
				defer mu.Unlock()
				if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Error("unsigned native request")
				}
				w.Header().Set("Content-Type", "application/xml")
				if r.URL.Query().Has("versioning") {
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
					return
				}
				if !r.URL.Query().Has("object-lock") {
					t.Error("unexpected native call", r.URL)
					w.WriteHeader(500)
					return
				}
				if r.Method == http.MethodGet {
					if body == "" {
						w.WriteHeader(404)
						_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
						return
					}
					_, _ = io.WriteString(w, body)
					return
				}
				if r.Method != http.MethodPut {
					t.Error(r.Method)
					w.WriteHeader(400)
					return
				}
				data, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
				}
				body = string(data)
				puts.Add(1)
			}), objectstorage.Config{}, 0)
			configure := func(flags objectstorage.ObjectLockConfig) {
				policy := f.handler.registry.Accounting
				r, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: f.nativeEndpoint, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", ObjectLock: flags}}}, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
				if err != nil {
					t.Fatal(err)
				}
				f.handler.registry = r
			}
			configure(objectstorage.ObjectLockConfig{Enabled: true, EventHolds: true})
			public := httptest.NewTLSServer(f.handler)
			defer public.Close()
			u, _ := url.Parse(public.URL)
			f.handler.host = u.Host
			client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""), HTTPClient: public.Client()}, func(o *awss3.Options) {
				o.BaseEndpoint = aws.String(public.URL)
				o.UsePathStyle = true
				o.RetryMaxAttempts = 1
			})
			for _, tc := range []struct {
				method, query, body string
				status              int
			}{
				{"PUT", "object-lock", `<ObjectLockConfiguration/>`, 400},
				{"PUT", "object-lock", `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><Future/></ObjectLockConfiguration>`, 400},
				{"PUT", "object-lock", strings.Repeat(" ", int(api.MaxObjectLockBodyBytes)+1), 400},
				{"GET", "object-lock", "body", 400},
				{"GET", "object-lock&object-lock", "", 400},
				{"GET", "object-lock&unknown=true", "", 400},
				{"DELETE", "object-lock", "", 501},
				{"GET", "object-lock&x-amz-bucket-object-lock-token=ignored", "", 501},
			} {
				sum := sha256.Sum256([]byte(tc.body))
				r := httptest.NewRequest(tc.method, public.URL+"/assets?"+tc.query, strings.NewReader(tc.body))
				hash := hex.EncodeToString(sum[:])
				r.Header.Set("X-Amz-Content-Sha256", hash)
				if err := awsv4.NewSigner().SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, hash, "s3", "us-east-1", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				if w.Code != tc.status || calls.Load() != 0 {
					t.Fatal("invalid request reached native", tc, w.Code, w.Body.String(), calls.Load())
				}
			}
			_, err := client.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("assets")})
			assertSDKErrorCode(t, err, "ObjectLockConfigurationNotFoundError")
			cfg := &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled, Rule: &types.ObjectLockRule{DefaultRetention: &types.DefaultRetention{Mode: types.ObjectLockRetentionModeCompliance, Days: aws.Int32(3), DefaultEventHold: &types.EventHoldDuration{Years: aws.Int32(1)}}}}
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: cfg})
			assertSDKErrorCode(t, err, "OperationAborted")
			if puts.Load() != 0 {
				t.Fatal("unverified versioning mutated lock")
			}
			prepareObjectLockGatewayVersioning(t, st, f.bucket, advance)
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: cfg})
			if err != nil {
				t.Fatal(err)
			}
			out, err := client.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("assets")})
			if err != nil || out.ObjectLockConfiguration == nil || out.ObjectLockConfiguration.Rule == nil || aws.ToInt32(out.ObjectLockConfiguration.Rule.DefaultRetention.Days) != 3 || aws.ToInt32(out.ObjectLockConfiguration.Rule.DefaultRetention.DefaultEventHold.Years) != 1 || puts.Load() != 1 {
				t.Fatal(out, err, puts.Load())
			}
			configure(objectstorage.ObjectLockConfig{})
			f.handler.enabled = func() bool { return false }
			if _, err = client.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("assets")}); err != nil {
				t.Fatal("disabled cleanup read", err)
			}
			before := calls.Load()
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: cfg})
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			if calls.Load() != before {
				t.Fatal("disabled ingress contacted native")
			}
			f.handler.enabled = func() bool { return true }
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: cfg})
			assertSDKErrorCode(t, err, "NotImplemented")
			if calls.Load() != before {
				t.Fatal("disabled capability contacted native")
			}
			configure(objectstorage.ObjectLockConfig{Enabled: true})
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: cfg})
			assertSDKErrorCode(t, err, "NotImplemented")
			clear := &types.ObjectLockConfiguration{ObjectLockEnabled: types.ObjectLockEnabledEnabled}
			_, err = client.PutObjectLockConfiguration(t.Context(), &awss3.PutObjectLockConfigurationInput{Bucket: aws.String("assets"), ObjectLockConfiguration: clear})
			if err != nil {
				t.Fatal("clear defaults", err)
			}
			out, err = client.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("assets")})
			if err != nil || out.ObjectLockConfiguration.ObjectLockEnabled != types.ObjectLockEnabledEnabled || out.ObjectLockConfiguration.Rule != nil || puts.Load() != 2 {
				t.Fatal(out, err)
			}
			mu.Lock()
			body = `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled><FutureProtection>ON</FutureProtection></ObjectLockConfiguration>`
			mu.Unlock()
			_, err = client.GetObjectLockConfiguration(t.Context(), &awss3.GetObjectLockConfigurationInput{Bucket: aws.String("assets")})
			assertSDKErrorCode(t, err, "NotImplemented")
			j, err := st.(state.ObjectBucketObjectLockStore).GetObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || j.State != "waiting" || j.ObservedKnown || !j.EnabledRequired {
				t.Fatal("future policy lost protection", j, err)
			}
		})
	}
}

func prepareObjectLockGatewayVersioning(t *testing.T, st multipartCopyIntegrationStore, b state.ObjectBucket, advance func()) {
	t.Helper()
	v := st.(state.ObjectBucketVersioningStore)
	j, err := v.ObserveObjectBucketVersioning(t.Context(), b.AccountID, b.AppID, b.ID, "Enabled")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.ClaimObjectBucketVersioning(t.Context(), b.ID, "propagate")
	if err != nil {
		t.Fatal("claim propagation", j, err)
	}
	j, err = v.AdvanceObjectBucketVersioning(t.Context(), b.ID, j.Token, "Enabled")
	if err != nil {
		t.Fatal("begin propagation", j, err)
	}
	advance()
	j, err = v.ClaimObjectBucketVersioning(t.Context(), b.ID, "configure")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.AdvanceObjectBucketVersioning(t.Context(), b.ID, j.Token, "Enabled")
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.(state.ObjectCapacityStore).ClaimObjectCapacityReconciliation(t.Context(), j.CapacityJobID, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), c.ID, c.Token, "", nil); err != nil {
		t.Fatal(err)
	}
	advance()
	j, err = v.ClaimObjectBucketVersioning(t.Context(), b.ID, "verify")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.AdvanceObjectBucketVersioning(t.Context(), b.ID, j.Token, "Enabled")
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
}
