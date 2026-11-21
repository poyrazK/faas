package taskrehearsal

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidsource"
	"github.com/onebox-faas/faas/pkg/rehearsalcommand"
	pgruntime "github.com/onebox-faas/faas/pkg/rehearsalpg/runtime"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/taskacl"
	"github.com/onebox-faas/faas/pkg/tasktoolrehearsal"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

type runnerPipeline struct {
	deployments map[string]state.Deployment
	builds      map[string]state.Build
	release     state.AppTask
}

func (p *runnerPipeline) BuildByID(_ context.Context, id string) (state.Build, error) {
	b, ok := p.builds[id]
	if !ok {
		return b, state.ErrNotFound
	}
	return b, nil
}

func (p *runnerPipeline) DeploymentByID(_ context.Context, id string) (state.Deployment, error) {
	d, ok := p.deployments[id]
	if !ok {
		return d, state.ErrNotFound
	}
	return d, nil
}

func (p *runnerPipeline) ReleaseAppTaskByDeployment(_ context.Context, id string) (state.AppTask, error) {
	if p.release.DeploymentID != id {
		return state.AppTask{}, state.ErrNotFound
	}
	return p.release, nil
}

func (p *runnerPipeline) CancelDeploymentTx(_ context.Context, id, _ string, _ state.CancelReason) (state.Deployment, []string, error) {
	d := p.deployments[id]
	if d.ID == "" || p.release.DeploymentID != id || p.release.Status != state.AppTaskSucceeded {
		return d, nil, state.ErrConflict
	}
	d.Status = state.DeployCancelled
	p.deployments[id] = d
	return d, nil, nil
}

