package computetriggers_test

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

// adr: 1036
func TestObjectComputeTriggersMem(t *testing.T) { objectComputeTriggersSuite(t, state.NewMemStore()) }
func TestObjectComputeTriggersPG(t *testing.T) {
	st, _ := pgStore(t)
	objectComputeTriggersSuite(t, st)
}

func objectComputeTriggersSuite(t *testing.T, st triggerFixtureStore) {
	t.Helper()
	b := seedTriggerBucket(t, st)
	ctx := t.Context()
	s := st.(state.ObjectComputeTriggerStore)
	manual := api.ObjectNotificationRule{ID: "manual", Events: []string{"s3:ObjectCreated:Put"}, Prefix: "manual/", Destination: "arn:gregale:lambda:" + b.Region + ":" + uuid.MustParse(b.AccountID).String() + ":function:" + uuid.MustParse(b.AppID).String()}
	p, err := s.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{manual})
	if err != nil {
		t.Fatal(err)
	}
	request := api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &p.Revision, Triggers: []api.ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/", Suffix: ".png"}}}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
	if err != nil || p.Revision != 2 || len(p.Rules) != 2 || len(p.ManagedRuleIDs) != 1 || !reflect.DeepEqual(p.Rules[0], manual) {
		t.Fatal(p, err)
	}
	// Input/output cannot mutate the store, and IDs survive unchanged deployments.
	id := p.ManagedRuleIDs[0]
	p.ManagedRuleIDs[0] = "evil"
	p.Rules[1].Events[0] = "evil"
	current, err := s.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || current.ManagedRuleIDs[0] != id || current.Rules[1].Events[0] != "s3:ObjectCreated:*" {
		t.Fatal(current, err)
	}
	request.ExpectedRevision = &current.Revision
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
	if err != nil || p.Revision != 2 || p.ManagedRuleIDs[0] != id {
		t.Fatal("not idempotent", p, err)
	}
	stale := int64(1)
	request.ExpectedRevision = &stale
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request); !errors.Is(err, state.ErrConflict) {
		t.Fatal("stale no-op", err)
	}
	request.ExpectedRevision = &p.Revision
	request.Triggers = []api.ObjectStorageComputeTrigger{{ID: "overlap", Prefix: "manual/"}}
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request); !errors.Is(err, state.ErrObjectNotificationInvalid) {
		t.Fatal("overlap", err)
	}
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, uuid.NewString(), b.AppID, b.ID, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign account", err)
	}
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, uuid.NewString(), b.ID, request); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("foreign app", err)
	}
	// Concurrent replacements must have one winner, even when both are valid.
	request.Triggers = []api.ObjectStorageComputeTrigger{{ID: "new", Prefix: "new/"}}
	var winners atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, e := s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
			if e == nil {
				winners.Add(1)
			} else if !errors.Is(e, state.ErrConflict) {
				t.Errorf("reconcile: %v", e)
			}
		}()
	}
	wg.Wait()
	if winners.Load() != 1 {
		t.Fatal("CAS winners", winners.Load())
	}
	p, err = s.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || p.Revision != 3 {
		t.Fatal(p, err)
	}
	request.ExpectedRevision = &p.Revision
	request.Triggers = []api.ObjectStorageComputeTrigger{}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
	if err != nil || p.Revision != 4 || len(p.ManagedRuleIDs) != 0 || len(p.Rules) != 1 || !reflect.DeepEqual(p.Rules[0], manual) {
		t.Fatal("clear", p, err)
	}
	request.ExpectedRevision = &p.Revision
	request.Triggers = []api.ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/"}}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
	if err != nil {
		t.Fatal(err)
	}
	// An identical full manual replacement takes ownership. Later deploys cannot adopt it.
	p, err = s.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, p.Rules)
	if err != nil || p.Revision != 6 || len(p.ManagedRuleIDs) != 0 {
		t.Fatal("manual takeover", p, err)
	}
	request.ExpectedRevision = &p.Revision
	if _, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request); !errors.Is(err, state.ErrConflict) {
		t.Fatal("manual ID overwritten", err)
	}
	request.Triggers = []api.ObjectStorageComputeTrigger{}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, request)
	if err != nil || p.Revision != 6 || len(p.Rules) != 2 {
		t.Fatal("clear erased manual", p, err)
	}
}

