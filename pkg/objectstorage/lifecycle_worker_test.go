package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type lifecycleCheckpointFailureStore struct {
	LifecycleExpirationStore
	fail atomic.Bool
}

func (s *lifecycleCheckpointFailureStore) CheckpointObjectLifecycleScan(ctx context.Context, id, token, key string, done bool) (state.ObjectLifecycleScan, error) {
	if s.fail.Swap(false) {
		return state.ObjectLifecycleScan{}, ErrUnavailable
	}
	return s.LifecycleExpirationStore.CheckpointObjectLifecycleScan(ctx, id, token, key, done)
}

func lifecycleWorkerDue(t *testing.T, f lifecycleServiceFixture) {
	t.Helper()
	if f.pool != nil {
		if _, err := f.pool.Exec(t.Context(), `UPDATE object_lifecycle_scans SET retry_at=clock_timestamp() WHERE bucket_id=$1`, f.bucket.ID); err != nil {
			t.Fatal(err)
		}
	} else {
		f.expire("")
	}
}

// adr: 408
func TestLifecycleWorkerHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"restart", "lost checkpoint", "lost acknowledgment", "page failure", "competing workers", "overlapping tags"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, scenario), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				ctx := t.Context()
				modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
				rules := []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}}
				if scenario == "overlapping tags" {
					rules[0].Filter.Tags = map[string]string{"missing": ""}
					rules = append(rules, api.ObjectLifecycleRule{ID: "matched", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Tags: map[string]string{"ttl": ""}}, Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}})
				}
				if _, err := f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, rules); err != nil {
					t.Fatal(err)
				}
				var requests, deletes atomic.Int32
				var first atomic.Bool
				started, release := make(chan struct{}), make(chan struct{})
				var releaseOnce sync.Once
				var mu sync.Mutex
				objects := map[string]bool{"a": true}
				if scenario == "restart" {
					objects["b"] = true
				}
				p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests.Add(1)
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/xml")
					if q.Has("versioning") {
						_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
						return
					}
					if q.Has("tagging") {
						if q.Get("versionId") != "" {
							t.Error("unversioned tags used a mutable null selector", q)
						}
						_, _ = io.WriteString(w, `<Tagging><TagSet><Tag><Key>ttl</Key><Value></Value></Tag></TagSet></Tagging>`)
						return
					}
					if q.Has("versions") {
						discovery := q.Get("prefix") == ""
						if discovery && (q.Get("version-id-marker") != "" || q.Get("max-keys") != "1") {
							t.Error("discovery retained a version continuation", q)
							w.WriteHeader(500)
							return
						}
						if discovery && !first.Swap(true) {
							if scenario == "page failure" {
								w.WriteHeader(500)
								return
							}
							if scenario == "competing workers" {
								close(started)
								<-release
							}
						}
						mu.Lock()
						keys := []string{}
						for key, present := range objects {
							if present && strings.HasPrefix(key, q.Get("prefix")) && key > q.Get("key-marker") {
								keys = append(keys, key)
							}
						}
						mu.Unlock()
						sort.Strings(keys)
						truncated := discovery && len(keys) > 1
						if truncated {
							keys = keys[:1]
						}
						_, _ = fmt.Fprintf(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>%t</IsTruncated>`, truncated)
						if truncated {
							_, _ = fmt.Fprintf(w, `<NextKeyMarker>%s</NextKeyMarker><NextVersionIdMarker>null</NextVersionIdMarker>`, keys[0])
						}
						for _, key := range keys {
							_, _ = fmt.Fprintf(w, `<Version><Key>%s</Key><VersionId>null</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version>`, key, modified.Format(time.RFC3339Nano))
						}
						_, _ = io.WriteString(w, `</ListVersionsResult>`)
						return
					}
					if r.Method != http.MethodDelete || q.Get("versionId") != "" {
						t.Error("wrong lifecycle mutation", r.Method, q)
						w.WriteHeader(500)
						return
					}
					key := strings.TrimPrefix(r.URL.Path, "/test/")
					// historyTestProvider uses its own physical path; the object
					// key is the last path segment in these fixtures.
					key = key[strings.LastIndex(key, "/")+1:]
					mu.Lock()
					if scenario != "lost checkpoint" {
						delete(objects, key)
					}
					mu.Unlock()
					deletes.Add(1)
					if scenario == "lost acknowledgment" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.WriteHeader(http.StatusNoContent)
				})).(Provider)
				t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
				svc := LifecycleExpirationService{Store: f.st, Provider: p}
				if scenario == "lost checkpoint" {
					wrapped := &lifecycleCheckpointFailureStore{LifecycleExpirationStore: f.st}
					wrapped.fail.Store(true)
					svc.Store = wrapped
				}
				var j state.ObjectLifecycleScan
				var err error
				if scenario == "competing workers" {
					type result struct {
						j   state.ObjectLifecycleScan
						err error
					}
					finished := make(chan result, 1)
					go func() { scan, err := svc.Step(ctx, f.bucket, f.policy); finished <- result{scan, err} }()
					select {
					case <-started:
					case <-time.After(api.ObjectLifecycleFinishTimeout):
						t.Fatal("worker did not enter discovery")
					}
					second := LifecycleExpirationService{Store: f.reopen(), Provider: p}
					if _, err = second.Step(ctx, f.bucket, f.policy); !errors.Is(err, state.ErrConflict) || requests.Load() != 1 {
						t.Fatal("competing worker dispatched discovery", err, requests.Load())
					}
					releaseOnce.Do(func() { close(release) })
					select {
					case r := <-finished:
						j, err = r.j, r.err
					case <-time.After(api.ObjectLifecycleFinishTimeout):
						t.Fatal("claimed worker did not finish")
					}
				} else {
					j, err = svc.Step(ctx, f.bucket, f.policy)
				}
				if scenario == "lost checkpoint" {
					if !errors.Is(err, ErrUnavailable) || deletes.Load() != 1 {
						t.Fatal("lost checkpoint fixture", j, err, deletes.Load())
					}
					stored, readErr := f.st.StartObjectLifecycleScan(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID)
					if readErr != nil || stored.LastKey != "" || stored.Token != "" {
						t.Fatal("checkpoint failure lost claim identity", stored, readErr)
					}
					lifecycleWorkerDue(t, f)
					svc.Store = f.reopen()
					// Replay with an intentionally stale provider listing. The
					// completed receipt must prevent a second provider DELETE.
					j, err = svc.Step(ctx, f.bucket, f.policy)
				}
				if scenario == "lost acknowledgment" {
					if err == nil || j.LastKey != "" || j.ScannedKeys != 0 || deletes.Load() != 1 {
						t.Fatal("uncertain mutation checkpointed", j, err, deletes.Load())
					}
					lifecycleWorkerDue(t, f)
					before := requests.Load()
					svc.Store = f.reopen()
					j, err = svc.Step(ctx, f.bucket, f.policy)
					if !errors.Is(err, state.ErrConflict) || j.LastKey != "" || requests.Load() != before {
						t.Fatal("unsettled deletion advanced empty discovery", j, err, requests.Load(), before)
					}
					return
				}
				if scenario == "page failure" {
					if err == nil || j.LastKey != "" || deletes.Load() != 0 || requests.Load() != 1 {
						t.Fatal("failed discovery made progress", j, err, requests.Load())
					}
					lifecycleWorkerDue(t, f)
					svc.Store = f.reopen()
					j, err = svc.Step(ctx, f.bucket, f.policy)
				}
				if err != nil || j.State != "scanning" || j.LastKey != "a" || j.ScannedKeys != 1 || deletes.Load() != 1 {
					t.Fatal("first key", j, err, deletes.Load())
				}
				svc.Store = f.reopen()
				id := j.ID
				if scenario == "restart" {
					j, err = svc.Step(ctx, f.bucket, f.policy)
					if err != nil || j.ID != id || j.LastKey != "b" || j.ScannedKeys != 2 || deletes.Load() != 2 {
						t.Fatal("restart lost key progress", j, err, deletes.Load())
					}
				}
				j, err = svc.Step(ctx, f.bucket, f.policy)
				if err != nil || j.ID != id || j.State != "completed" {
					t.Fatal("scan completion", j, err)
				}
				if rows, err := f.st.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(rows) != 0 {
					t.Fatal("completed scan did not schedule next sweep", rows, err)
				}
				usage, err := f.st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
				if err != nil || usage.Buckets[0].BaselineBytes != 1 || usage.Buckets[0].BaselineKeys != 1 || usage.Buckets[0].GrantedBytes != 0 {
					t.Fatal("discovery or ack refunded storage", usage, err)
				}
			})
		}
	}
}

// adr: 408
func TestLifecycleWorkerActionBudgetAndFailedReplayHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableVersioning(t)
			ctx := t.Context()
			modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
			rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Tags: map[string]string{"ttl": "yes"}}, NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}
			if _, err := f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, []api.ObjectLifecycleRule{rule}); err != nil {
				t.Fatal(err)
			}
			versions := []ListedObjectVersion{{Object: Object{Key: "key", Size: 1, LastModified: modified.AddDate(0, 0, 7)}, ProviderVersionID: "current", IsLatest: true}}
			identities := []state.ObjectVersionIdentity{}
			for i := range api.ObjectLifecycleActionsPerStep + 1 {
				id := fmt.Sprintf("private-%02d", i)
				versions = append(versions, ListedObjectVersion{Object: Object{Key: "key", Size: 1, LastModified: modified.Add(time.Duration(i) * time.Hour)}, ProviderVersionID: id})
				identities = append(identities, state.ObjectVersionIdentity{Key: "key", ProviderVersionID: id})
			}
			refs, err := f.st.RecordObjectVersions(ctx, f.bucket.AccountID, f.bucket.ID, identities)
			if err != nil {
				t.Fatal(err)
			}
			var tags atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				q := r.URL.Query()
				w.Header().Set("Content-Type", "application/xml")
				if q.Has("tagging") {
					tags.Add(1)
					w.Header().Set("X-Amz-Version-Id", q.Get("versionId"))
					_, _ = io.WriteString(w, `<Tagging><TagSet></TagSet></Tagging>`)
					return
				}
				if !q.Has("versions") {
					t.Error("tag mismatch dispatched mutation", r.Method, q)
					w.WriteHeader(500)
					return
				}
				_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
				items := versions
				if q.Get("prefix") == "" {
					if q.Get("version-id-marker") != "" {
						t.Error("native discovery cursor", q)
					}
					items = versions[:1]
					if q.Get("key-marker") != "" {
						items = nil
					}
				}
				for _, v := range items {
					_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version>`, v.ProviderVersionID, v.IsLatest, v.LastModified.Format(time.RFC3339Nano))
				}
				_, _ = io.WriteString(w, `</ListVersionsResult>`)
			})).(Provider)
			svc := LifecycleExpirationService{Store: f.st, Provider: p}
			j, err := svc.Step(ctx, f.bucket, f.policy)
			if !errors.Is(err, errLifecycleActionsPending) || j.LastKey != "" || j.ScannedKeys != 0 || tags.Load() != api.ObjectLifecycleActionsPerStep {
				t.Fatal("unbounded key step", j, err, tags.Load())
			}
			for i, ref := range refs {
				id, err := lifecycleDeletionReceiptID(j.ID, versions[i+1], ref.ID, LifecycleDecision{Kind: "noncurrent", RuleID: "expire"})
				if err != nil {
					t.Fatal(err)
				}
				d, err := f.st.GetObjectDeletion(ctx, f.bucket.AccountID, f.bucket.ID, id)
				if i == api.ObjectLifecycleActionsPerStep {
					if !errors.Is(err, state.ErrNotFound) {
						t.Fatal("step created an excess intent", d, err)
					}
				} else if err != nil || d.State != "failed" {
					t.Fatal("failed action lost stable receipt", d, err)
				}
			}
			lifecycleWorkerDue(t, f)
			svc.Store = f.reopen()
			j, err = svc.Step(ctx, f.bucket, f.policy)
			if err != nil || j.LastKey != "key" || j.ScannedKeys != 1 || tags.Load() != api.ObjectLifecycleActionsPerStep+1 {
				t.Fatal("failed receipts replayed or starved the remaining action", j, err, tags.Load())
			}
			if j, err = svc.Step(ctx, f.bucket, f.policy); err != nil || j.State != "completed" {
				t.Fatal(j, err)
			}
		})
	}
}

