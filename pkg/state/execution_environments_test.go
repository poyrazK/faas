package state_test

import (
	"context"
	"errors"
	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/runenv"
	"github.com/onebox-faas/faas/pkg/state"
	"strings"
	"testing"
	"time"
)

type environmentContractStore interface {
	state.ExecutionEnvironmentStore
	CreateAccount(context.Context, string, api.Plan) (state.Account, error)
	CreateExecution(context.Context, state.CreateExecutionParams) (state.Execution, error)
	RequestExecutionCancellation(context.Context, string, string, time.Time) (state.Execution, error)
	ClaimExecutionForClass(context.Context, string, string, api.ExecutionClass, time.Time, time.Duration) (state.ExecutionClaim, error)
}

func environmentFixture(account string) state.ExecutionEnvironment {
	req := api.CreateExecutionEnvironmentRequest{Runtime: api.ExecutionRuntimeNode22, PackageJSON: `{"dependencies":{"pkg":"1.0.0"}}`, PackageLock: `{"lockfileVersion":3,"packages":{"":{},"node_modules/pkg":{"resolved":"https://registry.npmjs.org/pkg/-/pkg-1.0.0.tgz","integrity":"sha512-` + strings.Repeat("A", 86) + `=="}}}`}
	base := "registry.example/node@sha256:" + strings.Repeat("a", 64)
	digest, _ := runenv.Digest(req, base)
	return state.ExecutionEnvironment{ID: uuid.NewString(), AccountID: account, Runtime: req.Runtime, Recipe: req, RecipeDigest: digest, BaseRef: base}
}
func TestMemRunEnvironmentAdmissionRetirementAndGC(t *testing.T) {
	exerciseEnvironmentLifecycle(t, state.NewMemStore())
}
func exerciseEnvironmentLifecycle(t *testing.T, store environmentContractStore) {
	t.Helper()
	ctx := context.Background()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	env := environmentFixture(acct.ID)
	created, err := store.CreateExecutionEnvironment(ctx, env)
	if err != nil || created.Status != "queued" {
		t.Fatalf("create %+v %v", created, err)
	}
	duplicate := env
	duplicate.ID = uuid.NewString()
	replay, err := store.CreateExecutionEnvironment(ctx, duplicate)
	if err != nil || replay.ID != created.ID {
		t.Fatal("recipe did not reuse environment", err)
	}
	params := executionTestParams(t, acct.ID, time.Now().UTC(), 0, "sealed")
	params.Request.EnvironmentID = env.ID
	params.FilesAccountWide = true
	if _, err := store.CreateExecution(ctx, params); !errors.Is(err, state.ErrExecutionEnvironmentUnavailable) {
		t.Fatalf("unready admission: %v", err)
	}
	build, err := store.ClaimExecutionEnvironment(ctx, "build")
	if err != nil || build.Lease == "" || len(build.ArtifactKeys) != 1 {
		t.Fatalf("build claim %+v %v", build, err)
	}
	stale := build
	stale.Lease = uuid.NewString()
	if err := store.PublishExecutionEnvironmentBuild(ctx, stale, strings.Repeat("b", 64)); !errors.Is(err, state.ErrExecutionEnvironmentLease) {
		t.Fatalf("stale publisher: %v", err)
	}
	if err := store.PublishExecutionEnvironmentBuild(ctx, build, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	image, err := store.ClaimExecutionEnvironment(ctx, "image")
	if err != nil || len(image.ArtifactKeys) != 6 {
		t.Fatalf("image claim %+v %v", image, err)
	}
	if err := store.PublishExecutionEnvironmentImage(ctx, image, strings.Repeat("c", 64), strings.Repeat("d", 64), 128<<20); err != nil {
		t.Fatal(err)
	}
	params.AdmittedAt = time.Now().UTC()
	params.DeadlineAt = params.AdmittedAt.Add(time.Duration(params.Request.Limits.TimeoutMS) * time.Millisecond)
	run, err := store.CreateExecution(ctx, params)
	if err != nil || run.EnvironmentID != env.ID || run.RuntimeImageDigest != "sha256:"+strings.Repeat("c", 64) {
		t.Fatalf("pinned admission %+v %v", run, err)
	}
	if err := store.RetireExecutionEnvironment(ctx, acct.ID, env.ID, uuid.NewString(), false); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("other key family retired environment", err)
	}
	if err := store.RetireExecutionEnvironment(ctx, acct.ID, env.ID, "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateExecution(ctx, params); !errors.Is(err, state.ErrExecutionEnvironmentUnavailable) {
		t.Fatalf("retired admission: %v", err)
	}
	if err := store.DeleteExecutionEnvironmentArtifacts(ctx, env.ID); !errors.Is(err, state.ErrExecutionEnvironmentLease) {
		t.Fatalf("active environment deletion: %v", err)
	}
	garbage, err := store.CollectableExecutionEnvironments(ctx)
	if err != nil || len(garbage) != 0 {
		t.Fatalf("active Run lost environment %+v %v", garbage, err)
	}
	if _, err := store.RequestExecutionCancellation(ctx, acct.ID, run.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	garbage, err = store.CollectableExecutionEnvironments(ctx)
	if err != nil || len(garbage) != 1 || len(garbage[0].ArtifactKeys) != 6 {
		t.Fatalf("retained GC obligations %+v %v", garbage, err)
	}
	if err := store.DeleteExecutionEnvironmentArtifacts(ctx, env.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.ExecutionEnvironmentByID(ctx, acct.ID, env.ID); !errors.Is(err, state.ErrNotFound) {
		t.Fatalf("deleted environment remains visible: %v", err)
	}
}
func TestMemExtendedClassHasSeparateLiveQuota(t *testing.T) {
	store := state.NewMemStore()
	acct := executionTestAccount(t, store, "extended-quota")
	params := executionTestParams(t, acct.ID, time.Now().UTC(), 0, "sealed")
	params.Request.Limits.Class = api.ExecutionClassExtended
	params.Request.Limits.TimeoutMS = 300000
	params.DeadlineAt = params.AdmittedAt.Add(300 * time.Second)
	if _, err := store.CreateExecution(context.Background(), params); err != nil {
		t.Fatal(err)
	}
	if _, err := store.CreateExecution(context.Background(), params); err == nil {
		t.Fatal("extended cap exceeded")
	}
	standard := executionTestParams(t, acct.ID, time.Now().UTC(), 0, "sealed")
	if _, err := store.CreateExecution(context.Background(), standard); err != nil {
		t.Fatal("extended cap consumed every standard slot", err)
	}
}

func TestMemExecutionClassClaimsStayInTheirLane(t *testing.T) {
	exerciseClassClaims(t, state.NewMemStore())
}
func exerciseClassClaims(t *testing.T, store environmentContractStore) {
	t.Helper()
	ctx := t.Context()
	acct, err := store.CreateAccount(ctx, uuid.NewString()+"@class.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	long := executionTestParams(t, acct.ID, at, 0, "sealed")
	long.Request.Limits.Class = api.ExecutionClassExtended
	long.Request.Limits.TimeoutMS = 300000
	long.DeadlineAt = at.Add(300 * time.Second)
	extended, err := store.CreateExecution(ctx, long)
	if err != nil {
		t.Fatal(err)
	}
	short := executionTestParams(t, acct.ID, at, 0, "sealed")
	standard, err := store.CreateExecution(ctx, short)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	got, err := store.ClaimExecutionForClass(ctx, acct.ID, "class-worker", api.ExecutionClassStandard, now, time.Second)
	if err != nil || got.ID != standard.ID {
		t.Fatalf("standard claim: %+v %v", got, err)
	}
	got, err = store.ClaimExecutionForClass(ctx, acct.ID, "class-worker", api.ExecutionClassExtended, now, time.Second)
	if err != nil || got.ID != extended.ID {
		t.Fatalf("extended claim: %+v %v", got, err)
	}
	if _, err = store.ClaimExecutionForClass(ctx, acct.ID, "class-worker", api.ExecutionClassStandard, now, time.Second); !errors.Is(err, state.ErrNotFound) {
		t.Fatal("duplicate class claim", err)
	}
}