func TestObjectComputeTriggerOwnershipMigrationPG(t *testing.T) {
	st, pool, ctx := pgStoreWithPool(t)
	b := seedTriggerBucket(t, st)
	s := state.ObjectComputeTriggerStore(st)
	revision := int64(0)
	p, err := s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &revision, Triggers: []api.ObjectStorageComputeTrigger{{ID: "images", Prefix: "uploads/"}}})
	if err != nil {
		t.Fatal(err)
	}
	restored := state.NewPgStore(pool)
	read, err := restored.GetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID)
	if err != nil || !reflect.DeepEqual(p, read) {
		t.Fatal("restart ownership", read, err)
	}
	for _, bad := range []string{`{}`, `null`, `[{}]`, `[{"id":"wrong"}]`, `[{"id":"` + p.ManagedRuleIDs[0] + `"}]`} {
		if _, err = pool.Exec(ctx, `UPDATE object_bucket_notifications SET managed_rules=$2::jsonb WHERE bucket_id=$1`, b.ID, bad); err == nil {
			t.Fatal("accepted bad ownership", bad)
		}
	}
	duplicate, _ := json.Marshal([]api.ObjectNotificationRule{p.Rules[0], p.Rules[0]})
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_notifications SET managed_rules=$2::jsonb WHERE bucket_id=$1`, b.ID, duplicate); err == nil {
		t.Fatal("duplicate ownership")
	}
	if _, err = pool.Exec(ctx, `UPDATE object_bucket_notifications SET rules='[]' WHERE bucket_id=$1`, b.ID); err == nil {
		t.Fatal("legacy writer erased managed rule")
	}
	data, err := migrations.FS.ReadFile("20261011214416456_object_compute_trigger_ownership.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(data), "-- +goose Down", 2)
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("replay", err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err == nil {
		t.Fatal("downgrade lost ownership")
	}
	if _, err = s.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, parts[1]); err != nil {
		t.Fatal("downgrade after clear", err)
	}
	if _, err = pool.Exec(ctx, parts[0]); err != nil {
		t.Fatal("restore", err)
	}
}

// Independent package exercises complete production stores without compiling
// unrelated test suites. Each PostgreSQL test uses its own migrated schema.
type triggerFixtureStore interface {
	state.Store
	state.ObjectBucketStore
}

func pgStore(t *testing.T) (*state.PgStore, context.Context) {
	st, _, ctx := pgStoreWithPool(t)
	return st, ctx
}
func pgStoreWithPool(t *testing.T) (*state.PgStore, *pgxpool.Pool, context.Context) {
	t.Helper()
	pool := pgtest.OpenMigrated(t)
	ctx := t.Context()
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	return state.NewPgStore(pool), pool, ctx
}
func seedTriggerBucket(t *testing.T, st triggerFixtureStore) state.ObjectBucket {
	t.Helper()
	ctx := t.Context()
	account, err := st.CreateAccount(ctx, "trigger-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "trigger-" + uuid.NewString(), Type: state.AppTypeApp, Status: state.AppActive, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	b, err := st.ReserveObjectBucket(ctx, state.ObjectBucket{ID: uuid.NewString(), AccountID: account.ID, AppID: app.ID, Name: "assets", Scope: "default", Region: "us-east-1", BackendID: "fixture", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "private-" + uuid.NewString()}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = st.ClaimObjectBucket(ctx, account.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
		t.Fatal(err)
	}
	if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
		t.Fatal(err)
	}
	return b
}

func TestComputeTriggerRetiredManualTargetMem(t *testing.T) {
	computeTriggerRetiredManual(t, state.NewMemStore())
}
func TestComputeTriggerRetiredManualTargetPG(t *testing.T) {
	st, _ := pgStore(t)
	computeTriggerRetiredManual(t, st)
}
func computeTriggerRetiredManual(t *testing.T, st triggerFixtureStore) {
	t.Helper()
	b := seedTriggerBucket(t, st)
	ctx := t.Context()
	s := st.(state.ObjectComputeTriggerStore)
	target, err := st.CreateApp(ctx, state.App{ID: uuid.NewString(), AccountID: b.AccountID, Slug: "retired-" + uuid.NewString(), Status: state.AppActive, Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	manual := api.ObjectNotificationRule{ID: "manual", Events: []string{"s3:ObjectCreated:*"}, Prefix: "manual/", Destination: "arn:gregale:lambda:" + b.Region + ":" + uuid.MustParse(b.AccountID).String() + ":function:" + target.ID}
	p, err := s.SetObjectBucketNotifications(ctx, b.AccountID, b.AppID, b.ID, []api.ObjectNotificationRule{manual})
	if err != nil {
		t.Fatal(err)
	}
	p, err = s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &p.Revision, Triggers: []api.ObjectStorageComputeTrigger{{ID: "incoming", Prefix: "uploads/"}}})
	if err != nil {
		t.Fatal(err)
	}
	if err = st.DeleteApp(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	cleared, err := s.ReconcileObjectBucketComputeTriggers(ctx, b.AccountID, b.AppID, b.ID, api.ReconcileObjectStorageComputeTriggersRequest{ExpectedRevision: &p.Revision, Triggers: []api.ObjectStorageComputeTrigger{}})
	if err != nil || len(cleared.ManagedRuleIDs) != 0 || len(cleared.Rules) != 1 || !reflect.DeepEqual(cleared.Rules[0], manual) {
		t.Fatal("retired manual blocked clearing", cleared, err)
	}
}
