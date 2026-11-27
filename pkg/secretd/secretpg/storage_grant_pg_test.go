//go:build !no_pg

// adr: 20261117190000
package secretpg_test

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/secretd"
	"github.com/onebox-faas/faas/pkg/secretd/secretpg"
	"github.com/onebox-faas/faas/pkg/state"
)

// The placement lookups behind GrantStorageObjects, and its audit.
func TestStorageGrantStoreOnPostgres(t *testing.T) {
	pool := pgtest.OpenMigrated(t)
	st := state.NewPgStore(pool)
	store := secretpg.New(pool, st)
	ctx := t.Context()

	newNode := func(prefix string) state.ComputeNode {
		t.Helper()
		n, err := st.CreateComputeNode(ctx, state.ComputeNode{
			Name: prefix + uuid.NewString()[:8], TargetURL: "unix:///run/faas/vmmd.sock",
			VPCPUs: 4, VCPUBudget: 160, MemMB: 8192, AdmissionCeilingMB: 8192, MaxConcurrency: 16, Active: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	node, other := newNode("grant-"), newNode("grant-other-")
	account, err := st.CreateAccount(ctx, uuid.NewString()+"@grants.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := st.CreateApp(ctx, state.App{AccountID: account.ID, Slug: "grants-" + uuid.NewString()[:8], RAMMB: 128, MaxConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dep, err := st.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindImage, Status: state.DeployLive, ImageDigest: "sha256:grants"})
	if err != nil {
		t.Fatal(err)
	}

	if got, err := store.DeploymentApp(ctx, dep.ID); err != nil || got != app.ID {
		t.Fatalf("deployment app = %q, %v", got, err)
	}
	if _, err := store.DeploymentApp(ctx, uuid.NewString()); !errors.Is(err, secretd.ErrUnknownDeployment) {
		t.Fatalf("unknown deployment = %v", err)
	}

	// An instance that is not over places the app; a stopped one does not.
	instance := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into instances (id, app_id, deployment_id, state, ram_mb, node_id) values ($1, $2, $3, 'parked', 128, $4)`,
		instance, app.ID, dep.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		node string
		want bool
	}{{node.ID, true}, {other.ID, false}} {
		if ok, err := store.NodeRunsApp(ctx, app.ID, tc.node); err != nil || ok != tc.want {
			t.Errorf("node %s runs app = %v, %v; want %v", tc.node, ok, err, tc.want)
		}
	}
	if _, err := pool.Exec(ctx, `update instances set state = 'stopped', terminal_at = now() where id = $1`, instance); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeRunsApp(ctx, app.ID, node.ID); err != nil || ok {
		t.Errorf("stopped instance still places the app: %v, %v", ok, err)
	}

	// A running build the node claimed.
	build := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into builds (id, deployment_id, kind, source_bytes, status, started_at) values ($1, $2, 'dockerfile', 1, 'running', now())`,
		build, dep.ID); err != nil {
		t.Fatal(err)
	}
	// A shared login records the claiming node the way builderd does.
	if err := st.RecordBuildNode(ctx, build, node.Name); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeBuildsDeployment(ctx, dep.ID, node.Name, time.Now().Add(-time.Hour)); err != nil || !ok {
		t.Errorf("claimed build = %v, %v", ok, err)
	}
	if ok, err := store.NodeBuildsDeployment(ctx, dep.ID, other.Name, time.Now().Add(-time.Hour)); err != nil || ok {
		t.Errorf("another node's build = %v, %v", ok, err)
	}
	if ok, err := store.NodeBuildsDeployment(ctx, dep.ID, node.Name, time.Now().Add(time.Hour)); err != nil || ok {
		t.Errorf("a build started before the window = %v, %v", ok, err)
	}

	// Malformed ids place nothing.
	if ok, err := store.NodeRunsApp(ctx, "not-a-uuid", node.ID); err != nil || ok {
		t.Errorf("malformed app = %v, %v", ok, err)
	}
	if ok, err := store.NodeRunsSandboxOf(ctx, account.ID, node.ID); err != nil || ok {
		t.Errorf("no sandbox session = %v, %v", ok, err)
	}
	// A session placed on the node but not yet up (placed_node_id), then
	// up (node_id); a closed one places nothing.
	session := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into sandbox_sessions (id, account_id, preset, ram_mb, idle_timeout_sec, expires_at, state, placed_node_id)
		values ($1, $2, 'base', 512, 60, now() + interval '1 hour', 'starting', $3)`, session, account.ID, node.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeRunsSandboxOf(ctx, account.ID, node.ID); err != nil || !ok {
		t.Errorf("placed session = %v, %v", ok, err)
	}
	if ok, err := store.NodeRunsSandboxOf(ctx, account.ID, other.ID); err != nil || ok {
		t.Errorf("another node's session = %v, %v", ok, err)
	}
	// The session boots a template the app builds: its node may read the
	// app's layer.
	template := uuid.NewString()
	if _, err := pool.Exec(ctx, `insert into sandbox_templates (id, account_id, name, app_id) values ($1, $2, 'tpl', $3)`,
		template, account.ID, app.ID); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeRunsSandboxTemplateOf(ctx, app.ID, node.ID); err != nil || ok {
		t.Errorf("template before the session names it = %v, %v", ok, err)
	}
	if _, err := pool.Exec(ctx, `update sandbox_sessions set template_id = $2 where id = $1`, session, template); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeRunsSandboxTemplateOf(ctx, app.ID, node.ID); err != nil || !ok {
		t.Errorf("session's template = %v, %v", ok, err)
	}
	if ok, err := store.NodeRunsSandboxTemplateOf(ctx, app.ID, other.ID); err != nil || ok {
		t.Errorf("another node's session template = %v, %v", ok, err)
	}
	if _, err := pool.Exec(ctx, `update sandbox_sessions set state = 'closed', closed_at = now() where id = $1`, session); err != nil {
		t.Fatal(err)
	}
	if ok, err := store.NodeRunsSandboxOf(ctx, account.ID, node.ID); err != nil || ok {
		t.Errorf("closed session = %v, %v", ok, err)
	}
	if ok, err := store.NodeRunsJob(ctx, uuid.NewString(), node.ID); err != nil || ok {
		t.Errorf("no job = %v, %v", ok, err)
	}

	// The audit appends; refuses a bad subject class (platform and
	// build_output are valid since 20261124135843100).
	if err := store.RecordStorageGrant(ctx, secretd.StorageGrantRecord{NodeID: node.ID, Class: "deployment", SubjectID: dep.ID,
		AppID: app.ID, Access: "read", Objects: 3}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pool.QueryRow(ctx, `select objects from storage_grant_audit where node_id = $1 and subject_id = $2`, node.ID, dep.ID).Scan(&n); err != nil || n != 3 {
		t.Fatalf("audit row objects = %d, %v", n, err)
	}
	if err := store.RecordStorageGrant(ctx, secretd.StorageGrantRecord{NodeID: node.ID, Class: "unknown_class", SubjectID: dep.ID,
		Access: "read", Objects: 1}); err == nil {
		t.Fatal("the audit accepted an unknown subject class")
	}
}
