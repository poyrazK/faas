package state_test

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// previewEnvironmentStore is the PR preview surface the environment
// allowance contract (ADR-20261026090000) runs against on both stores.
type previewEnvironmentStore interface {
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateProject(context.Context, state.Project) (state.Project, error)
	AppByID(context.Context, string) (state.App, error)
	ClosePRPreview(context.Context, string, time.Time) (state.App, error)
	state.PRPreviewSetStore
	state.PRPreviewSetBatchStore
	state.PRPreviewSetupFailureStore
	state.PRPreviewReopenStore
}

type previewEnvironmentFixture struct {
	store   previewEnvironmentStore
	account state.Account
	project state.Project
	prefix  string
}

func newPreviewEnvironmentFixture(t *testing.T, ctx context.Context, store previewEnvironmentStore, plan api.Plan) previewEnvironmentFixture {
	t.Helper()
	prefix := fmt.Sprintf("env%d", time.Now().UnixNano()%1_000_000_000)
	account, err := store.CreateAccount(ctx, prefix+"@example.test", plan)
	if err != nil {
		t.Fatal(err)
	}
	project, err := store.CreateProject(ctx, state.Project{AccountID: account.ID, Slug: prefix})
	if err != nil {
		t.Fatal(err)
	}
	return previewEnvironmentFixture{store: store, account: account, project: project, prefix: prefix}
}

func (f previewEnvironmentFixture) preview(pr int, workload string) state.App {
	expires := time.Now().Add(time.Hour)
	parent := f.prefix + "-" + workload
	return state.App{AccountID: f.account.ID, ProjectID: f.project.ID, Slug: fmt.Sprintf("pr-%d-%s", pr, parent),
		WorkloadName: workload, PreviewOfSlug: parent, PreviewPrNumber: pr, PreviewPrState: state.PreviewPrStateOpen,
		PreviewExpiresAt: &expires, RAMMB: 128, Status: state.AppActive}
}

func (f previewEnvironmentFixture) head(pr int, sha byte) state.PRPreviewHead {
	return state.PRPreviewHead{InstallationID: 7, RepoFullName: "octo/" + f.prefix, PRNumber: pr, CommitSHA: strings.Repeat(string(sha), 40)}
}

func (f previewEnvironmentFixture) reserve(ctx context.Context, pr int, sha byte, limits api.Limits, workloads ...string) ([]state.App, error) {
	apps := make([]state.App, 0, len(workloads))
	for _, workload := range workloads {
		apps = append(apps, f.preview(pr, workload))
	}
	return f.store.ReservePRPreviewSet(ctx, f.head(pr, sha), apps, limits)
}

