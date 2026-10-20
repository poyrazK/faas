// adr: 20261018161833
package state_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type scheduleTestStore interface {
	state.OperationScheduleStore
	CreateAccount(ctx context.Context, email string, plan api.Plan) (state.Account, error)
	CreateApp(ctx context.Context, a state.App) (state.App, error)
	CreateDeployment(ctx context.Context, d state.Deployment) (state.Deployment, error)
	MarkDeploymentLive(ctx context.Context, id string) error
	CreatePlatformTenant(ctx context.Context, accountID, externalRef, name string, limit int) (state.PlatformTenant, bool, error)
	SetPlatformTenantStatus(ctx context.Context, accountID, tenantID, status string) (state.PlatformTenant, error)
	PutOperationDefinition(ctx context.Context, def state.OperationDefinition) (state.OperationDefinition, error)
	OperationByID(ctx context.Context, accountID, tenantID, id string) (state.Operation, error)
}

type scheduleFixture struct {
	store         scheduleTestStore
	account       string
	tenant, other string
	customer      string
}

func newScheduleFixture(t *testing.T, store scheduleTestStore, plan api.Plan) scheduleFixture {
	t.Helper()
	ctx := t.Context()
	suffix := uuid.NewString()[:8]
	acct, err := store.CreateAccount(ctx, "schedules-"+suffix+"@example.com", plan)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "schedules-" + suffix, Type: state.AppTypeApp, RAMMB: 128, MaxConcurrency: 1, IdleTimeoutS: 30})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, ImageDigest: "sha256:schedules", Status: state.DeployPending})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MarkDeploymentLive(ctx, dep.ID); err != nil {
		t.Fatal(err)
	}
	f := scheduleFixture{store: store, account: acct.ID}
	for _, ref := range []string{"alice", "bob"} {
		tenant, _, err := store.CreatePlatformTenant(ctx, acct.ID, ref+"-"+suffix, ref, 100)
		if err != nil {
			t.Fatal(err)
		}
		if f.tenant == "" {
			f.tenant = tenant.ID
		} else {
			f.other = tenant.ID
		}
	}
	def, err := store.PutOperationDefinition(ctx, state.OperationDefinition{AccountID: acct.ID, OperationDefinitionResponse: api.OperationDefinitionResponse{
		AppID: app.ID, DeploymentID: dep.ID, Scope: dep.Scope, Spec: api.OperationDefinitionSpec{
			Name: "export", Method: "POST", Path: "/exports", Owner: api.OperationOwnerPlatformTenant,
			InputSchema:  []byte(`{"type":"object","properties":{"n":{"type":"integer"}},"additionalProperties":false}`),
			OutputSchema: []byte(`{"type":"object"}`), ProgressStages: []string{"generating"}}}})
	if err != nil {
		t.Fatal(err)
	}
	f.customer = def.ID
	return f
}

func (f scheduleFixture) create(t *testing.T, now time.Time, req api.OperationScheduleRequest) (state.OperationSchedule, error) {
	t.Helper()
	if req.DefinitionID == "" {
		req.DefinitionID = f.customer
	}
	return f.store.CreateOperationSchedule(t.Context(), state.OperationScheduleInput{AccountID: f.account, PlatformTenantID: f.tenant, Request: req, Now: now})
}

// claim claims due schedules and requires exactly ids among them.
func (f scheduleFixture) claim(t *testing.T, now time.Time, ids ...string) map[string]state.OperationSchedule {
	t.Helper()
	claimed, err := f.store.ClaimDueOperationSchedules(t.Context(), now, 100, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]state.OperationSchedule{}
	for _, s := range claimed {
		out[s.ID] = s
	}
	for _, id := range ids {
		if _, ok := out[id]; !ok || len(out) != len(ids) {
			t.Fatalf("claimed %d schedules at %s, want %v", len(out), now, ids)
		}
	}
	return out
}

func (f scheduleFixture) claimOne(t *testing.T, now time.Time, id string) state.OperationSchedule {
	t.Helper()
	return f.claim(t, now, id)[id]
}

func runScheduleStores(t *testing.T, plan api.Plan, test func(t *testing.T, f scheduleFixture)) {
	t.Run("mem", func(t *testing.T) { test(t, newScheduleFixture(t, state.NewMemStore(), plan)) })
	t.Run("pg", func(t *testing.T) {
		s, _ := pgStore(t)
		test(t, newScheduleFixture(t, s, plan))
	})
}

