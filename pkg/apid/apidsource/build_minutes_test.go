package apidsource

// adr: 20261103131742 — a source build is refused once the app's billing
// subject has used its plan's monthly build minutes; an image deployment
// builds nothing and is never refused.

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// buildQuotaFake answers the build-minute quota's reads: one app, its
// account or organization, and the build seconds its subject used.
type buildQuotaFake struct {
	app       state.App
	account   state.Account
	org       state.Org
	seconds   int64
	readErr   error
	subject   string
	readStart time.Time
	readEnd   time.Time
}

func (f *buildQuotaFake) LatestDeployment(context.Context, string) (state.Deployment, error) {
	return state.Deployment{}, state.ErrNotFound
}

func (f *buildQuotaFake) CreateDeployment(context.Context, state.Deployment) (state.Deployment, error) {
	return state.Deployment{}, errors.New("not used")
}

func (f *buildQuotaFake) FailSourceDeployment(context.Context, string, string) error {
	return errors.New("not used")
}

func (f *buildQuotaFake) CreateBuildWithID(context.Context, string, string, state.DeploymentKind, int64, string) (state.Build, error) {
	return state.Build{}, errors.New("not used")
}

func (f *buildQuotaFake) AppByID(_ context.Context, id string) (state.App, error) {
	if id != f.app.ID {
		return state.App{}, state.ErrNotFound
	}
	return f.app, nil
}

func (f *buildQuotaFake) AccountByID(_ context.Context, id string) (state.Account, error) {
	if id != f.account.ID {
		return state.Account{}, state.ErrNotFound
	}
	return f.account, nil
}

func (f *buildQuotaFake) OrgByID(_ context.Context, id string) (state.Org, error) {
	if id != f.org.ID {
		return state.Org{}, state.ErrNotFound
	}
	return f.org, nil
}

func (f *buildQuotaFake) BillingSubjectBuildSeconds(_ context.Context, subjectID string, start, end time.Time) (int64, error) {
	f.subject, f.readStart, f.readEnd = subjectID, start, end
	return f.seconds, f.readErr
}

func TestBuildMinutesProblem(t *testing.T) {
	now := time.Date(2026, time.November, 4, 15, 0, 0, 0, time.UTC)
	month := time.Date(2026, time.November, 1, 0, 0, 0, 0, time.UTC)
	personal := func(plan api.Plan, seconds int64) *buildQuotaFake {
		return &buildQuotaFake{
			app:     state.App{ID: "app-1", AccountID: "acct-1"},
			account: state.Account{ID: "acct-1", Plan: plan},
			seconds: seconds,
		}
	}
	for _, tc := range []struct {
		name        string
		store       *buildQuotaFake
		kind        state.DeploymentKind
		wantRefused bool
		wantSubject string
	}{
		{"under the Free quota", personal(api.PlanFree, 299*60+59), state.DeploymentKindTarball, false, "acct-1"},
		{"at the Free quota", personal(api.PlanFree, 300*60), state.DeploymentKindTarball, true, "acct-1"},
		{"Free minutes on Hobby", personal(api.PlanHobby, 300*60), state.DeploymentKindGitHub, false, "acct-1"},
		{"past the Hobby quota", personal(api.PlanHobby, 1000*60), state.DeploymentKindDockerfile, true, "acct-1"},
		{"image deployments build nothing", personal(api.PlanFree, 9999*60), state.DeploymentKindImage, false, ""},
		{"unreadable minutes admit the build", func() *buildQuotaFake {
			f := personal(api.PlanFree, 9999*60)
			f.readErr = errors.New("db down")
			return f
		}(), state.DeploymentKindTarball, false, "acct-1"},
		{"an organization's apps count for the organization", &buildQuotaFake{
			app:     state.App{ID: "app-1", AccountID: "acct-1", OrgID: "org-1"},
			account: state.Account{ID: "acct-1", Plan: api.PlanScale},
			org:     state.Org{ID: "org-1", Plan: api.PlanFree, ResourceOwnershipEnabled: true},
			seconds: 300 * 60,
		}, state.DeploymentKindTarball, true, "org-1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			problem := buildMinutesProblem(context.Background(), tc.store, "app-1", tc.kind, now, nil)
			if got := problem != nil; got != tc.wantRefused {
				t.Fatalf("refused = %v (%+v), want %v", got, problem, tc.wantRefused)
			}
			if tc.store.subject != tc.wantSubject {
				t.Fatalf("read subject = %q, want %q", tc.store.subject, tc.wantSubject)
			}
			if tc.wantSubject != "" && (!tc.store.readStart.Equal(month) || !tc.store.readEnd.Equal(month.AddDate(0, 1, 0))) {
				t.Fatalf("read window = [%s, %s), want November", tc.store.readStart, tc.store.readEnd)
			}
			if problem == nil {
				return
			}
			if problem.Code != api.CodePlanLimitBuildMinutes || problem.Status != http.StatusForbidden {
				t.Fatalf("problem = %s %d, want %s 403", problem.Code, problem.Status, api.CodePlanLimitBuildMinutes)
			}
			if problem.Limit == nil || problem.Observed == nil {
				t.Fatalf("problem limit/observed = %v/%v, want both set", problem.Limit, problem.Observed)
			}
		})
	}
}
