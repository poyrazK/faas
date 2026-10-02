package s3gateway

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

type versionDeleteHTTPFixture struct {
	mu             sync.Mutex
	gone           map[string]bool
	calls, deletes int
	lose           bool
	denied         bool
	paginated      bool
}

func (p *versionDeleteHTTPFixture) count() int { p.mu.Lock(); defer p.mu.Unlock(); return p.deletes }
func (p *versionDeleteHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	q := r.URL.Query()
	if r.Header.Get("Authorization") == "" && q.Get("X-Amz-Signature") == "" {
		t.Error("unsigned upstream request")
	}
	if q.Has("versions") {
		w.Header().Set("Content-Type", "application/xml")
		ids := []string{}
		for _, id := range []string{publicVersionNativeNew, publicVersionNativeOld, "private-marker"} {
			if !p.gone[id] {
				ids = append(ids, id)
			}
		}
		start := 0
		if marker := q.Get("version-id-marker"); marker != "" {
			found := false
			for i, id := range ids {
				if marker == id {
					start = i + 1
					found = true
					break
				}
			}
			if !found {
				t.Error("deleted inventory continuation", marker)
				w.WriteHeader(500)
				return
			}
		}
		end := len(ids)
		if p.paginated {
			end = min(end, start+1)
		}
		next := end < len(ids)
		_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>%t</IsTruncated>`, next)
		if next {
			_, _ = fmt.Fprintf(w, `<NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>%s</NextVersionIdMarker>`, url.PathEscape(publicVersionTestKey), ids[end-1])
		}
		for _, id := range ids[start:end] {
			latest := id == "private-marker" || p.gone["private-marker"] && id == publicVersionNativeNew || p.gone["private-marker"] && p.gone[publicVersionNativeNew] && id == publicVersionNativeOld
			if id == "private-marker" {
				_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><LastModified>2026-10-02T10:00:00Z</LastModified></DeleteMarker>`, url.PathEscape(publicVersionTestKey), id, latest)
			} else {
				_, _ = fmt.Fprintf(w, `<Version><Key>%s</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><Size>8</Size><ETag>&quot;etag&quot;</ETag><LastModified>2026-10-02T09:00:00Z</LastModified></Version>`, url.PathEscape(publicVersionTestKey), id, latest)
			}
		}
		_, _ = io.WriteString(w, `</ListVersionsResult>`)
		return
	}
	id := q.Get("versionId")
	if r.Method == http.MethodDelete {
		p.deletes++
		if p.denied {
			w.WriteHeader(403)
			_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
			return
		}
		if id == "" || id == "null" || strings.TrimPrefix(r.URL.Path, "/physical/") != publicVersionTestKey {
			t.Error("unsafe deletion selector", r.URL)
			w.WriteHeader(500)
			return
		}
		marker := id == "private-marker" && !p.gone[id]
		p.gone[id] = true
		if p.lose {
			p.lose = false
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("X-Amz-Version-Id", id)
		w.Header().Set("X-Amz-Delete-Marker", strconv.FormatBool(marker))
		w.WriteHeader(204)
		return
	}
	if id == "" {
		for _, candidate := range []string{"private-marker", publicVersionNativeNew, publicVersionNativeOld} {
			if !p.gone[candidate] {
				id = candidate
				break
			}
		}
	}
	if id == "" || p.gone[id] {
		w.WriteHeader(404)
		_, _ = io.WriteString(w, `<Error><Code>NoSuchVersion</Code><Message>provider private identity</Message></Error>`)
		return
	}
	w.Header().Set("X-Amz-Version-Id", id)
	if id == "private-marker" {
		w.Header().Set("X-Amz-Delete-Marker", "true")
		if q.Has("versionId") {
			w.WriteHeader(405)
		} else {
			w.WriteHeader(404)
		}
		return
	}
	body := "new-body"
	if id == publicVersionNativeOld {
		body = "old-body"
	}
	w.Header().Set("ETag", `"etag"`)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	if r.Method == http.MethodGet {
		_, _ = io.WriteString(w, body)
	}
}

