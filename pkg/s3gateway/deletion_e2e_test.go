package s3gateway

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"
	awsv4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type mutableDeleteFixture struct {
	mu                sync.Mutex
	status            string
	markers           []string
	deletes           int
	lost              bool
	denied            bool
	nullGone          bool
	truncatedBaseline bool
}

const mutableDeleteKey = "目录 /+%.txt"

func (p *mutableDeleteFixture) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.deletes
}
func (p *mutableDeleteFixture) nullDeleted() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.nullGone
}

func (p *mutableDeleteFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	q := r.URL.Query()
	w.Header().Set("Content-Type", "application/xml")
	if q.Has("versioning") {
		_, _ = fmt.Fprintf(w, "<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>", p.status)
		return
	}
	if q.Has("versions") {
		ids := append(append([]string{}, p.markers...), "data-old")
		index := 0
		if q.Get("version-id-marker") != "" {
			for i, id := range ids {
				if id == q.Get("version-id-marker") {
					index = i + 1
					break
				}
			}
		}
		if index >= len(ids) {
			t.Error("invalid continuation")
			w.WriteHeader(500)
			return
		}
		end := len(ids)
		// Force continuation pages to exercise preparation and restart recovery.
		if p.deletes > 0 || p.truncatedBaseline {
			end = index + 1
		}
		next := end < len(ids)
		_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>%t</IsTruncated>`, next)
		key := url.PathEscape(mutableDeleteKey)
		if next {
			_, _ = fmt.Fprintf(w, `<NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>%s</NextVersionIdMarker>`, key, ids[end-1])
		}
		for i := index; i < end; i++ {
			id := ids[i]
			if id == "data-old" {
				_, _ = fmt.Fprintf(w, `<Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><Size>7</Size><ETag>&quot;data&quot;</ETag><LastModified>2026-10-02T10:00:00Z</LastModified></Version>`, key, id, i == 0)
			} else {
				_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><LastModified>2026-10-02T10:01:00Z</LastModified></DeleteMarker>`, key, id, i == 0)
			}
		}
		_, _ = io.WriteString(w, `</ListVersionsResult>`)
		return
	}
	if r.Method != http.MethodDelete || !strings.HasSuffix(r.URL.Path, "/"+mutableDeleteKey) {
		t.Errorf("unexpected mutation %s %s", r.Method, r.URL)
		w.WriteHeader(500)
		return
	}
	p.deletes++
	if p.denied {
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
		return
	}
	if q.Get("versionId") == "null" {
		p.nullGone = true
		w.Header().Set("X-Amz-Version-Id", "null")
	} else if p.status == "Enabled" {
		id := "marker-new-" + strconv.Itoa(p.deletes)
		p.markers = append([]string{id}, p.markers...)
		w.Header().Set("X-Amz-Version-Id", id)
		w.Header().Set("X-Amz-Delete-Marker", "true")
	} else if p.status == "Suspended" {
		w.Header().Set("X-Amz-Version-Id", "null")
		w.Header().Set("X-Amz-Delete-Marker", "true")
	}
	if p.lost {
		p.lost = false
		conn, _, e := w.(http.Hijacker).Hijack()
		if e != nil {
			t.Fatal(e)
		}
		_ = conn.Close()
		return
	}
	w.WriteHeader(204)
}

