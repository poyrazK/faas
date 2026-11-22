// Package taskrehearsal prepares disposable resources for a claimed hosted Task plan.
package taskrehearsal

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/rehearsalpg"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/rehearsalwork"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
)

const (
	Migration = tasktoolrehearsal.MigrationWorkload
	Web       = "web"
	Worker    = "worker"
	Observer  = "observer"

	leaseDuration  = tasktoolrehearsal.WorkloadLease
	cleanupTimeout = 30 * time.Second
	databaseKey    = "DATABASE_URL"
	// This platform-owned marker provides an initial capture on an otherwise
	// empty database. Only the contained release command may install Task schema.
	bootstrapSQL = "CREATE TABLE public.gregale_rehearsal_fixture (version integer PRIMARY KEY); INSERT INTO public.gregale_rehearsal_fixture VALUES (1);"
)

type workload struct {
	name, access string
	async        bool
}

var workloads = [...]workload{
	{name: Migration, access: rehearsalwork.PostgresReadWrite},
	{name: Web, access: rehearsalwork.PostgresTaskRuntime},
	{name: Worker, access: rehearsalwork.PostgresTaskRuntime, async: true},
	{name: Observer, access: rehearsalwork.PostgresTaskObserver},
}

type Store interface {
	CreateAppIfUnderQuota(context.Context, state.App, api.Limits) (state.App, error)
	AppByID(context.Context, string) (state.App, error)
	RegisterScenarioTestMembers(context.Context, string, string, []state.ScenarioTestMember) error
	ScenarioTestMemberByApp(context.Context, string) (state.ScenarioTestMember, error)
	DeleteAppSecret(context.Context, string, string, string) error
	ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
	SetPreviewPrState(context.Context, string, string) (state.App, error)
	SoftDeleteAppCascade(context.Context, string) (state.App, error)
}

// Provisioner uses existing scenario containment, quotas and the dedicated
// fixture cluster. The credential sink stores only sealed, app-local secrets.
type Provisioner struct {
	AdmissionEnabled   bool
	Store              Store
	Postgres           *pgruntime.Service
	BuildApp           func(state.Account, api.CreateAppRequest, api.Limits) (state.App, *api.Problem)
	PersistDatabaseURL func(context.Context, state.Account, state.App, string) error
	PersistEnvironment func(context.Context, state.Account, state.App, map[string]string) error
}

// Prepared is an in-process handoff to the contained release runner. Lost
// handoffs can be cleaned through the journal and cannot resume provisioning.
type Prepared struct {
	provisioner *Provisioner
	account     state.Account
	run         uuid.UUID
	expiresAt   time.Time
	binding     atomic.Bool
}

func (p *Prepared) Namespace() string { return "rehearsal-" + p.run.String() }

func (p *Prepared) AppID(name string) string {
	return tasktoolrehearsal.WorkloadID(p.run, name)
}

func (p *Prepared) scenarioID() string {
	return strings.ReplaceAll(p.run.String(), "-", "")
}

func (p *Prepared) slug(name string) string {
	return "task-" + p.scenarioID()[:24] + "-" + name
}

func (p *Prepared) marker() string {
	return "task-" + p.scenarioID()
}

func (p *Prepared) owns(app state.App, name string) bool {
	return app.AccountID == p.account.ID && app.ID == p.AppID(name) &&
		app.Slug == p.slug(name) && app.PreviewOfSlug == p.marker() &&
		app.PreviewPrNumber == 0 && app.Visibility == api.AppVisibilityInternal
}

// Provision is for the holder of a newly acquired private claim. App and
// fixture leases are bounded by the persisted reservation time.
func (p *Provisioner) Provision(ctx context.Context, account state.Account, run state.HostedTaskRehearsalRun) (*Prepared, error) {
	return p.provisionRun(ctx, account, run, nil)
}

func (p *Provisioner) provisionRun(ctx context.Context, account state.Account, run state.HostedTaskRehearsalRun, env *runnerEnvironment) (*Prepared, error) {
	if !p.AdmissionEnabled {
		return nil, state.ErrConflict
	}
	target, err := p.target(account, run.ID)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	if run.ReservedAt.IsZero() || run.ReservedAt.After(now) || !run.ApprovalExpiresAt.After(now) {
		return nil, state.ErrConflict
	}
	target.expiresAt = run.ReservedAt.Add(leaseDuration)
	setup, cancel := context.WithDeadline(ctx, run.ApprovalExpiresAt)
	defer cancel()
	if err := target.provision(setup, env); err != nil {
		cleanup, stop := context.WithTimeout(context.WithoutCancel(ctx), cleanupTimeout)
		defer stop()
		return nil, errors.Join(err, target.cleanup(cleanup))
	}
	return target, nil
}

