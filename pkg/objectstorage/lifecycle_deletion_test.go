package objectstorage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

type lifecycleServiceTestStore interface {
	state.Store
	state.ObjectBucketStore
	state.ObjectStorageAccountingStore
	state.ObjectDeletionStore
	state.ObjectDeletionActivityStore
	state.ObjectLifecycleStore
	state.ObjectVersionReferenceStore
	state.ObjectCapacityStore
	state.ObjectVersionInventoryStore
	state.ObjectBucketVersioningStore
}

type lifecycleServiceFixture struct {
	st        lifecycleServiceTestStore
	pool      *pgxpool.Pool
	bucket    state.ObjectBucket
	policy    api.ObjectStoragePolicy
	advance   func()
	retry     func()
	expire    func(string)
	reopen    func() lifecycleServiceTestStore
	inventory []state.ObjectVersionInventoryRecord
}

func newLifecycleServiceFixture(t *testing.T, pg bool) lifecycleServiceFixture {
	t.Helper()
	ctx := t.Context()
	f := lifecycleServiceFixture{policy: api.ObjectStoragePolicy{MaxAccountBytes: 1 << 20, MaxBucketBytes: 1 << 20, MaxAccountKeys: 1000, MaxMonthlyCostMillicents: 1 << 20, MaxMonthlyRequests: 1000, MaxMonthlyEgressBytes: 1 << 20, MaxMonthlyAuthorizations: 1000, MaxReportAgeSeconds: 3600}}
	if pg {
		f.pool = pgtest.OpenMigrated(t)
		if err := db.MigrateUp(ctx, f.pool); err != nil {
			t.Fatal(err)
		}
		f.st = state.NewPgStore(f.pool)
		f.advance = func() {
			if _, err := f.pool.Exec(ctx, `UPDATE object_bucket_versioning SET propagation_until=CASE WHEN state IN ('waiting','propagating') THEN clock_timestamp()-interval '1 second' ELSE propagation_until END,retry_at=clock_timestamp()`); err != nil {
				t.Fatal(err)
			}
		}
		f.retry = f.advance
		f.expire = func(id string) {
			if _, err := f.pool.Exec(ctx, `UPDATE object_deletions SET lease_until=CASE WHEN lease_token='' THEN NULL ELSE clock_timestamp()-interval '1 second' END,retry_at=clock_timestamp()-interval '1 second' WHERE id=$1`, id); err != nil {
				t.Fatal(err)
			}
		}
		f.reopen = func() lifecycleServiceTestStore { return state.NewPgStore(f.pool) }
	} else {
		m := state.NewMemStore()
		now := time.Now().UTC()
		var offset atomic.Int64
		m.SetClockForTest(func() time.Time { return now.Add(time.Duration(offset.Load())) })
		f.st = m
		f.advance = func() { offset.Add(int64(api.ObjectBucketVersioningPropagation + time.Second)) }
		f.retry = func() { offset.Add(int64(api.ObjectBucketVersioningRetry + time.Second)) }
		f.expire = func(string) { offset.Add(int64(api.ObjectDeletionLease + api.ObjectDeletionRetry + time.Second)) }
		f.reopen = func() lifecycleServiceTestStore { return m }
	}
	acct, err := f.st.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := f.st.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "life-" + uuid.NewString(), Status: state.AppActive})
	if err != nil {
		t.Fatal(err)
	}
	f.bucket, err = f.st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: acct.ID, AppID: app.ID, Name: "lifecycle", Scope: "default", Region: "us-east-1", BackendID: "test", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "lifecycle-test"}, api.DefaultObjectBucketsPerApp)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.ClaimObjectBucket(ctx, acct.ID, app.ID, f.bucket.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = f.st.FinishObjectBucket(ctx, f.bucket.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	f.bucket.State = "ready"
	inventoryToken := uuid.NewString()
	if err = f.st.ClaimObjectInventory(ctx, f.bucket.ID, inventoryToken); err != nil {
		t.Fatal(err)
	}
	if err = f.st.FinishObjectInventory(ctx, f.bucket.ID, inventoryToken, 1, 1); err != nil {
		t.Fatal(err)
	}
	report := api.ObjectStorageUsageReport{AccountID: acct.ID, BackendID: "test", BackendFingerprint: f.bucket.BackendFingerprint, Source: "qualified-provider", PeriodStart: state.ObjectStoragePeriod(time.Now()), ObservedAt: time.Now().UTC()}
	if err = f.st.RecordObjectUsageReport(ctx, report); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f lifecycleServiceFixture) enableVersioning(t *testing.T) {
	t.Helper()
	ctx := t.Context()
	b := f.bucket
	if _, err := f.st.ObserveObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, "Enabled"); err != nil {
		t.Fatal(err)
	}
	f.advance()
	j, err := f.st.ClaimObjectBucketVersioning(ctx, b.ID, "configure")
	if err != nil {
		t.Fatal(err)
	}
	j, err = f.st.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled")
	if err != nil {
		t.Fatal(err)
	}
	c, err := f.st.ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	records := f.inventory
	if records == nil {
		records = []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 1}}
	}
	if _, err = f.st.StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", records); err != nil {
		t.Fatal(err)
	}
	f.retry()
	j, err = f.st.ClaimObjectBucketVersioning(ctx, b.ID, "verify")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.st.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, "Enabled"); err != nil {
		t.Fatal(err)
	}
}

