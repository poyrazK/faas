//go:build !no_pg

// adr: 20261110210000
package state_test

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

// Slice 3j-3: the claim that moves a build from queued to running records
// the claiming node, so secretd admits only that node to pull the app's
// private base images. A per-node login records its own node and cannot
// point a build at another; another login records the faas.builder_node
// session setting; a build that leaves running carries no node.
func TestBuildsBuilderNodeStampedAtClaim(t *testing.T) {
	store, owner := lifecycleCompletionPGStore(t)
	ctx := t.Context()
	nodeLogin, login := ownerAuthorityLogin(t, owner, false)
	shared, _ := ownerAuthorityLogin(t, owner, false)
	a := completionNode(t, store, state.NodeLifecycleActive)
	if _, err := owner.Exec(ctx, `insert into compute_node_db_logins (login, node_name) values ($1, $2)`, login, a.Name); err != nil {
		t.Fatal(err)
	}
	acct, err := store.CreateAccount(ctx, "builder-node@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, state.App{AccountID: acct.ID, Slug: "builder-node", Type: state.AppTypeApp, RAMMB: 512, MaxConcurrency: 5, IdleTimeoutS: 60})
	if err != nil {
		t.Fatal(err)
	}
	queue := func() state.Build {
		t.Helper()
		d, err := store.CreateDeployment(ctx, state.Deployment{AppID: app.ID, Kind: state.DeploymentKindTarball, Status: state.DeployPending, SourcePath: "/spool/x.tar.gz"})
		if err != nil {
			t.Fatal(err)
		}
		b, err := store.CreateBuild(ctx, d.ID, state.DeploymentKindTarball, 100, "")
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	builderNode := func(id string) string {
		t.Helper()
		var node *string
		if err := owner.QueryRow(ctx, `select builder_node_name from builds where id = $1`, id).Scan(&node); err != nil {
			t.Fatal(err)
		}
		if node == nil {
			return ""
		}
		return *node
	}

	// A per-node login's claim records its own node.
	mine := queue()
	if _, err := state.NewPgStore(nodeLogin).ClaimQueuedBuild(ctx, mine.ID); err != nil {
		t.Fatalf("per-node claim: %v", err)
	}
	if got := builderNode(mine.ID); got != a.Name {
		t.Fatalf("per-node claim recorded %q, want %q", got, a.Name)
	}
	// It cannot point the running build at another node, nor clear it.
	for name, sql := range map[string]string{
		"point at another node": `update builds set builder_node_name = 'elsewhere' where id = $1`,
		"clear its claim":       `update builds set builder_node_name = null where id = $1`,
	} {
		if _, err := nodeLogin.Exec(ctx, sql, mine.ID); sqlState(err) != "42501" {
			t.Errorf("%s = %v, want SQLSTATE 42501", name, err)
		}
	}
	// A per-node login's RecordBuildNode after its own claim changes nothing.
	if err := state.NewPgStore(nodeLogin).RecordBuildNode(ctx, mine.ID, a.Name); err != nil {
		t.Fatalf("per-node RecordBuildNode after the claim: %v", err)
	}

	// A shared login's claim records no node; builderd then records its own,
	// once: a second call does not overwrite it.
	theirs := queue()
	sharedStore := state.NewPgStore(shared)
	if _, err := sharedStore.ClaimQueuedBuild(ctx, theirs.ID); err != nil {
		t.Fatalf("shared-login claim: %v", err)
	}
	if got := builderNode(theirs.ID); got != "" {
		t.Fatalf("shared-login claim recorded %q, want none", got)
	}
	if err := sharedStore.RecordBuildNode(ctx, theirs.ID, "node-shared"); err != nil {
		t.Fatal(err)
	}
	if err := sharedStore.RecordBuildNode(ctx, theirs.ID, "node-late"); err != nil {
		t.Fatal(err)
	}
	if got := builderNode(theirs.ID); got != "node-shared" {
		t.Fatalf("shared-login build names %q, want node-shared", got)
	}
	// A per-node login cannot take over a build another node is running.
	if _, err := nodeLogin.Exec(ctx, `update builds set builder_node_name = $2 where id = $1`, theirs.ID, a.Name); sqlState(err) != "42501" {
		t.Fatalf("taking over another node's build = %v, want SQLSTATE 42501", err)
	}
	// RecordBuildNode never touches a queued build.
	waiting := queue()
	if err := sharedStore.RecordBuildNode(ctx, waiting.ID, "node-shared"); err != nil {
		t.Fatal(err)
	}
	if got := builderNode(waiting.ID); got != "" {
		t.Fatalf("queued build names %q", got)
	}

	// Leaving running clears the node; an insert never carries one.
	if _, err := owner.Exec(ctx, `update builds set status = 'succeeded', finished_at = now() where id = $1`, mine.ID); err != nil {
		t.Fatal(err)
	}
	if got := builderNode(mine.ID); got != "" {
		t.Fatalf("finished build still names %q", got)
	}
	fresh := queue()
	if got := builderNode(fresh.ID); got != "" {
		t.Fatalf("new build names %q", got)
	}
}
