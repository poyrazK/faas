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
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 552
func TestObjectNotificationMigrationPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	raw, err := migrations.FS.ReadFile("20261004090600471_object_bucket_notifications.sql")
	if err != nil {
		t.Fatal(err)
	}
	down := strings.Split(string(raw), "-- +goose Down")[1]
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, down); err != nil {
		t.Fatal("empty downgrade", err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	b, _ := seedAccounting(t, st)
	for _, query := range []string{`INSERT INTO object_bucket_notifications(bucket_id,revision,rules) VALUES ($1,0,'[]')`, `INSERT INTO object_bucket_notifications(bucket_id,revision,rules) VALUES ($1,1,'{}')`} {
		if _, err = pool.Exec(ctx, query, b.ID); err == nil {
			t.Fatal("configuration constraint bypassed")
		}
	}
	rule := api.ObjectNotificationRule{ID: "images", Destination: "arn:gregale:lambda:" + b.Region + ":" + b.AccountID + ":function:" + b.AppID, Events: []string{"s3:ObjectCreated:Put"}}
	if _, err = st.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{rule}); err != nil {
		t.Fatal(err)
	}
	checkBlocked := func(reason string) {
		t.Helper()
		tx, e := pool.Begin(ctx)
		if e != nil {
			t.Fatal(e)
		}
		_, e = tx.Exec(ctx, down)
		if e == nil {
			t.Fatal("unsafe downgrade", reason)
		}
		if e = tx.Rollback(ctx); e != nil {
			t.Fatal(e)
		}
	}
	checkBlocked("active configuration")
	c, err := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "images/a.jpg", Status: "pending", Bytes: 1}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag = "completed", `"done"`
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	if _, err = st.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	checkBlocked("accepted delivery survives clearing")
}

func TestObjectNotificationPublicationRollbackPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b, _ := seedAccounting(t, st)
	rule := api.ObjectNotificationRule{ID: "images", Destination: "arn:gregale:lambda:" + b.Region + ":" + b.AccountID + ":function:" + b.AppID, Events: []string{"s3:ObjectCreated:Put"}}
	if _, err := st.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{rule}); err != nil {
		t.Fatal(err)
	}
	c, err := st.BeginTrackedGatewayUpload(ctx, state.ObjectUploadCompletion{ID: uuid.NewString(), AccountID: b.AccountID, AppID: b.AppID, BucketID: b.ID, SubjectID: "owner", Key: "images/a.jpg", Status: "pending", Bytes: 1}, accountingPolicy())
	if err != nil {
		t.Fatal(err)
	}
	c, err = st.DispatchTrackedObjectUpload(ctx, b.AccountID, b.ID, c.ID)
	if err != nil {
		t.Fatal(err)
	}
	before, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	// Force capture to fail after the ordinary event/outbox insert has executed.
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_notifications SET rules='[{"id":""}]' WHERE bucket_id=$1`, b.ID); err != nil {
		t.Fatal(err)
	}
	c.Status, c.ETag, c.ProviderVersionID = "completed", `"done"`, "provider-private-notification"
	if _, err = st.FinishTrackedObjectUpload(ctx, c); !errors.Is(err, state.ErrObjectNotificationInvalid) {
		t.Fatal("capture error committed success", err)
	}
	receipt, err := st.GetObjectWriteReceipt(ctx, b.AccountID, b.AppID, b.ID, c.ID)
	if err != nil || receipt.Status != "pending" {
		t.Fatal(receipt, err)
	}
	after, err := st.ObjectUsage(ctx, b.AccountID, time.Now())
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("capture error changed accounting", before, after, err)
	}
	status, err := st.ObjectVersionAccountingStatus(ctx, b.AccountID, b.ID)
	if err != nil || status.VersionsObserved {
		t.Fatal("capture error recorded version proof", status, err)
	}
	noObjectEvent(t, st)
	encoded, err := json.Marshal([]api.ObjectNotificationRule{rule})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_notifications SET rules=$2 WHERE bucket_id=$1`, b.ID, encoded); err != nil {
		t.Fatal(err)
	}
	if _, err = st.FinishTrackedObjectUpload(ctx, c); err != nil {
		t.Fatal(err)
	}
	_, _, work := takeObjectEvent(t, st, b.AccountID, "write:"+c.ID, api.ObjectEventCreated)
	if len(work.RecipientSnapshot) != 1 || work.RecipientSnapshot[0].ObjectNotification == nil {
		t.Fatal("recovered publication omitted destination", work)
	}
}