func TestOperationScheduleCreateValidates(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(time.Hour)
	past := now.Add(-time.Minute)
	far := now.Add(api.OperationScheduleOnceHorizon + time.Hour)
	runScheduleStores(t, api.PlanHobby, func(t *testing.T, f scheduleFixture) {
		for _, tc := range []struct {
			name  string
			req   api.OperationScheduleRequest
			want  error
			limit string
		}{
			{name: "neither at nor cron", req: api.OperationScheduleRequest{}, want: state.ErrInvalidArgument},
			{name: "both at and cron", req: api.OperationScheduleRequest{At: &at, Cron: "0 * * * *"}, want: state.ErrInvalidArgument},
			{name: "past at", req: api.OperationScheduleRequest{At: &past}, want: state.ErrInvalidArgument},
			{name: "beyond the horizon", req: api.OperationScheduleRequest{At: &far}, want: state.ErrInvalidArgument},
			{name: "bad cron", req: api.OperationScheduleRequest{Cron: "61 * * * *"}, want: state.ErrInvalidArgument},
			{name: "bad timezone", req: api.OperationScheduleRequest{Cron: "0 * * * *", Timezone: "Mars/Olympus"}, want: state.ErrInvalidArgument},
			{name: "faster than the plan allows", req: api.OperationScheduleRequest{Cron: "*/2 * * * *"}, limit: "schedule_min_interval_seconds"},
			{name: "input outside the schema", req: api.OperationScheduleRequest{At: &at, Input: []byte(`{"n":"x"}`)}, want: state.ErrInvalidArgument},
			{name: "unknown definition", req: api.OperationScheduleRequest{DefinitionID: uuid.NewString(), At: &at}, want: state.ErrNotFound},
		} {
			t.Run(tc.name, func(t *testing.T) {
				_, err := f.create(t, now, tc.req)
				var limit *state.OperationLimitError
				switch {
				case tc.limit != "":
					if !errors.As(err, &limit) || limit.Kind != tc.limit {
						t.Fatalf("err = %v, want limit %s", err, tc.limit)
					}
				case !errors.Is(err, tc.want):
					t.Fatalf("err = %v, want %v", err, tc.want)
				}
			})
		}
		s, err := f.create(t, now, api.OperationScheduleRequest{Cron: "30 9 * * 1", Timezone: "Europe/Istanbul", Input: []byte(`{"n":1}`)})
		if err != nil {
			t.Fatal(err)
		}
		if s.Kind != api.OperationScheduleCron || s.State != api.OperationScheduleActive || s.Timezone != "Europe/Istanbul" || s.NextFireAt == nil ||
			s.NextFireAt.Weekday() != time.Monday || s.NextFireAt.Hour() != 6 || s.NextFireAt.Minute() != 30 {
			t.Fatalf("cron schedule = %+v next=%v", s.OperationScheduleResponse, s.NextFireAt)
		}
	})
}

func TestOperationSchedulePlanLimits(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(time.Hour)
	t.Run("free plan has no schedules", func(t *testing.T) {
		store := state.NewMemStore()
		acct, err := store.CreateAccount(t.Context(), "schedules-free@example.com", api.PlanFree)
		if err != nil {
			t.Fatal(err)
		}
		_, err = store.CreateOperationSchedule(t.Context(), state.OperationScheduleInput{AccountID: acct.ID, PlatformTenantID: uuid.NewString(),
			Request: api.OperationScheduleRequest{DefinitionID: uuid.NewString(), At: &at}, Now: now})
		var limit *state.OperationLimitError
		if !errors.As(err, &limit) || limit.Kind != "schedules_per_account" {
			t.Fatalf("free plan schedule: %v", err)
		}
	})
	runScheduleStores(t, api.PlanHobby, func(t *testing.T, f scheduleFixture) {
		max := api.MustLimitsFor(api.PlanHobby).Operations.SchedulesPerAccount
		var first state.OperationSchedule
		for i := 0; i < max; i++ {
			s, err := f.create(t, now, api.OperationScheduleRequest{At: &at})
			if err != nil {
				t.Fatalf("schedule %d: %v", i, err)
			}
			if i == 0 {
				first = s
			}
		}
		_, err := f.create(t, now, api.OperationScheduleRequest{At: &at})
		var limit *state.OperationLimitError
		if !errors.As(err, &limit) || limit.Kind != "schedules_per_account" || limit.Limit != int64(max) {
			t.Fatalf("schedule over the plan: %v", err)
		}
		if _, err := f.store.ChangeOperationSchedule(t.Context(), f.account, "", first.ID, state.OperationScheduleCancel, now); err != nil {
			t.Fatal(err)
		}
		if _, err := f.create(t, now, api.OperationScheduleRequest{At: &at}); err != nil {
			t.Fatalf("cancelled schedule still counted: %v", err)
		}
	})
}

