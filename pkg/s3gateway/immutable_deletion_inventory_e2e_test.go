package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestVersionDeletionRejectsEmptyAndDuplicateSelector(t *testing.T) {
	p := &versionDeleteHTTPFixture{gone: map[string]bool{}}
	f := newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	for _, tc := range []struct {
		query  string
		status int
	}{{"versionId=", http.StatusBadRequest}, {url.Values{"versionId": {uuid.NewString(), uuid.NewString()}}.Encode(), http.StatusNotImplemented}} {
		r := httptest.NewRequest(http.MethodDelete, "http://"+f.handler.host+"/assets/"+url.PathEscape(publicVersionTestKey)+"?"+tc.query, nil)
		r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
		if e := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", f.handler.now()); e != nil {
			t.Fatal(e)
		}
		w := httptest.NewRecorder()
		f.handler.ServeHTTP(w, r)
		if w.Code != tc.status || p.count() != 0 {
			t.Fatal("invalid selected delete became ordinary deletion", w.Code, w.Body.String(), p.count())
		}
	}
}

// adr: 406
func TestImmutableDeletionInventorySDKE2E(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			st, pool, advance := immutableDeletionTestStore(t, pg)
			p := &versionDeleteHTTPFixture{gone: map[string]bool{}, paginated: true}
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
			refs, e := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeNew}})
			if e != nil {
				t.Fatal(e)
			}
			cap := st.(state.ObjectCapacityStore)
			job, e := cap.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if e != nil {
				t.Fatal(e)
			}
			job, e = cap.ClaimObjectCapacityReconciliation(t.Context(), job.ID, uuid.NewString())
			if e != nil {
				t.Fatal(e)
			}
			backend, e := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
			if e != nil {
				t.Fatal(e)
			}
			inventory := backend.Provider.(objectstorage.ObjectVersionInventoryProvider)
			page, e := inventory.ListObjectVersions(t.Context(), f.bucket.PhysicalName, job.InventoryCursor, api.ObjectVersionInventoryPageSize)
			if e != nil || page.NextCursor == "" {
				t.Fatal(page, e)
			}
			job, e = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), job.ID, job.Token, page.NextCursor, immutableDeletionPageRecords(page))
			if e != nil {
				t.Fatal(e)
			}
			if pool != nil {
				st = state.NewPgStore(pool)
				f.store = st
				f.handler.store = st
				f.handler.requestMetrics = st
			}
			input := &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(refs[0].ID)}
			_, e = f.client.DeleteObject(t.Context(), input)
			assertSDKErrorCode(t, e, "ServiceUnavailable")
			if p.count() != 0 {
				t.Fatal("delete invalidated cursor", p.count())
			}
			cap = st.(state.ObjectCapacityStore)
			for job.State == "waiting" {
				job, e = cap.ClaimObjectCapacityReconciliation(t.Context(), job.ID, uuid.NewString())
				if e != nil {
					t.Fatal(e)
				}
				page, e = inventory.ListObjectVersions(t.Context(), f.bucket.PhysicalName, job.InventoryCursor, api.ObjectVersionInventoryPageSize)
				if e != nil {
					t.Fatal(page, e)
				}
				job, e = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), job.ID, job.Token, page.NextCursor, immutableDeletionPageRecords(page))
				if e != nil {
					t.Fatal(job, e)
				}
			}
			if job.State != "completed" || job.ScannedPages != 3 || job.AfterBytes != 16+int64(len(publicVersionTestKey)) {
				t.Fatal(job)
			}
			p.mu.Lock()
			p.lose = true
			p.mu.Unlock()
			_, e = f.client.DeleteObject(t.Context(), input)
			assertSDKErrorCode(t, e, "ServiceUnavailable")
			var response *smithyhttp.ResponseError
			if !errors.As(e, &response) || response.Response == nil {
				t.Fatal(e)
			}
			id := response.Response.Header.Get("X-Gregale-Delete-Id")
			if _, e = cap.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID); !errors.Is(e, state.ErrConflict) {
				t.Fatal("inventory passed uncertain delete", e)
			}
			p.mu.Lock()
			p.denied = true
			p.mu.Unlock()
			advance()
			svc := objectstorage.DeletionService{Store: st.(state.ObjectDeletionStore), Provider: backend.Provider, BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}
			receipt, e := svc.Recover(t.Context(), f.bucket, id)
			if e == nil || receipt.State != "dispatched" || p.count() != 2 {
				t.Fatal("retry rejection settled earlier uncertain attempt", receipt, e, p.count())
			}
			if _, e = cap.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID); !errors.Is(e, state.ErrConflict) {
				t.Fatal("rejection released original fence", e)
			}
			p.mu.Lock()
			p.denied = false
			p.mu.Unlock()
			advance()
			receipt, e = svc.Recover(t.Context(), f.bucket, id)
			if e != nil || receipt.State != "completed" || receipt.VersionID != refs[0].ID || p.count() != 3 {
				t.Fatal(receipt, e, p.count())
			}
			usage, e := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if e != nil || usage.Buckets[0].BaselineBytes != job.AfterBytes {
				t.Fatal("deletion refunded quota", usage, e)
			}
		})
	}
}

func immutableDeletionPageRecords(page objectstorage.ObjectVersionsPage) []state.ObjectVersionInventoryRecord {
	records := make([]state.ObjectVersionInventoryRecord, 0, len(page.Items))
	for _, v := range page.Items {
		sum := sha256.Sum256([]byte(v.Key + "\x00" + v.ProviderVersionID))
		records = append(records, state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(sum[:]), Bytes: v.SizeBytes})
	}
	return records
}

func immutableDeletionTestStore(t *testing.T, pg bool) (multipartCopyIntegrationStore, *pgxpool.Pool, func()) {
	t.Helper()
	if pg {
		st, pool := multipartCopyPGStore(t)
		return st, pool, func() {
			if _, e := pool.Exec(t.Context(), `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE now()-interval '1 second' END,retry_at=now() WHERE state IN ('prepared','dispatched')`); e != nil {
				t.Fatal(e)
			}
		}
	}
	st := state.NewMemStore()
	now := time.Now().UTC()
	st.SetClockForTest(func() time.Time { return now })
	return st, nil, func() { now = now.Add(api.ObjectDeletionRetry + time.Second) }
}