// adr: 550
func TestLifecycleDeletionImmutableHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"noncurrent", "restored current", "sole marker", "marker with history", "immutable lost acknowledgment"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, scenario), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				ctx := t.Context()
				marker := strings.Contains(scenario, "marker")
				f.inventory = []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 1}, {Identity: strings.Repeat("b", 64), Bytes: 1}}
				if marker {
					f.inventory = []state.ObjectVersionInventoryRecord{{Identity: strings.Repeat("a", 64), Bytes: 3}}
					if scenario == "marker with history" {
						f.inventory = append(f.inventory, state.ObjectVersionInventoryRecord{Identity: strings.Repeat("b", 64), Bytes: 1})
					}
				}
				f.enableVersioning(t)
				modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
				target := ListedObjectVersion{Object: Object{Key: "key", LastModified: modified, Size: 1}, ProviderVersionID: "private-target", DeleteMarker: marker, IsLatest: marker}
				rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}
				kind := "noncurrent"
				if marker {
					value := true
					kind = "expired_marker"
					rule.NoncurrentVersionExpiration = nil
					rule.Expiration = &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &value}
				}
				scan := f.scan(t, []api.ObjectLifecycleRule{rule})
				refs, err := f.st.RecordObjectVersions(ctx, f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: "key", ProviderVersionID: "private-target", DeleteMarker: marker}})
				if err != nil {
					t.Fatal(err)
				}
				var deletes atomic.Int32
				var deleted atomic.Bool
				p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/xml")
					if q.Has("versions") {
						_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
						if !deleted.Load() {
							if marker {
								_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>key</Key><VersionId>private-target</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified></DeleteMarker>`, modified.Format(time.RFC3339Nano))
							} else {
								_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-target</VersionId><IsLatest>%t</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;target&quot;</ETag></Version>`, scenario == "restored current", modified.Format(time.RFC3339Nano))
							}
						}
						if !marker && scenario != "restored current" {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-current</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;current&quot;</ETag></Version>`, modified.AddDate(0, 0, 7).Format(time.RFC3339Nano))
						}
						if scenario == "marker with history" {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-retained</VersionId><IsLatest>false</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;retained&quot;</ETag></Version>`, modified.AddDate(0, 0, -1).Format(time.RFC3339Nano))
						}
						_, _ = io.WriteString(w, `</ListVersionsResult>`)
						return
					}
					if r.Method != http.MethodDelete || q.Get("versionId") != "private-target" {
						t.Error("immutable target changed", r.Method, q)
						w.WriteHeader(500)
						return
					}
					deletes.Add(1)
					deleted.Store(true)
					if scenario == "immutable lost acknowledgment" && deletes.Load() == 1 {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					w.Header().Set("X-Amz-Version-Id", "private-target")
					if marker {
						w.Header().Set("X-Amz-Delete-Marker", "true")
					}
					w.WriteHeader(http.StatusNoContent)
				})).(Provider)
				s := DeletionService{Store: f.st, Provider: p}
				before, err := f.st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				j, err := s.StartLifecycle(ctx, f.bucket, scan, target, refs[0].ID, LifecycleDecision{Kind: kind, RuleID: "expire"}, f.policy)
				if scenario == "restored current" || scenario == "marker with history" {
					if !errors.Is(err, ErrLifecycleNotDue) || j.State != "failed" || deletes.Load() != 0 {
						t.Fatal("protected target expired", j, err, deletes.Load())
					}
				} else if scenario == "immutable lost acknowledgment" {
					if err == nil || j.State != "dispatched" || deletes.Load() != 1 {
						t.Fatal(j, err, deletes.Load())
					}
					f.expire(j.ID)
					// A dispatched exact mutation remains recoverable after the old
					// scan and its rule are cancelled; it cannot authorize a new target.
					if pg {
						if _, err = f.pool.Exec(ctx, `UPDATE object_lifecycle_scans SET lease_until=clock_timestamp()-interval '1 second' WHERE id=$1`, scan.ID); err != nil {
							t.Fatal(err)
						}
					}
					if _, err = f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, nil); err != nil {
						t.Fatal(err)
					}
					s.Store = f.reopen()
					j, err = s.Recover(ctx, f.bucket, j.ID)
					if err != nil || j.State != "completed" || deletes.Load() != 2 {
						t.Fatal("exact recovery failed", j, err, deletes.Load())
					}
				} else if err != nil || j.State != "completed" || j.VersionID != refs[0].ID || j.DeleteMarker != marker || deletes.Load() != 1 {
					t.Fatal(j, err, deletes.Load())
				}
				replay, err := s.StartLifecycle(ctx, f.bucket, scan, target, refs[0].ID, LifecycleDecision{Kind: kind, RuleID: "expire"}, f.policy)
				if err != nil || replay.ID != j.ID || replay.State != j.State || deletes.Load() > 2 {
					t.Fatal("immutable replay changed receipt", replay, err)
				}
				after, err := f.st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
				if err != nil || before.Buckets[0].BaselineBytes != after.Buckets[0].BaselineBytes || before.Buckets[0].BaselineKeys != after.Buckets[0].BaselineKeys || after.Buckets[0].GrantedBytes != 0 || after.Buckets[0].GrantedKeys != 0 {
					t.Fatal("immutable ack changed accounting", before, after, err)
				}
			})
		}
	}
}