func TestPostgresRealTaskRehearsalRunner(t *testing.T) {
	dsn := os.Getenv("FAAS_REHEARSAL_PG_TEST_ADMIN_URL")
	if dsn == "" {
		t.Skip("requires dedicated isolated PostgreSQL fixture cluster")
	}
	adminConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture administrator configuration")
	}
	backend, err := pgruntime.NewPostgres(t.Context(), dsn)
	if err != nil {
		t.Fatal(err)
	}
	admin, err := pgx.ConnectConfig(t.Context(), adminConfig)
	if err != nil {
		t.Fatal("cannot connect to fixture administrator")
	}
	defer func() { _ = admin.Close(t.Context()) }()
	starter := os.Getenv("FAAS_REHEARSAL_TASK_STARTER_DIR")

	for _, corrupt := range []bool{false, true} {
		name := "migration receipt grants runtime roles"
		if corrupt {
			name = "foreign receipt tears down without runtime builds"
		}
		t.Run(name, func(t *testing.T) {
			store := state.NewMemStore()
			account, err := store.CreateAccount(t.Context(), "rehearsal-runner@example.test", api.PlanPro)
			if err != nil {
				t.Fatal(err)
			}
			seal, err := age.GenerateX25519Identity()
			if err != nil {
				t.Fatal(err)
			}
			run := state.HostedTaskRehearsalRun{
				ID: uuid.NewString(), ReservedAt: time.Now().UTC().Truncate(time.Microsecond),
				ApprovalExpiresAt: time.Now().Add(time.Minute),
			}
			p := &Provisioner{
				AdmissionEnabled: true, Store: store, Postgres: pgruntime.NewService(store, backend),
				BuildApp: func(account state.Account, req api.CreateAppRequest, limits api.Limits) (state.App, *api.Problem) {
					return state.App{AccountID: account.ID, Slug: req.Slug, Type: state.AppTypeApp,
						Visibility: api.AppVisibility(req.Visibility), RAMMB: api.StandaloneAppDefaultRAMMB(limits), MaxConcurrency: 1}, nil
				},
				PersistEnvironment: func(ctx context.Context, account state.Account, app state.App, values map[string]string) error {
					for k, v := range values {
						body, err := secretbox.SealOneBound(seal.Recipient(), secretbox.Binding{AccountID: account.ID}, k, v, 4096)
						if err != nil {
							return err
						}
						if err := store.UpsertAppSecretWithClassInScope(ctx, account.ID, app.ID, api.DefaultEnvScope, k, seal.Recipient().String(), "", api.SecretClassEphemeral, body); err != nil {
							return err
						}
					}
					return nil
				},
			}
			identity, err := p.target(account, run.ID)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				ctx, stop := context.WithTimeout(context.WithoutCancel(t.Context()), cleanupTimeout)
				defer stop()
				if err := p.Cleanup(ctx, account.ID, run.ID); err != nil {
					t.Error(err)
				}
			})
			web, worker := []byte("web source"), []byte("worker source")
			pipeline := &runnerPipeline{deployments: map[string]state.Deployment{}, builds: map[string]state.Build{}}
			var submitted, removed int
			r := Runner{
				AdmissionEnabled: true, Store: pipeline,
				RemoveSource: func(_ context.Context, _ string) error { removed++; return nil },
				Submit: func(ctx context.Context, input BuildInput) (apidsource.EnqueueResult, error) {
					submitted++
					depID, buildID := apidsource.OperationIDs(input.OperationID, input.App.ID)
					d := state.Deployment{ID: depID, AppID: input.App.ID, Scope: api.DefaultEnvScope, Kind: state.DeploymentKindTarball,
						SourceSHA256: toolpublication.Digest(input.Archive), TrafficPercentExplicit: true, Status: state.DeploySnapshotting,
						RootfsKey: "simulated-rootfs", ImageDigest: "simulated-image"}
					switch input.App.ID {
					case identity.AppID(Migration):
						if string(input.Archive) != string(worker) || !input.Migration {
							return apidsource.EnqueueResult{}, errors.New("migration build source drifted")
						}
						migrationStarter := starter
						if corrupt {
							migrationStarter = ""
						}
						if err := rehearseTestMigration(ctx, store, adminConfig, seal, identity, migrationStarter); err != nil {
							return apidsource.EnqueueResult{}, err
						}
						d.ReleaseCommand = MigrationCommand()
						task, err := simulatedMigrationReceipt(identity, d)
						if err != nil {
							return apidsource.EnqueueResult{}, err
						}
						if !state.HostedTaskRehearsalMigrationRelease(task) {
							return apidsource.EnqueueResult{}, errors.New("command-only rehearsal migration was not recognized")
						}
						other := task
						other.AppID = uuid.NewString()
						if state.HostedTaskRehearsalMigrationRelease(other) {
							return apidsource.EnqueueResult{}, errors.New("ordinary release matched migration identity")
						}
						if corrupt {
							task.ID = uuid.NewString() // The closed receipt still names the original task.
						}
						pipeline.release = task
					case identity.AppID(Web), identity.AppID(Worker):
						if pipeline.deployments[pipeline.release.DeploymentID].Status != state.DeployCancelled || input.Migration {
							return apidsource.EnqueueResult{}, errors.New("runtime build preceded migration retirement")
						}
						want := worker
						if input.App.ID == identity.AppID(Web) {
							want = web
						}
						if string(input.Archive) != string(want) {
							return apidsource.EnqueueResult{}, errors.New("reviewed runtime build source drifted")
						}
						if _, err := store.GetAppSecret(ctx, account.ID, identity.AppID(Migration), migrationDatabaseKey); !errors.Is(err, state.ErrNotFound) {
							return apidsource.EnqueueResult{}, errors.New("runtime build could follow a live migration credential")
						}
						if _, err := store.GetAppSecret(ctx, account.ID, input.App.ID, databaseKey); err != nil {
							return apidsource.EnqueueResult{}, errors.New("runtime build began without its own database role")
						}
					default:
						return apidsource.EnqueueResult{}, errors.New("unexpected rehearsal build")
					}
					pipeline.deployments[depID] = d
					pipeline.builds[buildID] = state.Build{ID: buildID, DeploymentID: depID, Status: state.BuildSucceeded}
					return apidsource.EnqueueResult{DeploymentID: depID, BuildID: buildID}, nil
				},
			}
			got, err := r.Run(t.Context(), p, account, run, web, worker, runnerTestPolicy())
			if corrupt {
				if err == nil || got != (Execution{}) || submitted != 1 {
					t.Fatalf("invalid receipt advanced rehearsal: builds=%d, err=%v", submitted, err)
				}
			} else {
				if err != nil || got.MigrationTaskID != pipeline.release.ID || got.WebDeploymentID == "" || got.WorkerDeploymentID == "" || submitted != 3 {
					t.Fatalf("rehearsal did not finish three pinned builds: builds=%d, err=%v", submitted, err)
				}
				if starter != "" {
					for _, role := range []string{Web, Worker} {
						env, err := rehearsalTestEnvironment(t.Context(), store, adminConfig, seal, identity, role, databaseKey)
						if err != nil {
							t.Fatal(err)
						}
						if err := runRehearsalTestStarter(t.Context(), starter, env, "--input-type=module", "-e", startRehearsalTestRuntime, role); err != nil {
							t.Fatalf("start stock %s runtime with its rehearsal login: %v", role, err)
						}
					}
				}
				smokeCtx, cancel := context.WithCancel(t.Context())
				defer cancel()
				driver := &cancelledSmokeDriver{
					cancel: cancel, runID: run.ID, workerID: got.WorkerDeploymentID,
				}
				evidence, err := r.Smoke(smokeCtx, p, account, run, got, driver)
				if !errors.Is(err, context.Canceled) || !evidence.CleanupConfirmed {
					t.Fatalf("cancelled smoke must clean the owned rehearsal: %+v %v", evidence, err)
				}
			}
			if removed != 3 {
				t.Fatalf("expected cleanup of three deterministic source identities, got %d", removed)
			}
			assertDisposed(t, admin, store, identity)
			for _, role := range workloads {
				rows, err := store.ListAppSecretsInScope(t.Context(), account.ID, identity.AppID(role.name), api.DefaultEnvScope)
				if err != nil || len(rows) != 0 {
					t.Fatalf("rehearsal environment remains for %s: %v", role.name, err)
				}
			}
		})
	}
}

