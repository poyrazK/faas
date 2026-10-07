package s3gateway

import (
	"encoding/xml"
	"errors"
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
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 582
func TestVersionProtectionSDKE2E(t *testing.T) {
	for _, tc := range []struct{ pg, null bool }{{false, false}, {true, false}, {false, true}, {true, true}} {
		pg := tc.pg
		t.Run(fmt.Sprint("postgres=", pg, "/null=", tc.null), func(t *testing.T) {
			var st multipartCopyIntegrationStore
			var pool *pgxpool.Pool
			var offset atomic.Int64
			if pg {
				st, pool = multipartCopyPGStore(t)
			} else {
				m := state.NewMemStore()
				now := time.Now().UTC()
				m.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
				st = m
			}
			advance := func() {
				if pool == nil {
					offset.Add(int64(20 * time.Minute))
					return
				}
				_, err := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=clock_timestamp(),propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END;UPDATE object_bucket_object_lock SET retry_at=clock_timestamp();UPDATE object_version_protection SET retry_at=clock_timestamp(),lease_until=CASE WHEN lease_until IS NULL THEN NULL ELSE clock_timestamp()-interval '1 second' END WHERE state IN ('waiting','applying')`)
				if err != nil {
					t.Fatal(err)
				}
			}
			var mu sync.Mutex
			hold := api.ObjectVersionLegalHold{Status: "OFF"}
			retention := api.ObjectVersionRetention{}
			var calls, puts atomic.Int32
			lost := true
			rejectNext := false
			key := "protected/目录 +%.txt"
			native := "private/+%version"
			if tc.null {
				native = "null"
			}
			f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/xml")
				if !strings.HasPrefix(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 ") {
					t.Error("unsigned provider call")
				}
				q := r.URL.Query()
				if q.Has("object-lock") {
					_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
					return
				}
				if q.Has("versioning") {
					_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
					return
				}
				if q.Get("versionId") != native || !strings.HasSuffix(r.URL.Path, "/"+key) {
					t.Error("wrong private binding", r.URL)
					w.WriteHeader(500)
					return
				}
				if r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
					t.Error("unexpected governance bypass")
				}
				w.Header().Set("X-Amz-Version-Id", native)
				if r.Method == http.MethodPut {
					puts.Add(1)
					if rejectNext {
						rejectNext = false
						w.WriteHeader(403)
						_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
						return
					}
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					if q.Has("legal-hold") {
						hold, err = objectstorage.DecodeObjectVersionLegalHold(body)
					} else if q.Has("retention") {
						next, e := objectstorage.DecodeObjectVersionRetention(body)
						err = e
						if next.EventHold == "ON" {
							days := int32(0)
							if next.EventHoldDuration.Days != nil {
								days = *next.EventHoldDuration.Days
							} else {
								days = *next.EventHoldDuration.Years * 365
							}
							date := time.Now().UTC().AddDate(0, 0, int(days)).Truncate(time.Millisecond)
							if retention.RetainUntilDate != nil && date.Before(*retention.RetainUntilDate) {
								date = *retention.RetainUntilDate
							}
							if next.RetainUntilDate != nil && date.Before(*next.RetainUntilDate) {
								date = *next.RetainUntilDate
							}
							next.RetainUntilDate = &date
						} else if next.EventHold == "OFF" {
							next.RetainUntilDate = retention.RetainUntilDate
							next.EventHoldDuration = retention.EventHoldDuration
						}
						retention = next
					} else {
						t.Error("unexpected mutation", r.URL)
					}
					if err != nil {
						t.Error(err)
					}
					if lost {
						lost = false
						w.WriteHeader(500)
						_, _ = io.WriteString(w, `<Error><Code>InternalError</Code></Error>`)
					}
					return
				}
				if q.Has("legal-hold") {
					_, _ = fmt.Fprintf(w, "<LegalHold><Status>%s</Status></LegalHold>", hold.Status)
					return
				}
				if q.Has("retention") {
					if retention.Empty() {
						_, _ = io.WriteString(w, `<Retention/>`)
					} else {
						out := versionRetentionXML{Mode: retention.Mode, EventHold: retention.EventHold, RetainUntilDate: retention.RetainUntilDate.Format(time.RFC3339Nano)}
						if retention.EventHoldDuration != nil {
							out.Duration = &bucketObjectLockPeriodXML{Days: retention.EventHoldDuration.Days, Years: retention.EventHoldDuration.Years}
						}
						body, e := xml.Marshal(out)
						if e != nil {
							t.Error(e)
						}
						_, _ = w.Write(body)
					}
					return
				}
				t.Error("unexpected read", r.URL)
			}), objectstorage.Config{}, 0)
			configure := func(enabled bool) {
				policy := f.handler.registry.Accounting
				r, err := objectstorage.NewRegistry(objectstorage.Config{Accounting: &policy, DefaultRegion: "us-east-1", Defaults: map[string]string{"us-east-1": "local"}, Backends: []objectstorage.BackendConfig{{ID: "local", Driver: "s3", Region: "us-east-1", Namespace: "integration", Endpoint: f.nativeEndpoint, AllowHTTP: true, PathStyle: true, S3Region: "us-east-1", AccessKeyEnv: "KEY", SecretKeyEnv: "SECRET", ObjectLock: objectstorage.ObjectLockConfig{Enabled: enabled, EventHolds: enabled}}}}, func(string) string { return "local-provider-test-credential" }, map[string]objectstorage.Factory{"s3": objectstorage.NewS3})
				if err != nil {
					t.Fatal(err)
				}
				f.handler.registry = r
			}
			configure(true)
			lock := st.(state.ObjectBucketObjectLockStore)
			cfg := api.ObjectBucketObjectLockConfiguration{Enabled: true}
			if _, err := lock.RequestObjectBucketObjectLock(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, cfg); err != nil {
				t.Fatal(err)
			}
			prepareObjectLockGatewayVersioning(t, st, f.bucket, advance)
			j, err := lock.ClaimObjectBucketObjectLock(t.Context(), f.bucket.ID, "lock")
			if err != nil {
				t.Fatal(j, err)
			}
			if _, err = lock.FinishObjectBucketObjectLock(t.Context(), f.bucket.ID, j.Token, cfg); err != nil {
				t.Fatal(err)
			}
			refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: key, ProviderVersionID: native}})
			if err != nil {
				t.Fatal(err)
			}
			version := refs[0].ID
			public := httptest.NewTLSServer(f.handler)
			defer public.Close()
			u, _ := url.Parse(public.URL)
			f.handler.host = u.Host
			client := awss3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(testAccess, testSecret, ""), HTTPClient: public.Client()}, func(o *awss3.Options) {
				o.BaseEndpoint = aws.String(public.URL)
				o.UsePathStyle = true
				o.RetryMaxAttempts = 1
			})
			// New SDK writes must fail before dispatch and leave no journal.
			fences := st.(state.ObjectBucketWriteFenceStore)
			capture, err := fences.AcquireObjectBucketWriteFence(t.Context(), f.bucket, uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			var fencedResponse *smithyhttp.ResponseError
			if !errors.As(err, &fencedResponse) || fencedResponse.Response == nil || fencedResponse.Response.Header.Get("X-Gregale-Protection-Id") != "" {
				t.Fatal("fenced admission advertised an uncreated journal", err)
			}
			observed, err := fences.ReadObjectBucketWriteFence(t.Context(), f.bucket, capture.Token)
			if err != nil || observed.Protections != 0 || observed.Requests != 0 || puts.Load() != 0 {
				t.Fatal("fenced SDK request admitted provider work", observed, err, puts.Load())
			}
			if err := fences.ReleaseObjectBucketWriteFence(t.Context(), f.bucket, capture.Token); err != nil {
				t.Fatal(err)
			}
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			if puts.Load() != 1 {
				t.Fatal("native retries", puts.Load())
			}
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			assertSDKErrorCode(t, err, "OperationAborted")
			if puts.Load() != 1 {
				t.Fatal("public retry redispatched mutation", puts.Load())
			}
			ops := st.(state.ObjectVersionProtectionStore)
			_, err = ops.DueObjectVersionProtection(t.Context(), api.ObjectVersionProtectionBatch)
			if err != nil {
				t.Fatal(err)
			}
			advance()
			rows, err := ops.DueObjectVersionProtection(t.Context(), api.ObjectVersionProtectionBatch)
			if err != nil || len(rows) != 1 || !rows[0].Dispatched {
				t.Fatal(rows, err)
			}
			_, err = client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			assertSDKErrorCode(t, err, "OperationAborted")
			if !tc.null {
				before := calls.Load()
				_, err = client.GetObjectLegalHold(t.Context(), &awss3.GetObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String("foreign"), VersionId: aws.String(version)})
				assertSDKErrorCode(t, err, "NoSuchVersion")
				if calls.Load() != before {
					t.Fatal("foreign key reached provider")
				}
			}
			configure(false)
			if pool != nil {
				restarted := state.NewPgStore(pool)
				f.handler.store = restarted
				ops = restarted
			}
			backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			svc := objectstorage.VersionProtectionService{Store: ops, References: st.(state.ObjectVersionReferenceStore), BucketLock: lock, Provider: backend.Provider, BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}
			// An absent/mismatched readback after dispatch cannot release custody.
			mu.Lock()
			hold.Status = "OFF"
			mu.Unlock()
			uncertain, err := svc.Reconcile(t.Context(), f.bucket, rows[0].ID)
			if err == nil || uncertain.State != "waiting" || !uncertain.Dispatched || puts.Load() != 1 {
				t.Fatal("mismatched readback discarded or repeated mutation", uncertain, err, puts.Load())
			}
			_, err = client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			assertSDKErrorCode(t, err, "OperationAborted")
			mu.Lock()
			hold.Status = "ON"
			mu.Unlock()
			advance()
			done, err := svc.Reconcile(t.Context(), f.bucket, rows[0].ID)
			if err != nil || done.State != "ready" || puts.Load() != 1 {
				t.Fatal("lost ACK recovery repeated mutation", done, err, puts.Load())
			}
			read, err := client.GetObjectLegalHold(t.Context(), &awss3.GetObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			if err != nil || read.LegalHold.Status != types.ObjectLockLegalHoldStatusOn {
				t.Fatal(read, err)
			}
			configure(true)
			until := time.Now().UTC().Add(time.Hour).Truncate(time.Millisecond)
			_, err = client.PutObjectRetention(t.Context(), &awss3.PutObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, RetainUntilDate: &until}})
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.GetObjectRetention(t.Context(), &awss3.GetObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			if err != nil || result.Retention.Mode != types.ObjectLockRetentionModeCompliance || result.Retention.RetainUntilDate.Before(until) {
				t.Fatal(result, err)
			}
			count := puts.Load()
			_, err = client.PutObjectRetention(t.Context(), &awss3.PutObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), Retention: &types.ObjectLockRetention{}})
			assertSDKErrorCode(t, err, "OperationAborted")
			if puts.Load() != count {
				t.Fatal("active retention shortened")
			}
			mu.Lock()
			rejectNext = true
			mu.Unlock()
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff}})
			assertSDKErrorCode(t, err, "AccessDenied")
			// A positively rejected PUT is terminal and releases the admission fence.
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOn}})
			if err != nil || puts.Load() != count+1 {
				t.Fatal("positive rejection retained fence or repeated PUT", err, puts.Load())
			}
			// Event holds use the same signed S3 retention subresource.
			_, err = client.PutObjectRetention(t.Context(), &awss3.PutObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, EventHold: types.ObjectLockEventHoldOn, EventHoldDuration: &types.EventHoldDuration{Days: aws.Int32(30)}, RetainUntilDate: &until}})
			if err != nil {
				t.Fatal("enable event hold", err)
			}
			eventRead, err := client.GetObjectRetention(t.Context(), &awss3.GetObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			if err != nil || eventRead.Retention.EventHold != types.ObjectLockEventHoldOn {
				t.Fatal("event read", eventRead, err)
			}
			mu.Lock()
			lost = true
			mu.Unlock()
			eventPuts := puts.Load()
			_, err = client.PutObjectRetention(t.Context(), &awss3.PutObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version), Retention: &types.ObjectLockRetention{Mode: types.ObjectLockRetentionModeCompliance, EventHold: types.ObjectLockEventHoldOff}})
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			advance()
			configure(false)
			if pool != nil {
				ops = state.NewPgStore(pool)
				f.handler.store = state.NewPgStore(pool)
			}
			rows, err = ops.DueObjectVersionProtection(t.Context(), api.ObjectVersionProtectionBatch)
			if err != nil || len(rows) != 1 || rows[0].EventHoldBaseline == nil {
				t.Fatal("event release lost baseline", rows, err)
			}
			svc.Store = ops
			done, err = svc.Reconcile(t.Context(), f.bucket, rows[0].ID)
			if err != nil || done.State != "ready" || puts.Load() != eventPuts+1 {
				t.Fatal("event release redispatched", done, err, puts.Load())
			}
			eventRead, err = client.GetObjectRetention(t.Context(), &awss3.GetObjectRetentionInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(version)})
			if err != nil || eventRead.Retention.EventHold != types.ObjectLockEventHoldOff || eventRead.Retention.RetainUntilDate == nil || eventRead.Retention.RetainUntilDate.Before(until) {
				t.Fatal("released event hold", eventRead, err)
			}
			configure(true)
			_, err = client.PutObjectLegalHold(t.Context(), &awss3.PutObjectLegalHoldInput{Bucket: aws.String("assets"), Key: aws.String(key), VersionId: aws.String(uuid.NewString()), LegalHold: &types.ObjectLockLegalHold{Status: types.ObjectLockLegalHoldStatusOff}})
			if !errors.Is(err, nil) {
				assertSDKErrorCode(t, err, "NoSuchVersion")
			} else {
				t.Fatal("foreign version accepted")
			}
		})
	}
}