func (p *Provisioner) target(account state.Account, runID string) (*Prepared, error) {
	id, err := uuid.Parse(runID)
	if err != nil || id == uuid.Nil || id.String() != runID || uuid.Validate(account.ID) != nil ||
		p.Store == nil || p.Postgres == nil {
		return nil, state.ErrInvalidArgument
	}
	return &Prepared{provisioner: p, account: account, run: id}, nil
}

func (p *Prepared) provision(ctx context.Context, env *runnerEnvironment) error {
	store := p.provisioner.Store
	limits, ok := api.LimitsFor(p.account.Plan)
	if !ok || p.provisioner.BuildApp == nil || (env == nil && p.provisioner.PersistDatabaseURL == nil) || (env != nil && p.provisioner.PersistEnvironment == nil) {
		return state.ErrInvalidArgument
	}
	members, err := p.members()
	if err != nil {
		return err
	}
	var migration state.App
	for _, member := range members {
		if err := ctx.Err(); err != nil {
			return err
		}
		deny := []string{}
		app, problem := p.provisioner.BuildApp(p.account, api.CreateAppRequest{
			Slug: p.slug(member.Workload), Visibility: string(api.AppVisibilityInternal),
			AllowedServiceCallers: &deny,
		}, limits)
		if problem != nil {
			return fmt.Errorf("configure rehearsal workload: %s", member.Workload)
		}
		app.ID, app.PreviewOfSlug = member.AppID, p.marker()
		app.PreviewPrState, app.PreviewExpiresAt = state.PreviewPrStateOpen, &p.expiresAt
		app, err = store.CreateAppIfUnderQuota(ctx, app, limits)
		if err != nil {
			return fmt.Errorf("create rehearsal %s workload: %w", member.Workload, err)
		}
		if member.Workload == Migration {
			migration = app
		}
	}
	if err := store.RegisterScenarioTestMembers(ctx, p.account.ID, p.scenarioID(), members); err != nil {
		return fmt.Errorf("enroll rehearsal workloads: %w", err)
	}
	req := rehearsalpg.CreateRequest{
		SQL: bootstrapSQL,
		Spec: rehearsalpg.Spec{
			Version: rehearsalpg.Version, FixtureSHA256: rehearsalpg.HashBytes([]byte(bootstrapSQL)),
			Tables:        []rehearsalpg.Table{{Schema: "public", Name: "gregale_rehearsal_fixture", Columns: []string{"version"}}},
			MaxLockWaitMS: 5000,
		},
	}
	_, connection, err := p.provisioner.Postgres.CreateUntil(ctx, p.account.ID, p.scenarioID(),
		migration.ID, Migration+".svc.gregale", req, p.expiresAt)
	if err != nil {
		return fmt.Errorf("provision rehearsal database: %w", err)
	}
	if err := p.persistConnection(ctx, migration, Migration, connection, env); err != nil {
		return errors.New("persist rehearsal migration credential")
	}
	return ctx.Err()
}

func (p *Prepared) members() ([]state.ScenarioTestMember, error) {
	names := make([]string, 0, len(workloads))
	plan := rehearsalwork.Plan{Version: rehearsalwork.Version, Primary: Migration}
	for _, role := range workloads {
		names = append(names, role.name)
		plan.Participants = append(plan.Participants, rehearsalwork.Participant{
			Workload: role.name, Postgres: role.access, Async: role.async,
		})
	}
	managedWork, _, err := rehearsalwork.Normalize(plan, names)
	if err != nil {
		return nil, err
	}
	simulation, _, err := api.NormalizeScenarioSimulationPlan(api.ScenarioSimulationPlan{
		Version: api.ScenarioSimulationVersion,
		Fixtures: []api.ScenarioSimulationFixture{{
			Workload: Migration, Name: "rehearsal", Integration: "rehearsal", Adapter: "http",
			Method: http.MethodGet, Path: "/rehearsal", Responses: []api.ScenarioSimulationResponse{{Status: http.StatusNoContent}},
		}},
	}, names)
	if err != nil {
		return nil, err
	}
	members := make([]state.ScenarioTestMember, 0, len(workloads))
	for _, role := range workloads {
		members = append(members, state.ScenarioTestMember{
			Workload: role.name, AppID: p.AppID(role.name), NetworkMode: state.ScenarioNetworkContained,
			WebhookMode: state.ScenarioWebhookCaptureMode, SimulationJSON: string(simulation), ManagedWorkJSON: string(managedWork),
		})
	}
	return members, nil
}