type cancelledSmokeDriver struct {
	tasktoolrehearsal.SmokeDriver
	cancel          context.CancelFunc
	runID, workerID string
}

func (d *cancelledSmokeDriver) SubmitReport(_ context.Context, scope tasktoolrehearsal.SmokeScope) (string, error) {
	if scope.RunID != d.runID || scope.WorkerDeploymentID != d.workerID || scope.Namespace != "rehearsal-"+d.runID {
		return "", errors.New("worker smoke scope drifted")
	}
	d.cancel()
	return "", context.Canceled
}

func (*cancelledSmokeDriver) VerifyCleanup(ctx context.Context, _ tasktoolrehearsal.SmokeScope) error {
	return ctx.Err()
}

func runnerTestPolicy() tasktoolbinding.Policy {
	return tasktoolbinding.Policy{WebDatabaseEnv: databaseKey, WorkerDatabaseEnv: databaseKey,
		WebOwnerEnv: "MCP_TASK_OWNER_KEY", WorkerOwnerEnv: "MCP_TASK_OWNER_KEY",
		WebNamespaceEnv: namespaceKey, WorkerNamespaceEnv: namespaceKey, OwnerKeyID: "default"}
}

func rehearsalTestEnvironment(ctx context.Context, store *state.MemStore, admin *pgx.ConnConfig, seal *age.X25519Identity, p *Prepared, role, databaseEnv string) (map[string]string, error) {
	rows, err := store.ListAppSecretsInScope(ctx, p.account.ID, p.AppID(role), api.DefaultEnvScope)
	if err != nil {
		return nil, err
	}
	env := make(map[string]string, len(rows))
	for _, row := range rows {
		values, binding, err := secretbox.OpenMultiBound([]*age.X25519Identity{seal}, row.Ciphertext)
		if err != nil || binding.AccountID != p.account.ID || len(values) != 1 || values[row.Key] == "" {
			return nil, errors.New("could not read the bound rehearsal test environment")
		}
		env[row.Key] = values[row.Key]
	}
	databaseURL, err := url.Parse(env[databaseEnv])
	if err != nil || databaseURL.Scheme != "postgres" && databaseURL.Scheme != "postgresql" || databaseURL.User == nil {
		return nil, errors.New("invalid rehearsal test database binding")
	}
	databaseURL.Host = net.JoinHostPort(admin.Host, strconv.Itoa(int(admin.Port)))
	env[databaseEnv] = databaseURL.String()
	return env, nil
}