// adr: 547
func TestMutableDeletionHistoryLimitE2E(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			var st multipartCopyIntegrationStore
			var pool *pgxpool.Pool
			now := time.Now().UTC()
			if pg {
				st, pool = multipartCopyPGStore(t)
			} else {
				m := state.NewMemStore()
				m.SetClockForTest(func() time.Time { return now })
				st = m
			}
			p := &mutableDeleteFixture{status: "Enabled", markers: []string{"m1", "m2", "m3", "m4", "m5", "m6", "m7", "m8", "m9"}, truncatedBaseline: true}
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
			prepareDeletionVersioning(t, st, f.bucket, "Enabled", func(duration time.Duration) {
				if pool == nil {
					now = now.Add(duration)
				} else if _, e := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second'`); e != nil {
					t.Fatal(e)
				}
			})
			_, e := f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(mutableDeleteKey)})
			var response *smithyhttp.ResponseError
			if !errors.As(e, &response) || response.Response == nil || response.HTTPStatusCode() != http.StatusServiceUnavailable || p.count() != 0 {
				t.Fatal("truncated baseline dispatched a mutation", e, p.count())
			}
			id := response.Response.Header.Get("X-Gregale-Delete-Id")
			j, e := st.(state.ObjectDeletionStore).GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, id)
			if e != nil || j.State != "failed" || j.LastErrorCode != "preparation_failed" || len(j.Baseline) != 0 {
				t.Fatal("incomplete baseline became recovery proof", j, e)
			}
			usage, e := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if e != nil || usage.Buckets[0].GrantedBytes != 0 || usage.Buckets[0].GrantedKeys != 0 {
				t.Fatal("preparation failure retained marker reservation", usage, e)
			}
			p.mu.Lock()
			p.truncatedBaseline = false
			p.mu.Unlock()
			if _, e = f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(mutableDeleteKey)}); e != nil || p.count() != 1 {
				t.Fatal("failed preparation retained the bucket fence", e, p.count())
			}
		})
	}
}

// A definite permission rejection must not permanently freeze the bucket.
func TestMutableDeletionRejectedE2E(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			var st multipartCopyIntegrationStore
			var pool *pgxpool.Pool
			now := time.Now().UTC()
			if pg {
				st, pool = multipartCopyPGStore(t)
			} else {
				m := state.NewMemStore()
				m.SetClockForTest(func() time.Time { return now })
				st = m
			}
			p := &mutableDeleteFixture{status: "Enabled", markers: []string{"marker-old"}, denied: true}
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
			prepareDeletionVersioning(t, st, f.bucket, "Enabled", func(duration time.Duration) {
				if pool == nil {
					now = now.Add(duration)
				} else if _, e := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second'`); e != nil {
					t.Fatal(e)
				}
			})
			id := uuid.NewString()
			request := func(key string, signed bool) *httptest.ResponseRecorder {
				r := httptest.NewRequest(http.MethodDelete, "http://"+f.handler.host+"/assets/"+url.PathEscape(key), nil)
				r.Header.Set("X-Amz-Content-Sha256", "UNSIGNED-PAYLOAD")
				if signed {
					r.Header.Set("X-Gregale-Delete-Id", id)
				}
				if e := awsv4.NewSigner(func(o *awsv4.SignerOptions) { o.DisableURIPathEscaping = true }).SignHTTP(t.Context(), aws.Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}, r, "UNSIGNED-PAYLOAD", "s3", "us-east-1", f.handler.now()); e != nil {
					t.Fatal(e)
				}
				if !signed {
					r.Header.Set("X-Gregale-Delete-Id", id)
				}
				w := httptest.NewRecorder()
				f.handler.ServeHTTP(w, r)
				return w
			}
			w := request(mutableDeleteKey, true)
			if w.Code != http.StatusServiceUnavailable || p.count() != 1 {
				t.Fatal(w.Code, w.Body.String(), p.count())
			}
			d := st.(state.ObjectDeletionStore)
			if pg {
				d = state.NewPgStore(pool)
			}
			j, e := d.GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, id)
			if e != nil || j.State != "failed" || j.LastErrorCode != "provider_rejected" {
				t.Fatal(j, e)
			}
			usage, e := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if e != nil || usage.Buckets[0].GrantedBytes != 0 || usage.Buckets[0].GrantedKeys != 0 {
				t.Fatal("rejection retained marker reservation", usage, e)
			}
			request(mutableDeleteKey, true)
			request(mutableDeleteKey+"-changed", true)
			if w = request(mutableDeleteKey, false); w.Code != http.StatusBadRequest || p.count() != 1 {
				t.Fatal("replay or unsigned identity reached provider", w.Code, p.count())
			}
			if pool == nil {
				// Native configuration uses an injected propagation clock, while
				// legacy write admission uses wall time. Refresh the verified
				// inventory at wall time before exercising that admission path.
				st.(*state.MemStore).SetClockForTest(time.Now)
				cap := st.(state.ObjectCapacityStore)
				c, err := cap.RequestObjectCapacityReconciliation(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
				if err != nil {
					t.Fatal("rejection retained inventory fence", err)
				}
				c, err = cap.ClaimObjectCapacityReconciliation(t.Context(), c.ID, "fresh-inventory")
				if err != nil {
					t.Fatal(err)
				}
				_, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(t.Context(), c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 7}, {Identity: strings.Repeat("b", 64), Bytes: int64(len(mutableDeleteKey))}})
				if err != nil {
					t.Fatal(err)
				}
			}
			_, e = st.(state.ObjectTrackedGatewayUploadStore).BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: f.bucket.AccountID, AppID: f.bucket.AppID, BucketID: f.bucket.ID, SubjectID: testAccess, Key: "new-write", Bytes: 1, Status: "pending"}, f.handler.registry.Accounting)
			if e != nil {
				t.Fatal("permission rejection retained write fence", e)
			}
		})
	}
}
func prepareDeletionVersioning(t *testing.T, st multipartCopyIntegrationStore, b state.ObjectBucket, status string, advance func(time.Duration)) {
	t.Helper()
	ctx := t.Context()
	v := st.(state.ObjectBucketVersioningStore)
	cap := st.(state.ObjectCapacityStore)
	inventory := st.(state.ObjectVersionInventoryStore)
	j, e := v.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, status)
	if e != nil {
		t.Fatal(j, e)
	}
	advance(api.ObjectBucketVersioningPropagation + time.Second)
	j, e = v.ClaimObjectBucketVersioning(ctx, b.ID, "configure")
	if e != nil {
		t.Fatal(j, e)
	}
	j, e = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, status)
	if e != nil {
		t.Fatal(j, e)
	}
	c, e := cap.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "inventory")
	if e != nil {
		t.Fatal(c, e)
	}
	_, e = inventory.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 7}, {Identity: strings.Repeat("b", 64), Bytes: int64(len(mutableDeleteKey))}})
	if e != nil {
		t.Fatal(e)
	}
	advance(api.ObjectBucketVersioningRetry + time.Second)
	j, e = v.ClaimObjectBucketVersioning(ctx, b.ID, "verify")
	if e != nil {
		t.Fatal(e)
	}
	j, e = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, status)
	if e != nil || j.State != "ready" {
		t.Fatal(j, e)
	}
}

