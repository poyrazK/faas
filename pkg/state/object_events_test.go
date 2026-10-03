package state_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/events"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 409
func TestObjectMutationEventsMem(t *testing.T) { objectMutationEvents(t, state.NewMemStore()) }
func TestObjectMutationEventsPG(t *testing.T)  { s, _ := pgStore(t); objectMutationEvents(t, s) }

func noObjectEvent(t *testing.T, st accountingStore) {
	t.Helper()
	if work, err := st.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(t.Context(), time.Now().Add(24*time.Hour)); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("unexpected event: %+v, %v", work, err)
	}
}

func takeObjectEvent(t *testing.T, st accountingStore, account, id, typ string) (events.Envelope, api.ObjectStorageEvent, *state.PublishedEventWork) {
	t.Helper()
	work, err := st.(state.PublishedEventWorkStore).ClaimDuePublishedEvent(t.Context(), time.Now().Add(24*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	var envelope events.Envelope
	if err = json.Unmarshal(work.Payload, &envelope); err != nil {
		t.Fatal(err)
	}
	if err = envelope.Validate(); err != nil {
		t.Fatal(err)
	}
	if envelope.AccountID != account || envelope.ID != id || envelope.Source != api.ObjectEventSource || envelope.Type != typ {
		t.Fatalf("wrong event: %+v", envelope)
	}
	var data api.ObjectStorageEvent
	if err = json.Unmarshal(envelope.Data, &data); err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"provider-private", "subject-private", "source-private", "request-private", "recovery_token", "backend_id", "physical_name"} {
		if strings.Contains(string(work.Payload), secret) {
			t.Fatalf("private field in payload: %s", work.Payload)
		}
	}
	if err = st.(state.PublishedEventWorkStore).FinishPublishedEvent(t.Context(), work.ID, work.ClaimToken, nil); err != nil {
		t.Fatal(err)
	}
	return envelope, data, work
}

func objectMutationEvents(t *testing.T, st accountingStore) {
	for _, tc := range []struct{ origin, operation, native string }{{"gateway", "put", ""}, {"gateway_copy", "copy", "null"}, {"route", "upload", "provider-private-version"}} {
		t.Run(tc.origin, func(t *testing.T) {
			b, _ := seedAccounting(t, st)
			sub, _, err := st.(state.EventSubscriptionStore).UpsertEventSubscription(t.Context(), b.AccountID, b.AppID, api.ObjectEventSource, "object.*", json.RawMessage(`{"data":{"key":{"$prefix":"images/","$suffix":".jpg"}}}`))
			if err != nil {
				t.Fatal(err)
			}
			writes := st.(state.ObjectTrackedGatewayCopyStore)
			c := state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "subject-private", RequestID: "request-private", Key: "images/a.jpg", Bytes: 1, Status: "pending"}
			switch tc.origin {
			case "gateway":
				c, err = writes.BeginTrackedGatewayUpload(t.Context(), c, accountingPolicy())
			case "gateway_copy":
				c.SourceKey, c.SourceETag = "source-private", `"source"`
				c, err = writes.BeginTrackedGatewayCopy(t.Context(), c, accountingPolicy())
			case "route":
				route, e := st.(state.ObjectUploadRouteStore).UpsertObjectUploadRoute(t.Context(), state.ObjectUploadRoute{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, Name: "upload", KeyPrefix: "images/", MaxBytes: 10, Enabled: true})
				if e != nil {
					t.Fatal(e)
				}
				c.RouteID = route.ID
				c, _, err = writes.BeginTrackedObjectUpload(t.Context(), c, accountingPolicy())
			}
			if err != nil {
				t.Fatal(err)
			}
			noObjectEvent(t, st)
			c, err = writes.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, c.ID)
			if err != nil {
				t.Fatal(err)
			}
			noObjectEvent(t, st)
			c.Status, c.ETag, c.ProviderVersionID = "completed", `"done"`, "bad\nversion"
			if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); !errors.Is(err, state.ErrConflict) {
				t.Fatal("invalid provider identity", err)
			}
			noObjectEvent(t, st)
			c.ProviderVersionID = tc.native
			// The record's immutable routing and size win over completion input.
			c.Key, c.Bytes = "forged", 99
			out, err := writes.FinishTrackedObjectUpload(t.Context(), c)
			if err != nil {
				t.Fatal(err)
			}
			if tc.native != "" && (!state.ValidObjectVersionID(out.VersionID) || out.VersionID == tc.native && tc.native != "null") {
				t.Fatal("invalid public completion selector", out)
			}
			if tc.native != "" {
				native, e := st.(state.ObjectVersionReferenceStore).ResolveObjectVersion(t.Context(), b.AccountID, b.ID, "images/a.jpg", out.VersionID)
				if e != nil || native != tc.native {
					t.Fatal(native, e)
				}
			}
			if replay, e := writes.FinishTrackedObjectUpload(t.Context(), c); e != nil || replay.VersionID != out.VersionID {
				t.Fatal(replay, e)
			}
			// Removing a subscription after acceptance must not remove its recipient.
			if err = st.(state.EventSubscriptionStore).DeleteEventSubscription(t.Context(), sub.ID, b.AccountID, b.AppID); err != nil {
				t.Fatal(err)
			}
			_, data, work := takeObjectEvent(t, st, b.AccountID, "write:"+c.ID, api.ObjectEventCreated)
			if data.Key != "images/a.jpg" || data.AppID != b.AppID || data.BucketID != b.ID || data.ReceiptID != c.ID || data.Operation != tc.operation || data.ETag != `"done"` || data.SizeBytes == nil || *data.SizeBytes != 1 || data.VersionID != out.VersionID {
				t.Fatal(data)
			}
			if !work.SnapshotCaptured || len(work.RecipientSnapshot) != 1 || work.RecipientSnapshot[0].ID != sub.ID {
				t.Fatal("acceptance snapshot lost", work)
			}
			noObjectEvent(t, st)
			ledger, err := st.ListEvents(t.Context(), b.AccountID, 100)
			if err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, row := range ledger {
				if row.Kind == "event.published" {
					count++
				}
			}
			if count != 1 {
				t.Fatal("duplicate publication", count)
			}
		})
	}
	b, _ := seedAccounting(t, st)
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	c, err := writes.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "failed", Status: "pending"}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ErrorCode = "failed", "dispatch_failed"
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); err != nil {
		t.Fatal(err)
	}
	noObjectEvent(t, st)
	// Direct URL settlement cannot establish a confirmed creation.
	direct := uuid.NewString()
	if err = st.(state.ObjectCapacityStore).BeginObjectWrite(t.Context(), b.AccountID, b.ID, direct, "direct", 1, accountingPolicy()); err != nil {
		t.Fatal(err)
	}
	if err = st.(state.ObjectCapacityStore).SettleObjectWrite(t.Context(), b.AccountID, b.ID, direct); err != nil {
		t.Fatal(err)
	}
	noObjectEvent(t, st)
}