func rehearseTestMigration(ctx context.Context, store *state.MemStore, admin *pgx.ConnConfig, seal *age.X25519Identity, p *Prepared, starter string) error {
	env, err := rehearsalTestEnvironment(ctx, store, admin, seal, p, Migration, migrationDatabaseKey)
	if err != nil {
		return err
	}
	if starter != "" {
		return runRehearsalTestStarter(ctx, starter, env, MigrationCommand()[1:]...)
	}
	connection, err := pgx.Connect(ctx, env[migrationDatabaseKey])
	if err != nil {
		return errors.New("could not open migration database")
	}
	defer func() { _ = connection.Close(ctx) }()
	for _, table := range taskacl.ReadTables(false) {
		// Controlled test fixture. Production executes the reviewed guest script.
		if _, err := connection.Exec(ctx, "CREATE TABLE public."+pgx.Identifier{table}.Sanitize()+" (item text)"); err != nil {
			return errors.New("could not install controlled Task migration fixture")
		}
	}
	if _, err := connection.Exec(ctx, "CREATE SEQUENCE public.gregale_mcp_task_claim_order_seq"); err != nil {
		return errors.New("could not install controlled Task claim sequence")
	}
	return nil
}

func runRehearsalTestStarter(ctx context.Context, starter string, env map[string]string, args ...string) error {
	command := exec.CommandContext(ctx, "node", args...)
	command.Dir = starter
	command.Env = make([]string, 0, len(env))
	for key, value := range env {
		command.Env = append(command.Env, key+"="+value)
	}
	if err := command.Run(); err != nil {
		return fmt.Errorf("Task starter command failed: %w", err)
	}
	return nil
}

const startRehearsalTestRuntime = `
import { readFileSync } from 'node:fs';
import { startMcpTaskRuntime } from './task-runtime.js';
const config = JSON.parse(readFileSync('gregale-mcp.json', 'utf8'));
const runtime = await startMcpTaskRuntime(config, { role: process.argv[1] });
if (!runtime.taskRuntime) throw new Error('Task runtime did not start');
await runtime.close();
`

func simulatedMigrationReceipt(p *Prepared, d state.Deployment) (state.AppTask, error) {
	argv, err := rehearsalcommand.ContainedRelease(p.scenarioID(), d.ID, d.ReleaseCommand, false)
	if err != nil {
		return state.AppTask{}, err
	}
	spec, err := rehearsalcommand.Decode(argv)
	if err != nil {
		return state.AppTask{}, err
	}
	taskID := uuid.NewString()
	result, err := (rehearsalcommand.Receipt{Version: rehearsalcommand.Version, TaskID: taskID, SpecSHA256: spec.SHA256()}).Marshal()
	exit := 0
	return state.AppTask{ID: taskID, AccountID: p.account.ID, AppID: d.AppID, DeploymentID: d.ID, DeploymentScope: d.Scope,
		Kind: state.AppTaskKindRelease, Command: argv, ArtifactKey: d.RootfsKey, ImageDigest: d.ImageDigest,
		Status: state.AppTaskSucceeded, ExitCode: &exit, StdoutTail: string(result)}, err
}