func TestOperationScheduleFireOnce(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(time.Minute)
	runScheduleStores(t, api.PlanPro, func(t *testing.T, f scheduleFixture) {
		ctx := t.Context()
		s, err := f.create(t, now, api.OperationScheduleRequest{At: &at, Input: []byte(`{"n":7}`)})
		if err != nil {
			t.Fatal(err)
		}
		if claimed, err := f.store.ClaimDueOperationSchedules(ctx, now, 100, time.Minute); err != nil || len(claimed) != 0 {
			t.Fatalf("claimed before due: %d %v", len(claimed), err)
		}
		fireNow := at.Add(time.Second)
		claimed := f.claimOne(t, fireNow, s.ID)
		if again, err := f.store.ClaimDueOperationSchedules(ctx, fireNow, 100, time.Minute); err != nil || len(again) != 0 {
			t.Fatalf("leased schedule claimed twice: %d %v", len(again), err)
		}
		stale := claimed
		stale.LeaseToken = uuid.NewString()
		if _, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: stale, Now: fireNow}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("fire with a foreign lease: %v", err)
		}
		fired, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: claimed, Now: fireNow})
		if err != nil {
			t.Fatal(err)
		}
		if fired.State != api.OperationScheduleCompleted || fired.NextFireAt != nil || fired.Fires != 1 || fired.LastOperationID == "" ||
			fired.LastError != "" || fired.LastFiredAt == nil || !fired.LastFiredAt.Equal(at) {
			t.Fatalf("fired once schedule = %+v", fired.OperationScheduleResponse)
		}
		op, err := f.store.OperationByID(ctx, f.account, f.tenant, fired.LastOperationID)
		if err != nil {
			t.Fatal(err)
		}
		if op.DefinitionID != f.customer || op.PlatformTenantID != f.tenant {
			t.Fatalf("admitted operation = %+v", op)
		}
		if _, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: claimed, Now: fireNow}); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("completed schedule fired again: %v", err)
		}
	})
}

func TestOperationScheduleFireCronCollapsesMissedFires(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 30, 0, time.UTC)
	runScheduleStores(t, api.PlanPro, func(t *testing.T, f scheduleFixture) {
		ctx := t.Context()
		s, err := f.create(t, now, api.OperationScheduleRequest{Cron: "*/5 * * * *"})
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 10, 18, 12, 5, 0, 0, time.UTC); !s.NextFireAt.Equal(want) {
			t.Fatalf("first fire = %s, want %s", s.NextFireAt, want)
		}
		// apid was down for 23 minutes: one fire, then the next future time.
		late := time.Date(2026, 10, 18, 12, 28, 10, 0, time.UTC)
		fired, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: f.claimOne(t, late, s.ID), Now: late})
		if err != nil {
			t.Fatal(err)
		}
		if want := time.Date(2026, 10, 18, 12, 30, 0, 0, time.UTC); fired.State != api.OperationScheduleActive || fired.Fires != 1 || !fired.NextFireAt.Equal(want) {
			t.Fatalf("after a late fire = %+v, want next %s", fired.OperationScheduleResponse, want)
		}
		// The firer's apid gate refused this fire: recorded, not admitted, still advancing.
		next := time.Date(2026, 10, 18, 12, 30, 1, 0, time.UTC)
		refused, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: f.claimOne(t, next, s.ID), Refusal: "customer is not admitted", Now: next})
		if err != nil {
			t.Fatal(err)
		}
		if refused.State != api.OperationScheduleActive || refused.Fires != 1 || refused.LastError != "customer is not admitted" || refused.LastOperationID != fired.LastOperationID {
			t.Fatalf("refused fire = %+v", refused.OperationScheduleResponse)
		}
	})
}