func TestObjectMutationEventRollbackMem(t *testing.T) {
	objectMutationEventRollback(t, state.NewMemStore())
}
func TestObjectMutationEventRollbackPG(t *testing.T) {
	s, _ := pgStore(t)
	objectMutationEventRollback(t, s)
}
func objectMutationEventRollback(t *testing.T, st accountingStore) {
	b, _ := seedAccounting(t, st)
	writes := st.(state.ObjectTrackedGatewayUploadStore)
	c, err := writes.BeginTrackedGatewayUpload(t.Context(), state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "rollback", Bytes: 1, Status: "pending"}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c, err = writes.DispatchTrackedObjectUpload(t.Context(), b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	forged := events.Envelope{SpecVersion: "1.0", ID: "write:" + c.ID, Source: api.ObjectEventSource, Type: api.ObjectEventCreated, Time: time.Now(), DataContentType: "application/json", AccountID: b.AccountID, Data: json.RawMessage(`{"conflict":true}`)}
	payload, err := json.Marshal(forged)
	if err != nil {
		t.Fatal(err)
	}
	if err = st.AppendEvent(t.Context(), "test", "event.published", &b.AccountID, payload); err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(t.Context(), b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.ProviderVersionID = "completed", `"done"`, "provider-private-rollback"
	if _, err = writes.FinishTrackedObjectUpload(t.Context(), c); !errors.Is(err, state.ErrConflict) {
		t.Fatal("publication conflict committed completion", err)
	}
	receipt, err := st.(state.ObjectWriteReceiptStore).GetObjectWriteReceipt(t.Context(), b.AccountID, b.AppID, b.ID, c.ID)
	if err != nil || receipt.Status != "pending" {
		t.Fatal(receipt, err)
	}
	after, err := st.ObjectUsage(t.Context(), b.AccountID, time.Now())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("publication error changed accounting", before, after, err)
	}
	status, err := st.(state.ObjectVersionInventoryStore).ObjectVersionAccountingStatus(t.Context(), b.AccountID, b.ID)
	if err != nil || status.VersionsObserved {
		t.Fatal("publication error recorded native observation", status, err)
	}
	ledger, err := st.ListEvents(t.Context(), b.AccountID, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, row := range ledger {
		if row.Kind == "event.published" {
			count++
		}
	}
	if count != 1 {
		t.Fatal("rollback left an event", count)
	}
}

func TestObjectMultipartMutationEventsMem(t *testing.T) {
	objectMultipartMutationEvent(t, state.NewMemStore())
}
func TestObjectMultipartMutationEventsPG(t *testing.T) {
	s, _ := pgStore(t)
	objectMultipartMutationEvent(t, s)
}
func objectMultipartMutationEvent(t *testing.T, st accountingStore) {
	b, u := preparedMultipartResult(t, st, "multipart")
	results := st.(state.ObjectMultipartCompletionStore)
	if err := results.DispatchObjectMultipartCompletion(t.Context(), u); err != nil {
		t.Fatal(err)
	}
	noObjectEvent(t, st)
	out, err := results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`, ProviderVersionID: "provider-private-multipart"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = results.FinishObjectMultipartCompletion(t.Context(), u, state.ObjectMultipartCompletionResult{ETag: `"actual"`}); !errors.Is(err, state.ErrConflict) {
		t.Fatal(err)
	}
	_, data, _ := takeObjectEvent(t, st, b.AccountID, "multipart:"+u.ID, api.ObjectEventCreated)
	if data.Operation != "complete_multipart_upload" || data.VersionID != out.CompletionVersionID || data.SizeBytes == nil || *data.SizeBytes != 30 || data.ETag != out.CompletionETag {
		t.Fatal(data, out)
	}
	noObjectEvent(t, st)
}

func TestObjectDeletionMutationEventsMem(t *testing.T) {
	m := state.NewMemStore()
	now := time.Now()
	m.SetClockForTest(func() time.Time { return now })
	step := 0
	objectDeletionMutationEvents(t, m, func(string) {
		step++
		delay := api.ObjectBucketVersioningRetry
		if step%2 == 1 {
			delay = api.ObjectBucketVersioningPropagation
		}
		now = now.Add(delay + time.Second)
	})
}
func TestObjectDeletionMutationEventsPG(t *testing.T) {
	s, pool, ctx := pgStoreWithPool(t)
	objectDeletionMutationEvents(t, s, func(bucket string) {
		if _, err := pool.Exec(ctx, `UPDATE object_bucket_versioning SET retry_at=now(),propagation_until=now()-interval '1 second' WHERE bucket_id=$1`, bucket); err != nil {
			t.Fatal(err)
		}
	})
}
func objectDeletionMutationEvents(t *testing.T, st accountingStore, advance func(string)) {
	for _, tc := range []struct {
		status, selector, native string
		marker, lifecycle        bool
	}{{"", "", "", false, false}, {"Enabled", "", "provider-private-marker", true, false}, {"Suspended", "", "null", true, false}, {"", "immutable", "provider-private-target", false, false}, {"", "immutable", "provider-private-marker-target", true, false}, {"", "", "", false, true}} {
		t.Run(tc.status+tc.selector+tc.native, func(t *testing.T) {
			b, _ := seedAccounting(t, st)
			d := st.(state.ObjectDeletionStore)
			if tc.status != "" {
				configureObjectEventVersioning(t, st, b, tc.status, advance)
			}
			j := state.ObjectDeletion{ObjectDeletion: api.ObjectDeletion{ID: uuid.NewString(), BucketID: b.ID, Key: "logs/a"}, AccountID: b.AccountID, AppID: b.AppID, Token: "owner"}
			if tc.selector != "" {
				refs, err := st.(state.ObjectVersionReferenceStore).RecordObjectVersions(t.Context(), b.AccountID, b.ID, []state.ObjectVersionIdentity{{Key: j.Key, ProviderVersionID: tc.native, DeleteMarker: tc.marker}})
				if err != nil {
					t.Fatal(err)
				}
				j.Selector = refs[0].ID
			}
			if tc.lifecycle {
				days := int32(1)
				l := st.(state.ObjectLifecycleStore)
				if _, err := l.SetObjectBucketLifecycle(t.Context(), b.AccountID, b.AppID, b.ID, []api.ObjectLifecycleRule{{ID: "expire", Status: "Enabled", Filter: api.ObjectLifecycleFilter{Prefix: "logs/"}, Expiration: &api.ObjectLifecycleExpiration{Days: &days}}}); err != nil {
					t.Fatal(err)
				}
				scan, err := l.StartObjectLifecycleScan(t.Context(), b.AccountID, b.AppID, b.ID)
				if err != nil {
					t.Fatal(err)
				}
				scan, err = l.ClaimObjectLifecycleScan(t.Context(), scan.ID, "scan-owner")
				if err != nil {
					t.Fatal(err)
				}
				j.Lifecycle = &state.ObjectLifecycleDeletionBinding{ScanID: scan.ID, ScanToken: scan.Token, RuleID: "expire", Kind: "current", ExpectedProviderVersionID: "null", ExpectedLastModified: time.Now().Add(-5 * 24 * time.Hour)}
			}
			j, _, err := d.BeginObjectDeletion(t.Context(), j, accountingPolicy())
			if err != nil {
				t.Fatal(err)
			}
			j, err = d.DispatchObjectDeletion(t.Context(), j.ID, j.Token, tc.status, nil)
			if err != nil {
				t.Fatal(err)
			}
			noObjectEvent(t, st)
			j.State, j.ProviderVersionID, j.DeleteMarker = "completed", tc.native, tc.marker
			out, err := d.FinishObjectDeletion(t.Context(), j)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = d.FinishObjectDeletion(t.Context(), j); !errors.Is(err, state.ErrConflict) {
				t.Fatal(err)
			}
			typ := api.ObjectEventRemoved
			if tc.marker && tc.selector == "" {
				typ = api.ObjectEventDeleteMarkerCreated
			}
			_, data, _ := takeObjectEvent(t, st, b.AccountID, "delete:"+j.ID, typ)
			cause := "customer"
			if tc.lifecycle {
				cause = "lifecycle"
			}
			if data.Cause != cause || data.DeleteMarker != tc.marker || data.VersionID != out.VersionID || data.SizeBytes != nil {
				t.Fatal(data, out)
			}
			noObjectEvent(t, st)
		})
	}
}

func configureObjectEventVersioning(t *testing.T, st accountingStore, b state.ObjectBucket, status string, advance func(string)) {
	t.Helper()
	ctx := t.Context()
	v := st.(state.ObjectBucketVersioningStore)
	if _, err := v.RequestObjectBucketVersioning(ctx, b.AccountID, b.AppID, b.ID, status); err != nil {
		t.Fatal(err)
	}
	j, err := v.ClaimObjectBucketVersioning(ctx, b.ID, "apply")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.DispatchObjectBucketVersioning(ctx, b.ID, j.Token)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, status); err != nil {
		t.Fatal(err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "inventory")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, status)
	if err != nil {
		t.Fatal(err)
	}
	c, err := st.(state.ObjectCapacityStore).ClaimObjectCapacityReconciliation(ctx, j.CapacityJobID, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.(state.ObjectVersionInventoryStore).StageObjectVersionInventoryPage(ctx, c.ID, c.Token, "", nil); err != nil {
		t.Fatal(err)
	}
	advance(b.ID)
	j, err = v.ClaimObjectBucketVersioning(ctx, b.ID, "verify")
	if err != nil {
		t.Fatal(err)
	}
	j, err = v.AdvanceObjectBucketVersioning(ctx, b.ID, j.Token, status)
	if err != nil || j.State != "ready" {
		t.Fatal(j, err)
	}
}

func TestObjectMutationVersionMigrationPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	raw, err := migrations.FS.ReadFile("20261003130527920_object_mutation_events.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(raw), "-- +goose Down")[1]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal("empty downgrade failed", err)
	}
	var count int
	if err = tx.QueryRow(ctx, `SELECT count(*) FROM information_schema.columns WHERE table_name='object_upload_completions' AND column_name='version_id' AND table_schema=current_schema()`).Scan(&count); err != nil || count != 0 {
		t.Fatal("empty downgrade retained version column", count, err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := seedAccounting(t, st)
	c, err := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "version", Status: "pending", Bytes: 1}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET version_id='provider-private' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("native version bypassed migration constraint")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_upload_completions SET version_id='null' WHERE id=$1`, c.ID); err == nil {
		t.Fatal("pending receipt acquired result")
	}
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.ProviderVersionID = "completed", `"actual"`, "provider-private-migration"
	out, err := st.FinishTrackedObjectUpload(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	reopened := state.NewPgStore(pool)
	replay, err := reopened.FinishTrackedObjectUpload(ctx, c)
	if err != nil || replay.VersionID != out.VersionID {
		t.Fatal("reconstructed completion changed selector", out, replay, err)
	}
	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err == nil {
		t.Fatal("downgrade discarded durable result")
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	_, data, _ := takeObjectEvent(t, reopened, b.AccountID, "write:"+c.ID, api.ObjectEventCreated)
	if data.VersionID != out.VersionID {
		t.Fatal(data, out)
	}
	noObjectEvent(t, reopened)
}