func (f lifecycleServiceFixture) scan(t *testing.T, rules []api.ObjectLifecycleRule) state.ObjectLifecycleScan {
	t.Helper()
	b := f.bucket
	ctx := t.Context()
	if _, err := f.st.SetObjectBucketLifecycle(ctx, b.AccountID, b.AppID, b.ID, rules); err != nil {
		t.Fatal(err)
	}
	j, err := f.st.StartObjectLifecycleScan(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil {
		t.Fatal(err)
	}
	j, err = f.st.ClaimObjectLifecycleScan(ctx, j.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// adr: 550
func TestLifecycleDeletionCurrentHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"expire", "unversioned tagged expire", "new current", "tag removed", "cancelled scan", "lost acknowledgment", "page limit", "request budget"} {
			name := fmt.Sprintf("pg=%t/%s", pg, scenario)
			t.Run(name, func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				unversioned := scenario == "unversioned tagged expire"
				if !unversioned {
					f.enableVersioning(t)
				}
				ctx := t.Context()
				modified := time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond)
				target := ListedObjectVersion{Object: Object{Key: "key", Size: 1, LastModified: modified}, ProviderVersionID: "private-old", IsLatest: true}
				if unversioned {
					target.ProviderVersionID = "null"
				}
				rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", Expiration: &api.ObjectLifecycleExpiration{Days: lifecycleInt(1)}}
				if scenario == "tag removed" || unversioned {
					rule.Filter.Tags = map[string]string{"ttl": ""}
				}
				scan := f.scan(t, []api.ObjectLifecycleRule{rule})
				var deletes, lists atomic.Int32
				var deleted atomic.Bool
				p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					q := r.URL.Query()
					w.Header().Set("Content-Type", "application/xml")
					if q.Has("versioning") {
						if unversioned {
							_, _ = io.WriteString(w, `<VersioningConfiguration/>`)
						} else {
							_, _ = io.WriteString(w, `<VersioningConfiguration><Status>Enabled</Status></VersioningConfiguration>`)
						}
						return
					}
					if q.Has("versions") {
						lists.Add(1)
						_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType>`)
						if scenario == "page limit" {
							_, _ = fmt.Fprintf(w, `<IsTruncated>true</IsTruncated><NextKeyMarker>key</NextKeyMarker><NextVersionIdMarker>cursor-%d</NextVersionIdMarker>`, lists.Load())
						} else {
							_, _ = io.WriteString(w, `<IsTruncated>false</IsTruncated>`)
						}
						if deleted.Load() {
							_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>key</Key><VersionId>private-created-marker</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified></DeleteMarker>`, time.Now().UTC().Format(time.RFC3339Nano))
						}
						if scenario == "new current" {
							_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-new</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;new&quot;</ETag></Version>`, time.Now().UTC().Format(time.RFC3339Nano))
						}
						id := target.ProviderVersionID
						if scenario == "page limit" {
							id = fmt.Sprintf("private-%d", lists.Load())
						}
						_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>%s</VersionId><IsLatest>%t</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;data&quot;</ETag></Version></ListVersionsResult>`, id, !deleted.Load() && scenario != "new current", modified.Format(time.RFC3339Nano))
						return
					}
					if q.Has("tagging") {
						selector := target.ProviderVersionID
						if unversioned {
							selector = ""
						}
						if q.Get("versionId") != selector {
							t.Error("tag selection changed", q)
						}
						if unversioned {
							_, _ = io.WriteString(w, `<Tagging><TagSet><Tag><Key>ttl</Key><Value></Value></Tag></TagSet></Tagging>`)
						} else {
							w.Header().Set("X-Amz-Version-Id", target.ProviderVersionID)
							_, _ = io.WriteString(w, `<Tagging><TagSet></TagSet></Tagging>`)
						}
						return
					}
					if r.Method != http.MethodDelete || q.Get("versionId") != "" {
						t.Error("wrong expiration dispatch", r.Method, q)
						w.WriteHeader(500)
						return
					}
					deletes.Add(1)
					deleted.Store(true)
					if scenario == "lost acknowledgment" {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err != nil {
							t.Error(err)
							return
						}
						_ = conn.Close()
						return
					}
					if !unversioned {
						w.Header().Set("X-Amz-Version-Id", "private-created-marker")
						w.Header().Set("X-Amz-Delete-Marker", "true")
					}
					w.WriteHeader(http.StatusNoContent)
				})).(Provider)
				s := DeletionService{Store: f.st, Provider: p}
				if scenario == "request budget" {
					s.BeforeRequest = func(context.Context) error { return state.ErrObjectBudget }
				}
				if scenario == "cancelled scan" {
					var requests atomic.Int32
					s.BeforeRequest = func(ctx context.Context) error {
						if requests.Add(1) != 4 {
							return nil
						}
						// Stop the scanner after final eligibility but before dispatch.
						// The deletion lease is still valid, so only the atomic rule
						// and scan guard can prevent the provider mutation.
						if err := f.st.RetryObjectLifecycleScan(ctx, scan.ID, scan.Token); err != nil {
							return err
						}
						_, err := f.st.SetObjectBucketLifecycle(ctx, f.bucket.AccountID, f.bucket.AppID, f.bucket.ID, nil)
						return err
					}
				}
				before, err := f.st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				j, err := s.StartLifecycle(ctx, f.bucket, scan, target, "", LifecycleDecision{Kind: "current", RuleID: "expire"}, f.policy)
				switch scenario {
				case "expire", "unversioned tagged expire":
					if err != nil || j.State != "completed" || j.DeleteMarker != !unversioned || deletes.Load() != 1 {
						t.Fatal(j, err, deletes.Load())
					}
				case "lost acknowledgment":
					if err == nil || j.State != "dispatched" || deletes.Load() != 1 {
						t.Fatal(j, err, deletes.Load())
					}
					f.expire(j.ID)
					s.Store = f.reopen()
					j, err = s.Recover(ctx, f.bucket, j.ID)
					if err != nil || j.State != "completed" || deletes.Load() != 1 {
						t.Fatal("recovery redispatched", j, err, deletes.Load())
					}
				default:
					if err == nil || j.State != "failed" || deletes.Load() != 0 {
						t.Fatal("unsafe target expired", j, err, deletes.Load())
					}
				}
				if scenario == "new current" || scenario == "tag removed" {
					if !errors.Is(err, ErrLifecycleNotDue) {
						t.Fatal("wrong eligibility failure", err)
					}
				}
				if scenario == "cancelled scan" && !errors.Is(err, state.ErrConflict) {
					t.Fatal("cancelled scan authorized dispatch", err)
				}
				if scenario == "page limit" && lists.Load() != api.ObjectDeletionHistoryPages {
					t.Fatal("unbounded preparation", lists.Load())
				}
				replay, err := s.StartLifecycle(ctx, f.bucket, scan, target, "", LifecycleDecision{Kind: "current", RuleID: "expire"}, f.policy)
				if err != nil || replay.ID != j.ID || replay.State != j.State || deletes.Load() > 1 {
					t.Fatal("receipt replay mutated", replay, err, deletes.Load())
				}
				after, err := f.st.ObjectUsage(ctx, f.bucket.AccountID, time.Now())
				if err != nil {
					t.Fatal(err)
				}
				if after.Buckets[0].BaselineBytes != before.Buckets[0].BaselineBytes || after.Buckets[0].BaselineKeys != before.Buckets[0].BaselineKeys {
					t.Fatal("ack refunded baseline", before, after)
				}
				wantBytes, wantKeys := int64(0), int64(0)
				if deletes.Load() != 0 && !unversioned {
					wantBytes, wantKeys = int64(len(target.Key)), 1
				}
				if after.Buckets[0].GrantedBytes-before.Buckets[0].GrantedBytes != wantBytes || after.Buckets[0].GrantedKeys-before.Buckets[0].GrantedKeys != wantKeys {
					t.Fatal("marker admission or preparation refund", before, after)
				}
			})
		}
	}
}