func (f previewEnvironmentFixture) close(t *testing.T, ctx context.Context, pr int, apps []state.App) {
	t.Helper()
	for _, app := range apps {
		if _, err := f.store.ClosePRPreview(ctx, app.ID, time.Now().Add(state.PRPreviewClosedGrace)); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.store.ClosePRPreviewSet(ctx, 7, "octo/"+f.prefix, pr); err != nil {
		t.Fatal(err)
	}
}

func quotaKind(err error) state.QuotaErrorKind {
	var quota *state.QuotaError
	if errors.As(err, &quota) {
		return quota.Kind
	}
	return ""
}

func runPreviewEnvironmentAllowanceContract(t *testing.T, newStore func(t *testing.T) (previewEnvironmentStore, context.Context)) {
	t.Run("closed preview frees its environment", func(t *testing.T) {
		store, ctx := newStore(t)
		f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanFree)
		limits := api.MustLimitsFor(api.PlanFree)
		first, err := f.reserve(ctx, 42, 'a', limits, "api")
		if err != nil {
			t.Fatal(err)
		}
		f.close(t, ctx, 42, first)
		second, err := f.reserve(ctx, 43, 'b', limits, "api")
		if err != nil {
			t.Fatalf("PR #43 after PR #42 closed: %v, want the closed preview's environment free", err)
		}
		if _, err := f.reserve(ctx, 42, 'c', limits, "api"); quotaKind(err) != state.QuotaErrorKindPreviewApps {
			t.Fatalf("reopening PR #42 beside PR #43 = %v, want the environment allowance", err)
		}
		if _, err := store.ReopenPRPreview(ctx, first[0].ID, time.Now().Add(time.Hour), limits); quotaKind(err) != state.QuotaErrorKindPreviewApps {
			t.Fatalf("API reopen of PR #42 = %v, want the environment allowance", err)
		}
		f.close(t, ctx, 43, second)
		reopened, err := store.ReopenPRPreview(ctx, first[0].ID, time.Now().Add(time.Hour), limits)
		if err != nil || reopened.PreviewPrState != state.PreviewPrStateOpen {
			t.Fatalf("API reopen with a free environment = (%+v, %v)", reopened, err)
		}
	})
	t.Run("an environment takes one allowance", func(t *testing.T) {
		store, ctx := newStore(t)
		f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanHobby)
		limits := api.Limits{Plan: api.PlanHobby, DeployedApps: 5, PreviewApps: 2}
		for _, pr := range []int{1, 2} {
			if _, err := f.reserve(ctx, pr, 'a', limits, "api", "worker", "db"); err != nil {
				t.Fatalf("PR #%d with three workloads: %v", pr, err)
			}
		}
		if _, err := f.reserve(ctx, 3, 'a', limits, "api"); quotaKind(err) != state.QuotaErrorKindPreviewApps {
			t.Fatalf("third environment = %v, want the environment allowance", err)
		}
		if _, err := f.reserve(ctx, 1, 'b', limits, "api", "worker", "db", "cache"); err != nil {
			t.Fatalf("growing an open environment: %v, want it admitted without a new allowance", err)
		}
	})
	t.Run("an environment holds at most the deployable workloads", func(t *testing.T) {
		store, ctx := newStore(t)
		f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanHobby)
		limits := api.Limits{Plan: api.PlanHobby, DeployedApps: 2, PreviewApps: 5}
		_, err := f.reserve(ctx, 1, 'a', limits, "api", "worker", "db")
		var quota *state.QuotaError
		if !errors.As(err, &quota) || quota.Kind != state.QuotaErrorKindPreviewWorkloads || quota.Limit != 2 {
			t.Fatalf("three workloads under a two-app plan = %v, want the workload limit", err)
		}
		if _, err := store.GetPRPreviewSet(ctx, 7, "octo/"+f.prefix, 1); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("refused environment recorded a set: %v", err)
		}
	})
	t.Run("a setup failure fails the head until the next one", func(t *testing.T) {
		store, ctx := newStore(t)
		f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanPro)
		limits := api.MustLimitsFor(api.PlanPro)
		if err := store.FailPRPreviewSetup(ctx, f.head(9, 'a'), "unrecorded"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failure without a set = %v, want ErrNotFound", err)
		}
		reserved, err := f.reserve(ctx, 9, 'a', limits, "api")
		if err != nil {
			t.Fatal(err)
		}
		if err := store.FailPRPreviewSetup(ctx, f.head(9, 'b'), "  the plan\n allows   1  "); err != nil {
			t.Fatal(err)
		}
		set, err := store.GetPRPreviewSet(ctx, 7, "octo/"+f.prefix, 9)
		if err != nil || set.CommitSHA != strings.Repeat("b", 40) || set.SetupError != "the plan allows 1" ||
			len(set.MemberAppIDs) != 1 || set.MemberAppIDs[0] != reserved[0].ID {
			t.Fatalf("failed set = (%+v, %v), want head b, the reason and the old members", set, err)
		}
		if _, err := f.reserve(ctx, 9, 'c', limits, "api"); err != nil {
			t.Fatal(err)
		}
		if set, err := store.GetPRPreviewSet(ctx, 7, "octo/"+f.prefix, 9); err != nil || set.SetupError != "" || set.CommitSHA != strings.Repeat("c", 40) {
			t.Fatalf("next head = (%+v, %v), want the failure cleared", set, err)
		}
		f.close(t, ctx, 9, reserved)
		if err := store.FailPRPreviewSetup(ctx, f.head(9, 'd'), "late"); !errors.Is(err, state.ErrNotFound) {
			t.Fatalf("failure on a closed set = %v, want ErrNotFound", err)
		}
	})
	t.Run("reserving a head refreshes gates", func(t *testing.T) {
		store, ctx := newStore(t)
		f := newPreviewEnvironmentFixture(t, ctx, store, api.PlanPro)
		limits := api.MustLimitsFor(api.PlanPro)
		reserved, err := f.reserve(ctx, 5, 'a', limits, "api")
		if err != nil {
			t.Fatal(err)
		}
		gated := f.preview(5, "api")
		gated.RequireSigned, gated.Visibility = true, api.AppVisibilityInternal
		gated.PublicAuthIPAllowlist = []netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}
		gated.EgressAllowlist = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
		if _, err := store.ReservePRPreviewSet(ctx, f.head(5, 'b'), []state.App{gated}, limits); err != nil {
			t.Fatal(err)
		}
		app, err := store.AppByID(ctx, reserved[0].ID)
		if err != nil || !app.RequireSigned || app.Visibility != api.AppVisibilityInternal ||
			len(app.PublicAuthIPAllowlist) != 1 || len(app.EgressAllowlist) != 1 {
			t.Fatalf("reused preview = (%+v, %v), want the production app's gates", app, err)
		}
	})
}

func TestMemStorePreviewEnvironmentAllowance(t *testing.T) {
	runPreviewEnvironmentAllowanceContract(t, func(*testing.T) (previewEnvironmentStore, context.Context) {
		return state.NewMemStore(), context.Background()
	})
}