// adr: 547
func TestMutableDeletionSDKE2E(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprint("postgres=", pg), func(t *testing.T) {
			var st multipartCopyIntegrationStore
			var pool *pgxpool.Pool
			now := time.Now().UTC()
			if pg {
				st, pool = multipartCopyPGStore(t)
			} else {
				m := state.NewMemStore()
				m.SetClockForTest(func() time.Time { return now })
				st = m
			}
			advance := func(duration time.Duration) {
				if pool == nil {
					now = now.Add(duration)
				} else {
					if _, e := pool.Exec(t.Context(), `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second';UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE now()-interval '1 second' END,retry_at=now() WHERE state IN ('prepared','dispatched')`); e != nil {
						t.Fatal(e)
					}
				}
			}
			p := &mutableDeleteFixture{status: "Enabled", markers: []string{"marker-old"}, lost: true, truncatedBaseline: true}
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
			prepareDeletionVersioning(t, st, f.bucket, "Enabled", advance)
			_, e := f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(mutableDeleteKey)})
			assertSDKErrorCode(t, e, "ServiceUnavailable")
			d := st.(state.ObjectDeletionStore)
			advance(api.ObjectDeletionRetry + time.Second)
			rows, e := d.DueObjectDeletions(t.Context(), api.ObjectDeletionBatch)
			if e != nil || len(rows) != 1 {
				t.Fatal(rows, e, "provider deletes", p.count())
			}
			id := rows[0].ID
			if p.count() != 1 {
				t.Fatal("uncertain attempt redispatched", p.count())
			}
			_, e = f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: aws.String("assets"), Key: aws.String("blocked"), Body: strings.NewReader("x")})
			assertSDKErrorCode(t, e, "ServiceUnavailable")
			if pg {
				st = state.NewPgStore(pool)
				d = st.(state.ObjectDeletionStore)
			}
			backend, e := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
			if e != nil {
				t.Fatal(e)
			}
			svc := objectstorage.DeletionService{Store: d, Provider: backend.Provider, BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}
			j, e := svc.Recover(t.Context(), f.bucket, id)
			if e != nil || j.State != "completed" || !j.DeleteMarker || !state.ValidObjectVersionID(j.VersionID) {
				t.Fatal(j, e)
			}
			native, e := st.(state.ObjectVersionReferenceStore).ResolveObjectVersion(t.Context(), f.bucket.AccountID, f.bucket.ID, mutableDeleteKey, j.VersionID)
			if e != nil || native != "marker-new-1" {
				t.Fatal(native, e)
			}
			j, e = svc.Start(t.Context(), f.bucket, mutableDeleteKey, "", id, f.handler.registry.Accounting)
			if e != nil || j.State != "completed" || p.count() != 1 {
				t.Fatal("receipt replay created a marker", j, e, p.count())
			}
			usage, e := st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
			if e != nil || usage.Buckets[0].GrantedBytes != int64(len(mutableDeleteKey)) || usage.Buckets[0].GrantedKeys != 1 {
				t.Fatal("marker quota", usage, e)
			}
			// Mutable null ack loss retains the fence even when provider truth shows gone.
			p.mu.Lock()
			p.lost = true
			p.mu.Unlock()
			nullID := uuid.NewString()
			j, e = svc.Start(t.Context(), f.bucket, mutableDeleteKey, "null", nullID, f.handler.registry.Accounting)
			if e == nil || j.State != "dispatched" || !p.nullDeleted() {
				t.Fatal(j, e)
			}
			advance(api.ObjectDeletionRetry + time.Second)
			j, e = svc.Recover(t.Context(), f.bucket, nullID)
			if e == nil || j.State != "dispatched" || p.count() != 2 {
				t.Fatal("absence settled null or repeated mutation", j, e, p.count())
			}
			_, e = st.(state.ObjectBucketVersioningStore).RequestObjectBucketVersioning(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, "Suspended")
			if e == nil {
				t.Fatal("pending null delete permitted configuration")
			}
		})
	}
}
func TestMutableDeletionSuspendedAndNullSDK(t *testing.T) {
	st := state.NewMemStore()
	now := time.Now().UTC()
	st.SetClockForTest(func() time.Time { return now })
	p := &mutableDeleteFixture{status: "Suspended", markers: []string{"marker-old"}}
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	prepareDeletionVersioning(t, st, f.bucket, "Suspended", func(duration time.Duration) { now = now.Add(duration) })
	out, e := f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(mutableDeleteKey)})
	if e != nil || !aws.ToBool(out.DeleteMarker) || aws.ToString(out.VersionId) != "null" {
		t.Fatal(out, e)
	}
	out, e = f.client.DeleteObject(t.Context(), &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(mutableDeleteKey), VersionId: aws.String("null")})
	if e != nil || aws.ToString(out.VersionId) != "null" {
		t.Fatal(out, e)
	}
	bulk, e := f.client.DeleteObjects(t.Context(), &awss3.DeleteObjectsInput{Bucket: aws.String("assets"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{{Key: aws.String(mutableDeleteKey)}, {Key: aws.String(mutableDeleteKey), VersionId: aws.String("null")}}}})
	if e != nil || len(bulk.Errors) != 0 || len(bulk.Deleted) != 2 || p.count() != 4 {
		t.Fatal(bulk, e, p.count())
	}
	response, ok := awsmiddleware.GetRawResponse(bulk.ResultMetadata).(*smithyhttp.Response)
	if !ok || response == nil {
		t.Fatal("missing bulk response")
	}
	root, e := uuid.Parse(response.Header.Get("X-Gregale-Delete-Id"))
	if e != nil {
		t.Fatal("missing bulk retry identity", e)
	}
	for index := range 2 {
		id := uuid.NewSHA1(root, []byte("bulk-entry:"+strconv.Itoa(index))).String()
		j, e := st.GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, id)
		if e != nil || j.State != "completed" || j.VersionID != "null" {
			t.Fatal("bulk receipt is not discoverable", j, e)
		}
	}
}
