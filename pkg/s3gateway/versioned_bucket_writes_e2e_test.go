package s3gateway

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
)

// Once a customer enables versioning and the native inventory completes, the
// bucket keeps taking writes and deletes through the gateway (ADR-540).
func TestVersionedBucketTakesWritesAndDeletesMem(t *testing.T) {
	st := state.NewMemStore()
	// Gateway egress checks read the wall clock, so the store clock ends
	// near it after the propagation and retry waits below.
	now := time.Now().UTC().Add(-api.ObjectBucketVersioningPropagation - api.ObjectBucketVersioningRetry - 2*time.Second)
	st.SetClockForTest(func() time.Time { return now })
	var mu sync.Mutex
	status, writes, deleted, unexpected := "", 0, false, []string{}
	f := newMultipartCopyIntegrationWithTransfer(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		q := r.URL.Query()
		switch {
		case q.Has("versioning") && r.Method == http.MethodPut:
			body, _ := io.ReadAll(r.Body)
			status = "Suspended"
			if strings.Contains(string(body), "Enabled") {
				status = "Enabled"
			}
		case q.Has("versioning"):
			w.Header().Set("Content-Type", "application/xml")
			if status == "" {
				_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
				return
			}
			_, _ = fmt.Fprintf(w, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, status)
		case r.Method == http.MethodPut:
			writes++
			_, _ = io.Copy(io.Discard, r.Body)
			w.Header().Set("ETag", fmt.Sprintf(`"etag-%d"`, writes))
			w.Header().Set("X-Amz-Version-Id", fmt.Sprintf("native-%d", writes))
		case q.Has("versions"):
			w.Header().Set("Content-Type", "application/xml")
			_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>false</IsTruncated>`)
			if deleted {
				_, _ = io.WriteString(w, `<DeleteMarker><Key>key</Key><VersionId>marker-1</VersionId><IsLatest>true</IsLatest><LastModified>2026-11-03T00:00:00.000Z</LastModified></DeleteMarker>`)
			}
			for i := writes; i >= 1; i-- {
				_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>native-%d</VersionId><IsLatest>%v</IsLatest><ETag>"etag-%d"</ETag><Size>5</Size><LastModified>2026-11-03T00:00:00.000Z</LastModified></Version>`, i, i == writes && !deleted, i)
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
		case r.Method == http.MethodDelete:
			deleted = true
			w.Header().Set("X-Amz-Delete-Marker", "true")
			w.Header().Set("X-Amz-Version-Id", "marker-1")
			w.WriteHeader(http.StatusNoContent)
		default:
			unexpected = append(unexpected, r.Method+" "+r.URL.String())
			w.WriteHeader(http.StatusNotImplemented)
		}
	}), objectstorage.Config{}, 0)
	ctx := t.Context()
	in := &awss3.PutBucketVersioningInput{Bucket: aws.String("assets"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}
	if _, err := f.client.PutBucketVersioning(ctx, in); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.ObjectBucketVersioningPropagation + time.Second)
	backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
	if err != nil {
		t.Fatal(err)
	}
	svc := objectstorage.BucketVersioningService{Store: st, Provider: backend.Provider.(objectstorage.BucketVersioningProvider), BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}
	j, err := svc.Reconcile(ctx, f.bucket)
	if err != nil || j.State != "inventory" {
		t.Fatal(j, err)
	}
	c, err := st.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, "scan")
	if err != nil {
		t.Fatal(c, err)
	}
	if _, err = st.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", nil); err != nil {
		t.Fatal(err)
	}
	now = now.Add(api.ObjectBucketVersioningRetry + time.Second)
	if j, err = svc.Reconcile(ctx, f.bucket); err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
	// Usage reports carry wall-clock times; a store clock behind them reads
	// them as future reports.
	now = time.Now().UTC().Add(time.Second)
	put := func(body string) error {
		_, err := f.client.PutObject(ctx, &awss3.PutObjectInput{Bucket: in.Bucket, Key: aws.String("key"), Body: strings.NewReader(body)})
		return err
	}
	if err = put("first"); err != nil {
		t.Fatal("first write to a versioned bucket", err)
	}
	if err = put("second"); err != nil {
		t.Fatal("overwrite in a versioned bucket", err)
	}
	if _, err = f.client.DeleteObject(ctx, &awss3.DeleteObjectInput{Bucket: in.Bucket, Key: aws.String("key")}); err != nil {
		t.Fatal("delete in a versioned bucket", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(unexpected) > 0 || writes != 2 || !deleted {
		t.Fatal("provider dispatch", unexpected, writes, deleted)
	}
}
