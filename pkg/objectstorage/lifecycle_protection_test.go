package objectstorage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

func (f lifecycleServiceFixture) enableObjectLock(t *testing.T) {
	t.Helper()
	f.enableVersioning(t)
	s := f.st.(state.ObjectBucketObjectLockStore)
	b := f.bucket
	c := api.ObjectBucketObjectLockConfiguration{Enabled: true}
	if _, err := s.RequestObjectBucketObjectLock(t.Context(), b.AccountID, b.AppID, b.ID, c); err != nil {
		t.Fatal(err)
	}
	j, err := s.ClaimObjectBucketObjectLock(t.Context(), b.ID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.FinishObjectBucketObjectLock(t.Context(), b.ID, j.Token, c); err != nil {
		t.Fatal(err)
	}
}

type lifecycleProtectionNative struct {
	target                      ListedObjectVersion
	retention, hold, versioning string
	missingLock                 bool
	requests, deletes, reads    atomic.Int32
	deleted, loseAck, keep      atomic.Bool
	brokenProof                 atomic.Bool
}

func newLifecycleProtectionNative(t *testing.T, marker, null bool) (*lifecycleProtectionNative, Provider) {
	t.Helper()
	id := "private-protected-target"
	if null {
		id = "null"
	}
	n := &lifecycleProtectionNative{
		target:    ListedObjectVersion{Object: Object{Key: "key", LastModified: time.Now().UTC().AddDate(0, 0, -10).Truncate(time.Millisecond), Size: 1}, ProviderVersionID: id, DeleteMarker: marker, IsLatest: marker},
		retention: `<Retention/>`, hold: `<LegalHold><Status>OFF</Status></LegalHold>`, versioning: "Enabled",
	}
	p := historyTestProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.requests.Add(1)
		q := r.URL.Query()
		w.Header().Set("Content-Type", "application/xml")
		if q.Has("object-lock") {
			if n.missingLock {
				w.WriteHeader(404)
				_, _ = io.WriteString(w, `<Error><Code>ObjectLockConfigurationNotFoundError</Code></Error>`)
				return
			}
			_, _ = io.WriteString(w, `<ObjectLockConfiguration><ObjectLockEnabled>Enabled</ObjectLockEnabled></ObjectLockConfiguration>`)
			return
		}
		if q.Has("versioning") {
			_, _ = fmt.Fprintf(w, `<VersioningConfiguration><Status>%s</Status></VersioningConfiguration>`, n.versioning)
			return
		}
		if q.Has("legal-hold") || q.Has("retention") {
			n.reads.Add(1)
			if q.Get("versionId") != n.target.ProviderVersionID || n.deleted.Load() {
				t.Error("protection read used an absent or different target", q)
				w.WriteHeader(404)
				return
			}
			w.Header().Set("X-Amz-Version-Id", n.target.ProviderVersionID)
			body := n.retention
			if q.Has("legal-hold") {
				body = n.hold
			}
			_, _ = io.WriteString(w, body)
			return
		}
		if q.Has("versions") {
			if n.deleted.Load() && n.brokenProof.Load() {
				_, _ = io.WriteString(w, `<ListVersionsResult><IsTruncated>true</IsTruncated></ListVersionsResult>`)
				return
			}
			_, _ = io.WriteString(w, `<ListVersionsResult><EncodingType>url</EncodingType><IsTruncated>false</IsTruncated>`)
			if !n.deleted.Load() {
				if n.target.DeleteMarker {
					_, _ = fmt.Fprintf(w, `<DeleteMarker><Key>key</Key><VersionId>%s</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified></DeleteMarker>`, n.target.ProviderVersionID, n.target.LastModified.Format(time.RFC3339Nano))
				} else {
					_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>%s</VersionId><IsLatest>false</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;target&quot;</ETag></Version>`, n.target.ProviderVersionID, n.target.LastModified.Format(time.RFC3339Nano))
				}
			}
			if !n.target.DeleteMarker {
				_, _ = fmt.Fprintf(w, `<Version><Key>key</Key><VersionId>private-current</VersionId><IsLatest>true</IsLatest><LastModified>%s</LastModified><Size>1</Size><ETag>&quot;current&quot;</ETag></Version>`, n.target.LastModified.AddDate(0, 0, 5).Format(time.RFC3339Nano))
			}
			_, _ = io.WriteString(w, `</ListVersionsResult>`)
			return
		}
		if r.Method != http.MethodDelete || q.Get("versionId") != n.target.ProviderVersionID || r.Header.Get("X-Amz-Bypass-Governance-Retention") != "" {
			t.Error("unexpected mutation", r.Method, q)
			w.WriteHeader(500)
			return
		}
		n.deletes.Add(1)
		if !n.keep.Load() {
			n.deleted.Store(true)
		}
		if n.loseAck.Swap(false) {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			_ = conn.Close()
			return
		}
		w.Header().Set("X-Amz-Version-Id", n.target.ProviderVersionID)
		// Repeated DELETE may omit the marker header. Classification comes from
		// immutable discovery plus complete absence proof, not this ACK.
		w.WriteHeader(204)
	}))
	return n, p.(Provider)
}

func lifecycleProtectionPlan(t *testing.T, f lifecycleServiceFixture, n *lifecycleProtectionNative) (state.ObjectLifecycleScan, string, LifecycleDecision) {
	t.Helper()
	rule := api.ObjectLifecycleRule{ID: "expire", Status: "Enabled", NoncurrentVersionExpiration: &api.ObjectLifecycleNoncurrentExpiration{NoncurrentDays: 1}}
	kind := "noncurrent"
	if n.target.DeleteMarker {
		value := true
		rule.NoncurrentVersionExpiration = nil
		rule.Expiration = &api.ObjectLifecycleExpiration{ExpiredObjectDeleteMarker: &value}
		kind = "expired_marker"
	}
	scan := f.scan(t, []api.ObjectLifecycleRule{rule})
	selector := "null"
	if n.target.ProviderVersionID != "null" {
		refs, err := f.st.RecordObjectVersions(t.Context(), f.bucket.AccountID, f.bucket.ID, []state.ObjectVersionIdentity{{Key: n.target.Key, ProviderVersionID: n.target.ProviderVersionID, DeleteMarker: n.target.DeleteMarker}})
		if err != nil {
			t.Fatal(err)
		}
		selector = refs[0].ID
	}
	return scan, selector, LifecycleDecision{RuleID: rule.ID, Kind: kind}
}

// adr: 619
func TestLifecycleProtectionHTTP(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour).Format(time.RFC3339Nano)
	future := time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)
	for _, pg := range []bool{false, true} {
		for _, tc := range []struct {
			name, retention, hold, code string
		}{
			{name: "no retention"},
			{name: "expired compliance", retention: `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>` + past + `</RetainUntilDate></Retention>`},
			{name: "fixed compliance", retention: `<Retention><Mode>COMPLIANCE</Mode><RetainUntilDate>` + future + `</RetainUntilDate></Retention>`, code: "object_protected"},
			{name: "fixed governance", retention: `<Retention><Mode>GOVERNANCE</Mode><RetainUntilDate>` + future + `</RetainUntilDate></Retention>`, code: "object_protected"},
			{name: "legal hold", hold: `<LegalHold><Status>ON</Status></LegalHold>`, code: "object_protected"},
			{name: "event hold", retention: `<Retention><Mode>COMPLIANCE</Mode><EventHold>ON</EventHold><EventHoldDuration><Days>1</Days></EventHoldDuration></Retention>`, code: "object_protected"},
			{name: "released event", retention: `<Retention><Mode>COMPLIANCE</Mode><EventHold>OFF</EventHold><RetainUntilDate>` + past + `</RetainUntilDate></Retention>`},
			{name: "release missing date", retention: `<Retention><Mode>COMPLIANCE</Mode><EventHold>OFF</EventHold></Retention>`, code: "preparation_failed"},
			{name: "malformed hold", hold: `<LegalHold/>`, code: "preparation_failed"},
			{name: "unknown policy", retention: `<Retention><Future>ON</Future></Retention>`, code: "preparation_failed"},
			{name: "missing lock", code: "preparation_failed"},
			{name: "suspended", code: "preparation_failed"},
			{name: "marker"}, {name: "null"},
		} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, tc.name), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				f.enableObjectLock(t)
				n, p := newLifecycleProtectionNative(t, tc.name == "marker", tc.name == "null")
				if tc.retention != "" {
					n.retention = tc.retention
				}
				if tc.hold != "" {
					n.hold = tc.hold
				}
				n.missingLock = tc.name == "missing lock"
				if tc.name == "suspended" {
					n.versioning = "Suspended"
				}
				scan, selector, decision := lifecycleProtectionPlan(t, f, n)
				var metered atomic.Int32
				s := DeletionService{Store: f.st, Provider: p, BeforeRequest: func(_ context.Context) error { metered.Add(1); return nil }}
				j, err := s.StartLifecycle(t.Context(), f.bucket, scan, n.target, selector, decision, f.policy)
				if tc.code == "" {
					if err != nil || j.State != "completed" || !j.ProtectionRequired || !j.ProtectionVerified || !j.DeletionVerified || j.DeleteMarker != n.target.DeleteMarker || n.deletes.Load() != 1 {
						t.Fatal(j, n.deletes.Load(), err)
					}
				} else if err == nil || j.State != "failed" || j.LastErrorCode != tc.code || !j.ProtectionRequired || j.ProtectionVerified || j.DeletionVerified || n.deletes.Load() != 0 {
					t.Fatal(j, n.deletes.Load(), err)
				}
				if tc.code == "object_protected" && !errors.Is(err, ErrObjectProtected) {
					t.Fatal("missing protected error", err)
				}
				if tc.name == "marker" && n.reads.Load() != 0 {
					t.Fatal("marker incorrectly read data protection")
				}
				if metered.Load() != n.requests.Load() {
					t.Fatal("unmetered native request", metered.Load(), n.requests.Load())
				}
				u, err := f.st.ObjectUsage(t.Context(), f.bucket.AccountID, time.Now())
				if err != nil || u.Buckets[0].BaselineBytes != 1 || u.Buckets[0].BaselineKeys != 1 {
					t.Fatal("deletion proof refunded an unverified inventory baseline", u, err)
				}
				stored, err := f.reopen().GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, j.ID)
				if err != nil || stored.ProtectionRequired != j.ProtectionRequired || stored.DeletionVerified != j.DeletionVerified {
					t.Fatal("proof lost on reconstruction", stored, err)
				}
			})
		}
	}
}

// adr: 619
func TestLifecycleProtectionRecoveryHTTP(t *testing.T) {
	for _, pg := range []bool{false, true} {
		for _, scenario := range []string{"lost ack", "null lost ack", "marker lost ack", "ack still present", "hold after uncertainty", "truncated proof"} {
			t.Run(fmt.Sprintf("pg=%t/%s", pg, scenario), func(t *testing.T) {
				f := newLifecycleServiceFixture(t, pg)
				f.enableObjectLock(t)
				n, p := newLifecycleProtectionNative(t, strings.HasPrefix(scenario, "marker"), strings.HasPrefix(scenario, "null"))
				n.loseAck.Store(strings.Contains(scenario, "lost ack"))
				n.brokenProof.Store(scenario == "truncated proof")
				n.keep.Store(!n.loseAck.Load() && !n.brokenProof.Load())
				scan, selector, decision := lifecycleProtectionPlan(t, f, n)
				s := DeletionService{Store: f.st, Provider: p}
				j, err := s.StartLifecycle(t.Context(), f.bucket, scan, n.target, selector, decision, f.policy)
				if err == nil || j.State != "dispatched" || !j.ProtectionVerified || j.DeletionVerified || n.deletes.Load() != 1 {
					t.Fatal(j, err)
				}
				if active, err := f.st.HasActiveObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.AppID, f.bucket.ID); err != nil || !active {
					t.Fatal("uncertainty released custody", active, err)
				}
				f.expire(j.ID)
				s.Store = f.reopen()
				if scenario == "hold after uncertainty" {
					n.hold = `<LegalHold><Status>ON</Status></LegalHold>`
					out, err := s.Recover(t.Context(), f.bucket, j.ID)
					if !errors.Is(err, ErrObjectProtected) || out.State != "dispatched" || out.DeletionVerified || n.deletes.Load() != 1 {
						t.Fatal("recovery removed protection or original evidence", out, err)
					}
					f.expire(j.ID)
					n.hold = `<LegalHold><Status>OFF</Status></LegalHold>`
				}
				n.keep.Store(false)
				n.brokenProof.Store(false)
				out, err := s.Recover(t.Context(), f.bucket, j.ID)
				wantDeletes := int32(2)
				if strings.Contains(scenario, "lost ack") || scenario == "truncated proof" {
					wantDeletes = 1
				}
				if err != nil || out.State != "completed" || !out.DeletionVerified || n.deletes.Load() != wantDeletes {
					t.Fatal(out, n.deletes.Load(), err)
				}
			})
		}
	}
}

// adr: 619
func TestLifecycleProtectionJournalFences(t *testing.T) {
	for _, pg := range []bool{false, true} {
		t.Run(fmt.Sprintf("pg=%t", pg), func(t *testing.T) {
			f := newLifecycleServiceFixture(t, pg)
			f.enableObjectLock(t)
			n, _ := newLifecycleProtectionNative(t, false, false)
			scan, selector, decision := lifecycleProtectionPlan(t, f, n)
			marker := false
			input := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: f.bucket.ID, Key: n.target.Key, Selector: selector}, AccountID: f.bucket.AccountID, AppID: f.bucket.AppID, Token: "owned", Lifecycle: &state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: decision.RuleID, Kind: decision.Kind, ExpectedProviderVersionID: n.target.ProviderVersionID, ExpectedLastModified: n.target.LastModified}}
			if _, _, err := f.st.BeginObjectDeletion(t.Context(), input, f.policy); !errors.Is(err, state.ErrConflict) {
				t.Fatal("legacy admission passed a permanently locked bucket", err)
			}
			input.Lifecycle.ExpectedDeleteMarker = &marker
			j, created, err := f.st.BeginObjectDeletion(t.Context(), input, f.policy)
			if err != nil || !created || !j.ProtectionRequired {
				t.Fatal(j, err)
			}
			marker = true
			*j.Lifecycle.ExpectedDeleteMarker = true
			j, err = f.reopen().GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, j.ID)
			if err != nil || *j.Lifecycle.ExpectedDeleteMarker {
				t.Fatal("caller rewrote frozen marker classification", j, err)
			}
			if _, err = f.st.DispatchObjectDeletion(t.Context(), j.ID, j.Token, "", nil); !errors.Is(err, state.ErrConflict) {
				t.Fatal("unaware worker dispatched", err)
			}
			if pg {
				// Replaying all earlier S3 DDL must retain the separate proof fence.
				names, err := fs.Glob(migrations.FS, "202610040906*.sql")
				if err != nil || len(names) == 0 {
					t.Fatal(names, err)
				}
				for _, name := range append(names, "20261005143206150_object_lifecycle_protection.sql") {
					body, err := migrations.FS.ReadFile(name)
					if err != nil {
						t.Fatal(err)
					}
					up := strings.Split(strings.Split(string(body), "-- +goose Up")[1], "-- +goose Down")[0]
					if _, err = f.pool.Exec(t.Context(), up); err != nil {
						t.Fatal("live receipt replay", name, err)
					}
				}
				for _, sql := range []string{
					`INSERT INTO object_deletions(id,bucket_id,object_key,selector,target_provider_version_id,lifecycle_scan_id,lifecycle_binding,state,lease_token,lease_until,protection_required) SELECT gen_random_uuid(),bucket_id,object_key,selector,target_provider_version_id,lifecycle_scan_id,lifecycle_binding,'dispatched','old-worker',clock_timestamp()+interval '1 minute',true FROM object_deletions WHERE id=$1`,
					`UPDATE object_deletions SET state='dispatched' WHERE id=$1`,
					`UPDATE object_deletions SET protection_required=false WHERE id=$1`,
					`UPDATE object_deletions SET lifecycle_binding=lifecycle_binding||'{"expected_delete_marker":true}' WHERE id=$1`,
				} {
					if _, err = f.pool.Exec(t.Context(), sql, j.ID); err == nil {
						t.Fatal("legacy SQL weakened protection", sql)
					}
				}
			}
			j, err = f.reopen().(state.ObjectProtectedLifecycleDeletionStore).DispatchObjectProtectedLifecycleDeletion(t.Context(), j.ID, j.Token, "", nil)
			if err != nil || !j.ProtectionVerified {
				t.Fatal(j, err)
			}
			result := j
			result.State, result.ProviderVersionID, result.VersionID = "completed", j.TargetProviderVersionID, j.Selector
			if _, err = f.st.FinishObjectDeletion(t.Context(), result); !errors.Is(err, state.ErrConflict) {
				t.Fatal("ACK settled without exact absence proof", err)
			}
			if pg {
				if _, err = f.pool.Exec(t.Context(), `UPDATE object_deletions SET state='completed',provider_version_id=target_provider_version_id,version_id=selector,lease_token='',lease_until=NULL WHERE id=$1`, j.ID); err == nil {
					t.Fatal("old SQL writer settled without proof")
				}
			}
			result.DeletionVerified = true
			result.DeleteMarker = true
			if _, err = f.st.FinishObjectDeletion(t.Context(), result); !errors.Is(err, state.ErrConflict) {
				t.Fatal("wrong target class settled", err)
			}
			result.DeleteMarker = false
			out, err := f.st.FinishObjectDeletion(t.Context(), result)
			if err != nil || !out.DeletionVerified || out.State != "completed" {
				t.Fatal(out, err)
			}
			if pg {
				body, err := migrations.FS.ReadFile("20261005143206150_object_lifecycle_protection.sql")
				if err != nil {
					t.Fatal(err)
				}
				if _, err = f.pool.Exec(t.Context(), strings.Split(string(body), "-- +goose Down")[1]); err == nil {
					t.Fatal("rollback discarded protection history")
				}
			}
		})
	}
}

// adr: 619
func TestLifecycleProtectionLegacyUpgradePG(t *testing.T) {
	f := newLifecycleServiceFixture(t, true)
	f.enableObjectLock(t)
	n, p := newLifecycleProtectionNative(t, false, false)
	scan, selector, decision := lifecycleProtectionPlan(t, f, n)
	body, err := migrations.FS.ReadFile("20261005143206150_object_lifecycle_protection.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(string(body), "-- +goose Down")
	if _, err = f.pool.Exec(t.Context(), parts[1]); err != nil {
		t.Fatal("pristine rollback", err)
	}
	binding, err := json.Marshal(state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: decision.RuleID, Kind: decision.Kind, ExpectedProviderVersionID: n.target.ProviderVersionID, ExpectedLastModified: n.target.LastModified})
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.NewString()
	if _, err = f.pool.Exec(t.Context(), `INSERT INTO object_deletions(id,bucket_id,object_key,selector,state,lease_token,lease_until,target_provider_version_id,lifecycle_scan_id,lifecycle_binding) VALUES($1,$2,'key',$3,'prepared','old-worker',clock_timestamp()+interval '1 minute',$4,$5,$6)`, id, f.bucket.ID, selector, n.target.ProviderVersionID, scan.ID, binding); err != nil {
		t.Fatal("legacy preparation", err)
	}
	if _, err = f.pool.Exec(t.Context(), `UPDATE object_deletions SET state='dispatched' WHERE id=$1`, id); err != nil {
		t.Fatal("legacy dispatch", err)
	}
	up := strings.Split(parts[0], "-- +goose Up")[1]
	if _, err = f.pool.Exec(t.Context(), up); err != nil {
		t.Fatal("upgrade with active custody", err)
	}
	j, err := f.reopen().GetObjectDeletion(t.Context(), f.bucket.AccountID, f.bucket.ID, id)
	if err != nil || !j.ProtectionRequired || j.State != "dispatched" || j.Lifecycle.ExpectedDeleteMarker != nil || j.ProtectionVerified || j.DeletionVerified {
		t.Fatal("upgrade invented proof or discarded dispatch", j, err)
	}
	f.expire(id)
	out, err := (DeletionService{Store: f.reopen(), Provider: p}).Recover(t.Context(), f.bucket, id)
	if err == nil || out.State != "dispatched" || !out.RecoveryClaimed || out.DeletionVerified || n.deletes.Load() != 0 || n.requests.Load() != 0 {
		t.Fatal("unknown historical classification authorized deletion", out, err)
	}
}