// adr: 404
func TestVersionDeletionEndToEndMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now().UTC()
	m.SetClockForTest(func() time.Time { return now })
	versionDeletionEndToEnd(t, m, nil, func() { now = now.Add(api.ObjectDeletionRetry + time.Second) })
}
func TestVersionDeletionEndToEndPG(t *testing.T) {
	st, pool := multipartCopyPGStore(t)
	versionDeletionEndToEnd(t, st, func() multipartCopyIntegrationStore { return state.NewPgStore(pool) }, func() {
		if _, e := pool.Exec(t.Context(), `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE now()-interval '1 second' END,retry_at=now() WHERE state IN ('prepared','dispatched')`); e != nil {
			t.Fatal(e)
		}
	})
}
func versionDeletionEndToEnd(t *testing.T, st multipartCopyIntegrationStore, restart func() multipartCopyIntegrationStore, advance func()) {
	p := &versionDeleteHTTPFixture{gone: map[string]bool{}}
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }))
	ctx := t.Context()
	listed, err := f.client.ListObjectVersions(ctx, &awss3.ListObjectVersionsInput{Bucket: aws.String("assets")})
	if err != nil || len(listed.Versions) != 2 || len(listed.DeleteMarkers) != 1 {
		t.Fatal(listed, err)
	}
	newID, oldID, marker := aws.ToString(listed.Versions[0].VersionId), aws.ToString(listed.Versions[1].VersionId), aws.ToString(listed.DeleteMarkers[0].VersionId)
	versionDeleteInventory(t, f)
	before, err := st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
	if err != nil || before.Buckets[0].BaselineBytes != 16+int64(len(publicVersionTestKey)) {
		t.Fatal(before, err)
	}
	out, err := f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID)})
	if err != nil || aws.ToString(out.VersionId) != oldID || aws.ToBool(out.DeleteMarker) || p.count() != 1 {
		t.Fatal(out, err, p.count())
	}
	_, err = f.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID)})
	assertSDKErrorCode(t, err, "NoSuchVersion")
	batch, err := f.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{Bucket: aws.String("assets"), Delete: &types.Delete{Objects: []types.ObjectIdentifier{
		{Key: aws.String(publicVersionTestKey), VersionId: aws.String(marker)},
		{Key: aws.String(publicVersionTestKey), VersionId: aws.String(oldID)},
		{Key: aws.String("wrong-key"), VersionId: aws.String(newID)},
		{Key: aws.String(publicVersionTestKey), VersionId: aws.String(uuid.NewString())},
		{Key: aws.String(publicVersionTestKey), VersionId: aws.String("null")},
		{Key: aws.String(publicVersionTestKey)},
	}}})
	if err != nil || len(batch.Deleted) != 2 || len(batch.Errors) != 4 || !aws.ToBool(batch.Deleted[0].DeleteMarker) || aws.ToString(batch.Deleted[0].VersionId) != marker || p.count() != 3 {
		t.Fatal(batch, err, p.count())
	}
	for i, want := range []string{"NoSuchVersion", "NoSuchVersion", "ServiceUnavailable", "ServiceUnavailable"} {
		if aws.ToString(batch.Errors[i].Code) != want {
			t.Fatal(batch.Errors)
		}
	}
	get, err := f.client.GetObject(ctx, &awss3.GetObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey)})
	if err != nil {
		t.Fatal("marker deletion did not reveal older data", err)
	}
	body, _ := io.ReadAll(get.Body)
	_ = get.Body.Close()
	if string(body) != "new-body" || aws.ToString(get.VersionId) != newID {
		t.Fatal(string(body), get)
	}
	p.mu.Lock()
	p.lose = true
	p.mu.Unlock()
	_, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(newID)})
	assertSDKErrorCode(t, err, "ServiceUnavailable")
	if p.count() != 4 {
		t.Fatal("provider retried uncertain delete", p.count())
	}
	if restart != nil {
		st = restart()
		f.store = st
		f.handler.store = st
		f.handler.requestMetrics = st
	}
	advance()
	rows, e := st.(state.ObjectDeletionStore).DueObjectDeletions(ctx, api.ObjectDeletionBatch)
	if e != nil || len(rows) != 1 {
		t.Fatal(rows, e)
	}
	providerForRecovery, e := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if e != nil {
		t.Fatal(e)
	}
	recovered, e := (objectstorage.DeletionService{Store: st.(state.ObjectDeletionStore), Provider: providerForRecovery.Provider, BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}).Recover(ctx, f.bucket, rows[0].ID)
	if e != nil || recovered.State != "completed" || recovered.VersionID != newID || p.count() != 5 {
		t.Fatal("immutable restart recovery", recovered, e, p.count())
	}
	out, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(newID)})
	if err != nil || aws.ToString(out.VersionId) != newID || p.count() != 6 {
		t.Fatal("immutable restart retry", out, err, p.count())
	}
	refs := st.(state.ObjectVersionReferenceStore)
	if native, err := refs.ResolveObjectVersion(ctx, f.bucket.AccountID, f.bucket.ID, publicVersionTestKey, newID); err != nil || native != publicVersionNativeNew {
		t.Fatal("delete removed durable retry identity", native, err)
	}
	wrongOwner := f.bucket
	wrongOwner.AccountID = uuid.NewString()
	provider, _ := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if _, err = objectstorage.DeleteOwnedObjectVersion(ctx, refs, provider.Provider, wrongOwner, publicVersionTestKey, newID, nil); err == nil || p.count() != 6 {
		t.Fatal("tenant selector dispatched", err, p.count())
	}
	after, err := st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
	if err != nil || after.Buckets[0].BaselineBytes != before.Buckets[0].BaselineBytes || after.Buckets[0].GrantedBytes != before.Buckets[0].GrantedBytes {
		t.Fatal("DELETE prematurely refunded quota", after, err)
	}
	quiet, err := f.client.DeleteObjects(ctx, &awss3.DeleteObjectsInput{Bucket: aws.String("assets"), Delete: &types.Delete{Quiet: aws.Bool(true), Objects: []types.ObjectIdentifier{{Key: aws.String(publicVersionTestKey), VersionId: aws.String(newID)}, {Key: aws.String("wrong-key"), VersionId: aws.String(newID)}}}})
	if err != nil || len(quiet.Deleted) != 0 || len(quiet.Errors) != 1 {
		t.Fatal(quiet, err)
	}
	versionDeleteInventory(t, f)
	after, err = st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
	if err != nil || after.Buckets[0].BaselineBytes != 0 || after.Buckets[0].BaselineKeys != 0 {
		t.Fatal("verified inventory did not reclaim capacity", after, err)
	}
}
func versionDeleteInventory(t *testing.T, f *multipartCopyIntegration) {
	t.Helper()
	ctx := t.Context()
	cap := f.store.(state.ObjectCapacityStore)
	job, err := cap.RequestObjectCapacityReconciliation(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
	if err != nil {
		t.Fatal(err)
	}
	job, err = cap.ClaimObjectCapacityReconciliation(ctx, job.ID, uuid.NewString())
	if err != nil || job.InventoryScope != state.ObjectInventoryAllVersions {
		t.Fatal(job, err)
	}
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	page, err := backend.Provider.(objectstorage.ObjectVersionInventoryProvider).ListObjectVersions(ctx, f.bucket.PhysicalName, "", api.ObjectVersionInventoryPageSize)
	if err != nil || page.NextCursor != "" {
		t.Fatal(page, err)
	}
	records := make([]state.ObjectVersionInventoryRecord, 0, len(page.Items))
	for _, v := range page.Items {
		hash := sha256.Sum256([]byte(v.Key + "\x00" + v.ProviderVersionID))
		records = append(records, state.ObjectVersionInventoryRecord{Identity: hex.EncodeToString(hash[:]), Bytes: v.SizeBytes})
	}
	job, err = f.store.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, job.ID, job.Token, "", records)
	if err != nil || job.State != "completed" || !job.InventoryVerified {
		t.Fatal(job, err)
	}
}
func TestVersionDeletionPermissionsAndConditions(t *testing.T) {
	for _, permission := range []string{state.ObjectBucketPermissionRead, state.ObjectBucketPermissionWrite} {
		t.Run(permission, func(t *testing.T) {
			p := &versionDeleteHTTPFixture{gone: map[string]bool{}}
			st := state.NewMemStore()
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { p.serve(t, w, r) }), permission)
			refs, err := st.RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: publicVersionTestKey, ProviderVersionID: publicVersionNativeOld}})
			if err != nil {
				t.Fatal(err)
			}
			in := &awss3.DeleteObjectInput{Bucket: aws.String("assets"), Key: aws.String(publicVersionTestKey), VersionId: aws.String(refs[0].ID)}
			_, err = f.client.DeleteObject(t.Context(), in)
			if permission == state.ObjectBucketPermissionRead {
				assertSDKErrorCode(t, err, "AccessDenied")
				if p.count() != 0 {
					t.Fatal(p.count())
				}
				return
			}
			if err != nil || p.count() != 1 {
				t.Fatal(err, p.count())
			}
			in.IfMatch = aws.String(`"etag"`)
			_, err = f.client.DeleteObject(t.Context(), in)
			assertSDKErrorCode(t, err, "NotImplemented")
			if p.count() != 1 {
				t.Fatal("ignored delete predicate", p.count())
			}
		})
	}
}