func TestOperationScheduleRefusalsAreRecorded(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(time.Minute)
	runScheduleStores(t, api.PlanPro, func(t *testing.T, f scheduleFixture) {
		ctx := t.Context()
		once, err := f.create(t, now, api.OperationScheduleRequest{At: &at})
		if err != nil {
			t.Fatal(err)
		}
		cron, err := f.create(t, now, api.OperationScheduleRequest{Cron: "* * * * *"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.store.SetPlatformTenantStatus(ctx, f.account, f.tenant, state.PlatformTenantSuspended); err != nil {
			t.Fatal(err)
		}
		fireNow := at.Add(time.Second)
		claimed := f.claim(t, fireNow, once.ID, cron.ID)
		for _, s := range []state.OperationSchedule{once, cron} {
			fired, err := f.store.FireOperationSchedule(ctx, state.OperationScheduleFire{Schedule: claimed[s.ID], Now: fireNow})
			if err != nil {
				t.Fatal(err)
			}
			wantState := api.OperationScheduleFailed
			if s.Kind == api.OperationScheduleCron {
				wantState = api.OperationScheduleActive
			}
			if fired.State != wantState || fired.Fires != 0 || fired.LastOperationID != "" || fired.LastError != "customer is suspended" {
				t.Fatalf("%s fire for a suspended customer = %+v", s.Kind, fired.OperationScheduleResponse)
			}
		}
		if _, err := f.create(t, now, api.OperationScheduleRequest{At: &at}); !errors.Is(err, state.ErrPlatformTenantSuspended) {
			t.Fatalf("schedule created for a suspended customer: %v", err)
		}
	})
}

func TestOperationScheduleChanges(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	at := now.Add(time.Hour)
	runScheduleStores(t, api.PlanPro, func(t *testing.T, f scheduleFixture) {
		ctx := t.Context()
		cron, err := f.create(t, now, api.OperationScheduleRequest{Cron: "0 * * * *"})
		if err != nil {
			t.Fatal(err)
		}
		once, err := f.create(t, now, api.OperationScheduleRequest{At: &at})
		if err != nil {
			t.Fatal(err)
		}
		change := func(tenant, id, action string) (state.OperationSchedule, error) {
			return f.store.ChangeOperationSchedule(ctx, f.account, tenant, id, action, now)
		}
		if _, err := change(f.other, cron.ID, state.OperationSchedulePause); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("another customer paused the schedule: %v", err)
		}
		if _, err := f.store.OperationSchedule(ctx, f.account, f.other, cron.ID); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("another customer read the schedule: %v", err)
		}
		if list, err := f.store.ListOperationSchedules(ctx, f.account, f.other, ""); err != nil || len(list) != 0 {
			t.Fatalf("another customer listed %d schedules: %v", len(list), err)
		}
		for _, step := range []struct {
			id, action, want string
			err              error
		}{
			{once.ID, state.OperationSchedulePause, "", state.ErrConflict},
			{cron.ID, state.OperationScheduleResume, "", state.ErrConflict},
			{cron.ID, "explode", "", state.ErrInvalidArgument},
			{cron.ID, state.OperationSchedulePause, api.OperationSchedulePaused, nil},
			{cron.ID, state.OperationScheduleResume, api.OperationScheduleActive, nil},
			{cron.ID, state.OperationSchedulePause, api.OperationSchedulePaused, nil},
			{cron.ID, state.OperationScheduleCancel, api.OperationScheduleCancelled, nil},
			{cron.ID, state.OperationScheduleResume, "", state.ErrConflict},
			{once.ID, state.OperationScheduleCancel, api.OperationScheduleCancelled, nil},
		} {
			got, err := change(f.tenant, step.id, step.action)
			if step.err != nil {
				if !errors.Is(err, step.err) {
					t.Fatalf("%s %s: err = %v, want %v", step.action, step.id, err, step.err)
				}
				continue
			}
			if err != nil || got.State != step.want || (got.State == api.OperationScheduleActive) != (got.NextFireAt != nil) {
				t.Fatalf("%s: %+v %v", step.action, got.OperationScheduleResponse, err)
			}
		}
		list, err := f.store.ListOperationSchedules(ctx, f.account, "", "")
		if err != nil || len(list) != 2 || list[0].ID == list[1].ID || list[0].State != api.OperationScheduleCancelled || list[1].State != api.OperationScheduleCancelled {
			t.Fatalf("account list = %d %v", len(list), err)
		}
		if claimed, err := f.store.ClaimDueOperationSchedules(ctx, now.Add(48*time.Hour), 100, time.Minute); err != nil || len(claimed) != 0 {
			t.Fatalf("cancelled schedules claimed: %d %v", len(claimed), err)
		}
	})
}