// adr: 550
func TestLifecycleHistorySuccessorsAndTies(t *testing.T) {
	now := time.Now().UTC()
	base := now.AddDate(0, 0, -10).Truncate(time.Millisecond)
	version := func(id string, days int, latest, marker bool) ListedObjectVersion {
		return ListedObjectVersion{Object: Object{Key: "key", LastModified: base.AddDate(0, 0, days)}, ProviderVersionID: id, IsLatest: latest, DeleteMarker: marker}
	}
	for _, tc := range []struct {
		name     string
		versions []ListedObjectVersion
		since    time.Time
		newer    int
		want     error
	}{
		{"mixed provider order", []ListedObjectVersion{version("old", 0, false, false), version("new", 3, false, false), version("marker", 7, true, true)}, base.AddDate(0, 0, 3), 1, nil},
		{"equal timestamps conservative", []ListedObjectVersion{version("tie", 0, false, false), version("old", 0, false, false), version("current", 7, true, false)}, base.AddDate(0, 0, 7), 0, nil},
		{"all equal timestamps", []ListedObjectVersion{version("old", 0, false, false), version("current", 0, true, false)}, base, 0, nil},
		{"duplicate current", []ListedObjectVersion{version("old", 0, true, false), version("current", 7, true, false)}, time.Time{}, 0, ErrUnavailable},
		{"missing current", []ListedObjectVersion{version("old", 0, false, false)}, time.Time{}, 0, ErrUnavailable},
		{"current older than history", []ListedObjectVersion{version("old", 7, false, false), version("current", 0, true, false)}, time.Time{}, 0, ErrUnavailable},
	} {
		t.Run(tc.name, func(t *testing.T) {
			modified := base
			if tc.name == "current older than history" {
				modified = base.AddDate(0, 0, 7)
			}
			o, err := lifecycleHistoryObject(tc.versions, "old", modified, now)
			if tc.want != nil {
				if !errors.Is(err, tc.want) {
					t.Fatal(o, err)
				}
				return
			}
			if err != nil || !o.NoncurrentSince.Equal(tc.since) || o.NewerNoncurrent != tc.newer {
				t.Fatal(o, err)
			}
		})
	}
}
