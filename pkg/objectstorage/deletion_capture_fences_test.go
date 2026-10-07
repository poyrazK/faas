// adr: 590
package objectstorage

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestDeletionCaptureAdmissionBeforeProviderHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			ctx := t.Context()
			fences := f.st.(state.ObjectBucketWriteFenceStore)
			refs, err := f.st.RecordObjectVersions(ctx, f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-original"}})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fences.AcquireObjectBucketWriteFence(ctx, f.bucket, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(http.StatusNoContent)
			})).(Provider)
			for _, selector := range []string{"", "null", refs[0].ID} {
				j, err := (DeletionService{Store: f.st, Provider: provider}).Start(ctx, f.bucket, "key", selector, uuid.NewString(), f.policy)
				if !errors.Is(err, state.ErrObjectBucketWriteFenced) || j.ID != "" || calls.Load() != 0 {
					t.Fatalf("held source admitted selector %q: %+v %v calls=%d", selector, j, err, calls.Load())
				}
			}
		})
	}
}

func TestLifecycleCaptureHoldAfterDiscoveryHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			ctx := t.Context()
			rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}}
			if _, err := f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, rules); err != nil {
				t.Fatal(err)
			}
			fences := f.st.(state.ObjectBucketWriteFenceStore)
			token := uuid.NewString()
			var held, deleted atomic.Bool
			var deletes atomic.Int32
			modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
			provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/xml")
				q := r.URL.Query()
				switch {
				case q.Has("versions"):
					if q.Get("prefix") == "" && !held.Swap(true) {
						if _, err := fences.AcquireObjectBucketWriteFence(ctx, f.bucket, token); err != nil {
							t.Error(err)
							w.WriteHeader(500)
							return
						}
					}
					_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
					if !deleted.Load() {
						_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>null</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version>`, modified.Format(time.RFC3339Nano))
					}
					_, _ = io.WriteString(w, `</ListVersionsResult>`)
				case q.Has("versioning"):
					_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
				case q.Has("tagging"):
					_, _ = io.WriteString(w, `<Tagging><TagSet/></Tagging>`)
				case r.Method == http.MethodDelete:
					deletes.Add(1)
					deleted.Store(true)
					w.WriteHeader(http.StatusNoContent)
				default:
					t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})).(Provider)
			svc := LifecycleExpirationService{Store: f.st, Provider: provider}
			j, err := svc.Step(ctx, f.bucket, f.policy)
			if !errors.Is(err, state.ErrObjectBucketWriteFenced) || deletes.Load() != 0 || j.LastKey != "" || j.ScannedKeys != 0 {
				t.Fatalf("discovery bypassed new capture hold: %+v %v deletes=%d", j, err, deletes.Load())
			}
			fence, err := fences.ReadObjectBucketWriteFence(ctx, f.bucket, token)
			if err != nil || fence.Deletions != 0 {
				t.Fatalf("blocked lifecycle admitted a durable intent: %+v %v", fence, err)
			}
			if err := fences.ReleaseObjectBucketWriteFence(ctx, f.bucket, token); err != nil {
				t.Fatal(err)
			}
			lifecycleWorkerDue(t, f)
			svc.Store = f.reopen()
			j, err = svc.Step(ctx, f.bucket, f.policy)
			if err != nil || deletes.Load() != 1 || j.LastKey != "key" {
				t.Fatalf("released hold did not resume lifecycle: %+v %v deletes=%d", j, err, deletes.Load())
			}
		})
	}
}

func TestDeletionCaptureRetainsOriginalRecoveryHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, outcome := range []string{"immutable acknowledgment", "immutable rejection", "mutable absence", "mutable marker"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, outcome), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				ctx := t.Context()
				selector := ""
				if strings.HasPrefix(outcome, "immutable") {
					refs, err := f.st.RecordObjectVersions(ctx, f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-original"}})
					if err != nil {
						t.Fatal(err)
					}
					selector = refs[0].ID
				}
				if outcome == "mutable marker" {
					f.enableVersioning(t)
				}
				fences := f.st.(state.ObjectBucketWriteFenceStore)
				token := uuid.NewString()
				var deletes atomic.Int32
				modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
				provider := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/xml")
					q := r.URL.Query()
					switch {
					case q.Has("versioning"):
						_, _ = io.WriteString(w, `<VersioningConfiguration>`)
						if outcome == "mutable marker" {
							_, _ = io.WriteString(w, `<Status>Enabled</Status>`)
						}
						_, _ = io.WriteString(w, `</VersioningConfiguration>`)
					case q.Has("versions"):
						_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
						if deletes.Load() != 0 {
							_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>key</Key><VersionId>private-marker</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified></DeleteMarker>`, modified.Add(time.Second).Format(time.RFC3339Nano))
						}
						_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-original</VersionId><IsLatest>%t</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version></ListVersionsResult>`, deletes.Load() == 0, modified.Format(time.RFC3339Nano))
					case r.Method == http.MethodDelete:
						if deletes.Add(1) == 1 {
							fence, err := fences.AcquireObjectBucketWriteFence(ctx, f.bucket, token)
							if err != nil || fence.Deletions != 1 {
								t.Errorf("dispatched deletion missing during provider IO: %+v %v", fence, err)
							}
							conn, _, err := w.(http.Hijacker).Hijack()
							if err != nil {
								t.Error(err)
								return
							}
							_ = conn.Close() // Original mutation may have succeeded.
							return
						}
						if outcome == "immutable rejection" {
							w.WriteHeader(http.StatusForbidden)
							_, _ = io.WriteString(w, `<Error><Code>AccessDenied</Code></Error>`)
							return
						}
						w.Header().Set("X-Amz-Version-Id", "private-original")
						w.WriteHeader(http.StatusNoContent)
					default:
						t.Errorf("unexpected provider request: %s %s", r.Method, r.URL.Path)
						w.WriteHeader(500)
					}
				})).(Provider)
				svc := DeletionService{Store: f.st, Provider: provider}
				j, err := svc.Start(ctx, f.bucket, "key", selector, uuid.NewString(), f.policy)
				if err == nil || j.State != "dispatched" || deletes.Load() != 1 {
					t.Fatalf("lost acknowledgment fixture: %+v %v", j, err)
				}
				f.expire(j.ID)
				fence, err := fences.ReadObjectBucketWriteFence(ctx, f.bucket, token)
				if err != nil || fence.Deletions != 1 {
					t.Fatalf("expiry erased original provider outcome: %+v %v", fence, err)
				}
				svc.Store = f.reopen()
				j, err = svc.Recover(ctx, f.bucket, j.ID)
				wantBusy := outcome == "immutable rejection" || outcome == "mutable absence"
				if wantBusy && (err == nil || j.State != "dispatched") || !wantBusy && (err != nil || j.State != "completed") {
					t.Fatalf("original recovery outcome: %+v %v", j, err)
				}
				fence, err = fences.ReadObjectBucketWriteFence(ctx, f.bucket, token)
				want := int64(0)
				if wantBusy {
					want = 1
				}
				if err != nil || fence.Deletions != want {
					t.Fatalf("capture drainage did not preserve original outcome: %+v %v", fence, err)
				}
				if strings.HasPrefix(outcome, "mutable") && deletes.Load() != 1 {
					t.Fatalf("recovery reissued mutable deletion: %d", deletes.Load())
				}
			})
		}
	}
}