// adr: 408
func TestLifecycleWorkerCancelledDiscoveryHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			ctx := t.Context()
			if _, err := f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}}); err != nil {
				t.Fatal(err)
			}
			call, cancel := context.WithCancel(ctx)
			defer cancel()
			var first atomic.Bool
			var requests atomic.Int32
			p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !r.URL.Query().Has("versions") {
					t.Error("unexpected cancelled discovery request", r.Method, r.URL.Path)
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
				if !first.Swap(true) {
					cancel()
					<-r.Context().Done()
					return
				}
				_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated></ListVersionsResult>`)
			})).(Provider)
			svc := LifecycleExpirationService{Store: f.st, Provider: p}
			j, err := svc.Step(call, f.bucket, f.policy)
			if err == nil || call.Err() == nil || requests.Load() != 1 {
				t.Fatal("cancelled discovery succeeded", j, err, requests.Load())
			}
			saved, err := f.reopen().GetObjectLifecycleScan(ctx, f.bucket.AccountID, f.bucket.ID, j.ID)
			if err != nil || saved.Token != "" || saved.LastKey != "" || saved.ScannedKeys != 0 || !saved.RetryAt.After(saved.CreatedAt) {
				t.Fatal("cancelled request did not durably defer its scan", saved, err)
			}
			if due, err := f.st.DueObjectLifecyclePolicies(ctx, api.ObjectLifecycleBatch); err != nil || len(due) != 0 {
				t.Fatal("cancelled scan immediately due", due, err)
			}
			lifecycleWorkerDue(t, f)
			svc.Store = f.reopen()
			recovered, err := svc.Step(ctx, f.bucket, f.policy)
			if err != nil || recovered.ID != j.ID || recovered.State != "completed" || requests.Load() != 2 {
				t.Fatal("cancelled scan did not resume after reconstruction", recovered, err, requests.Load())
			}
		})
	}
}
