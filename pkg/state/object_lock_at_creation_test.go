// adr: 20261119065000
package state_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type objectLockAtCreationStore interface {
	state.ObjectBucketStore
	state.ObjectBucketObjectLockStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateApp(context.Context, state.App) (state.App, error)
}

func TestObjectLockAtCreationMem(t *testing.T) {
	objectLockAtCreationSuite(t, func(*testing.T) objectLockAtCreationStore { return state.NewMemStore() })
}

func TestObjectLockAtCreationPG(t *testing.T) {
	objectLockAtCreationSuite(t, func(t *testing.T) objectLockAtCreationStore { st, _ := pgStore(t); return st })
}

// A bucket created with Object Lock journals the accepted request in its
// reservation, so provisioning and recovery send the provider's creation
// flag; a plan without Object Lock gets no bucket; a retry asking for Object
// Lock never returns a bucket created without it.
func objectLockAtCreationSuite(t *testing.T, open func(*testing.T) objectLockAtCreationStore) {
	for _, tc := range []struct {
		name         string
		plan         api.Plan
		lock         bool
		wantErr      error
		wantRequired bool
	}{
		{"plain bucket", api.PlanPro, false, nil, false},
		{"object lock bucket", api.PlanPro, true, nil, true},
		{"plain bucket on free", api.PlanFree, false, nil, false},
		{"object lock refused on free", api.PlanFree, true, state.ErrObjectLockPlan, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			st := open(t)
			a, app := seedObjectLockAtCreationApp(t, st, tc.plan)
			b, err := st.ReserveObjectBucket(ctx, newObjectLockAtCreationBucket(a.ID, app.ID, tc.lock), 10)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("ReserveObjectBucket error = %v, want %v", err, tc.wantErr)
			}
			if tc.wantErr != nil {
				var limit *state.ObjectStorageLimitError
				if !errors.As(err, &limit) || limit.Kind != "lock_retention_days" {
					t.Fatalf("plan refusal %v names no limit", err)
				}
				if rows, err := st.ListObjectBuckets(ctx, a.ID, app.ID); err != nil || len(rows) != 0 {
					t.Fatalf("refused reservation left buckets %v (%v)", rows, err)
				}
				return
			}
			if b.State != "provisioning" {
				t.Fatalf("bucket state = %q", b.State)
			}
			j, err := st.GetObjectBucketObjectLock(ctx, a.ID, app.ID, b.ID)
			if err != nil || j.EnabledRequired != tc.wantRequired {
				t.Fatalf("journal EnabledRequired = %v (%v), want %v", j.EnabledRequired, err, tc.wantRequired)
			}
			if !tc.wantRequired {
				return
			}
			if j.State != "waiting" || j.Revision != 1 || j.DesiredConfiguration == nil || !j.DesiredConfiguration.Equal(api.ObjectBucketObjectLockConfiguration{Enabled: true}) {
				t.Fatalf("creation journal = %+v", j)
			}
			// The recovery worker waits for the provider bucket.
			due, err := st.DueObjectBucketObjectLock(ctx, api.ObjectBucketObjectLockBatch)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range due {
				if row.BucketID == b.ID {
					t.Fatal("provisioning bucket's journal is due")
				}
			}
			if _, err = st.ClaimObjectBucket(ctx, a.ID, app.ID, b.ID, "create", "provisioning"); err != nil {
				t.Fatal(err)
			}
			if err = st.FinishObjectBucket(ctx, b.ID, "create", "ready"); err != nil {
				t.Fatal(err)
			}
			if j, err = st.GetObjectBucketObjectLock(ctx, a.ID, app.ID, b.ID); err != nil || !j.EnabledRequired {
				t.Fatalf("ready bucket lost its creation request: %+v (%v)", j, err)
			}
			if due, err = st.DueObjectBucketObjectLock(ctx, api.ObjectBucketObjectLockBatch); err != nil || len(due) != 1 || due[0].BucketID != b.ID {
				t.Fatalf("ready bucket's creation request is not due: %v (%v)", due, err)
			}
		})
	}
	t.Run("retry asking for object lock", func(t *testing.T) {
		for _, tc := range []struct {
			name           string
			created, retry bool
			wantErr        error
		}{
			{"plain bucket", false, true, state.ErrConflict},
			{"object lock bucket", true, true, nil},
			{"object lock bucket without asking", true, false, nil},
		} {
			t.Run(tc.name, func(t *testing.T) {
				ctx := context.Background()
				st := open(t)
				a, app := seedObjectLockAtCreationApp(t, st, api.PlanPro)
				first, err := st.ReserveObjectBucket(ctx, newObjectLockAtCreationBucket(a.ID, app.ID, tc.created), 10)
				if err != nil {
					t.Fatal(err)
				}
				again, err := st.ReserveObjectBucket(ctx, newObjectLockAtCreationBucket(a.ID, app.ID, tc.retry), 10)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("retry error = %v, want %v", err, tc.wantErr)
				}
				if err == nil && again.ID != first.ID {
					t.Fatalf("retry reserved %s, want %s", again.ID, first.ID)
				}
			})
		}
	})
}

func seedObjectLockAtCreationApp(t *testing.T, st objectLockAtCreationStore, plan api.Plan) (state.Account, state.App) {
	t.Helper()
	ctx := context.Background()
	a, err := st.CreateAccount(ctx, "lock-"+uuid.NewString()+"@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: a.ID, Slug: "lock-" + uuid.NewString()[:8], Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	return a, app
}

func newObjectLockAtCreationBucket(account, app string, lock bool) state.ObjectBucket {
	return state.ObjectBucket{ID: uuid.NewString(), AccountID: account, AppID: app, Name: "vault", Scope: "default", Region: "us-east-1", BackendID: "external", BackendFingerprint: strings.Repeat("a", 64), PhysicalName: "gregale-" + uuid.NewString(), ObjectLockAtCreation: lock}
}
