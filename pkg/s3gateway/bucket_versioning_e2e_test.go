package s3gateway

import (
	"encoding/xml"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/state"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type bucketVersioningHTTPFixture struct {
	mu     sync.Mutex
	status string
	puts   int
	lost   bool
}

func (p *bucketVersioningHTTPFixture) serve(t *testing.T, w http.ResponseWriter, r *http.Request) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	w.Header().Set("Content-Type", "application/xml")
	if !r.URL.Query().Has("versioning") {
		t.Errorf("unexpected dispatch %s %s", r.Method, r.URL)
		w.WriteHeader(500)
		return
	}
	if r.Method == http.MethodPut {
		var in struct{ Status string }
		if xml.NewDecoder(r.Body).Decode(&in) != nil {
			t.Error("invalid upstream XML")
		}
		p.status = in.Status
		p.puts++
		if p.lost {
			p.lost = false
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
		}
		return
	}
	_, _ = fmt.Fprintf(w, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, p.status)
}
func TestBucketVersioningSDKEndToEndMem(t *testing.T) {
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprint("lost=", lost), func(t *testing.T) {
			st := state.NewMemStore()
			now := time.Now().UTC()
			st.SetClockForTest(func() time.Time { return now })
			p := &bucketVersioningHTTPFixture{lost: lost}
			f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				p.mu.Lock()
				empty := p.status == "" && r.Method == http.MethodGet
				p.mu.Unlock()
				if empty {
					_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
					return
				}
				p.serve(t, w, r)
			}))
			in := &awss3.PutBucketVersioningInput{Bucket: aws.String("assets"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}
			_, err := f.client.PutBucketVersioning(t.Context(), in)
			if lost {
				assertSDKErrorCode(t, err, "ServiceUnavailable")
			} else if err != nil {
				t.Fatal(err)
			}
			now = now.Add(api.ObjectBucketVersioningRetry + time.Second)
			_, err = f.client.PutBucketVersioning(t.Context(), in)
			if err != nil {
				t.Fatal("configuration recovery", err)
			}
			p.mu.Lock()
			puts := p.puts
			p.mu.Unlock()
			if puts != 1 {
				t.Fatal("lost acknowledgment duplicated provider PUT", puts)
			}
			out, err := f.client.GetBucketVersioning(t.Context(), &awss3.GetBucketVersioningInput{Bucket: in.Bucket})
			if err != nil || out.Status != types.BucketVersioningStatusEnabled {
				t.Fatal(out, err)
			}
			_, err = f.client.PutObject(t.Context(), &awss3.PutObjectInput{Bucket: in.Bucket, Key: aws.String("blocked"), Body: strings.NewReader("x")})
			assertSDKErrorCode(t, err, "ServiceUnavailable")
			j, err := st.GetObjectBucketVersioning(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || j.State != "propagating" {
				t.Fatal(j, err)
			}
			now = now.Add(api.ObjectBucketVersioningPropagation + time.Second)
			backend, err := f.handler.registry.Resolve(f.bucket.BackendID, f.bucket.BackendFingerprint)
			if err != nil {
				t.Fatal(err)
			}
			svc := objectstorage.BucketVersioningService{Store: st, Provider: backend.Provider.(objectstorage.BucketVersioningProvider), BeforeRequest: objectstorage.VersioningRequestRecorder(st, f.bucket.ID)}
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "inventory" {
				t.Fatal(j, err)
			}
			c, err := st.ClaimObjectCapacityReconciliation(t.Context(), j.CapacityJobID, "scan")
			if err != nil {
				t.Fatal(c, err)
			}
			if _, err = st.StageObjectVersionInventoryPage(t.Context(), c.ID, c.Token, "", []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 7}}); err != nil {
				t.Fatal(err)
			}
			now = now.Add(api.ObjectBucketVersioningRetry + time.Second)
			j, err = svc.Reconcile(t.Context(), f.bucket)
			if err != nil || j.State != "ready" {
				t.Fatal(j, err)
			}
			in.VersioningConfiguration.Status = types.BucketVersioningStatusSuspended
			if _, err = f.client.PutBucketVersioning(t.Context(), in); err != nil {
				t.Fatal(err)
			}
			out, err = f.client.GetBucketVersioning(t.Context(), &awss3.GetBucketVersioningInput{Bucket: in.Bucket})
			if err != nil || out.Status != types.BucketVersioningStatusSuspended {
				t.Fatal(out, err)
			}
			j, err = st.GetObjectBucketVersioning(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
			if err != nil || !j.VersionsRequired || j.Revision != 2 {
				t.Fatal(j, err)
			}
		})
	}
}
func TestBucketVersioningSDKPermissionAndUnsupportedMFA(t *testing.T) {
	st := state.NewMemStore()
	var calls int
	f := newMultipartCopyIntegrationWithProvider(t, st, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
	}), state.ObjectBucketPermissionRead)
	in := &awss3.PutBucketVersioningInput{Bucket: aws.String("assets"), VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}
	_, err := f.client.PutBucketVersioning(t.Context(), in)
	assertSDKErrorCode(t, err, "AccessDenied")
	if calls != 0 {
		t.Fatal("read credential dispatched change")
	}
	f = newMultipartCopyIntegrationWithProvider(t, state.NewMemStore(), http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
	}))
	in.VersioningConfiguration.MFADelete = types.MFADeleteDisabled
	_, err = f.client.PutBucketVersioning(t.Context(), in)
	assertSDKErrorCode(t, err, "NotImplemented")
	if calls != 0 {
		t.Fatal("unsupported MFA directive dispatched")
	}
}
