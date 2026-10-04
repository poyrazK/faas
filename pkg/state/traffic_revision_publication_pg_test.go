//go:build !no_pg

// adr: 570
package state

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/hostidentity"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func TestPgTrafficRevisionCreationPublication(t *testing.T) {
	for _, activity := range []bool{false, true} {
		t.Run(fmt.Sprintf("activity=%v", activity), func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficRevisionCreation(t, store, account, app, activity, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficAppBindingIntent(t, pool) })
		})
	}
}

func pgTrafficRevisionIntent(t *testing.T, pool *pgxpool.Pool) string {
	t.Helper()
	var supplemental string
	err := pool.QueryRow(t.Context(), `SELECT md5(jsonb_build_object(
 'aliases',(SELECT jsonb_agg(to_jsonb(t) ORDER BY app_id,name) FROM deployment_aliases t),
 'openapi',(SELECT jsonb_agg(to_jsonb(t) ORDER BY deployment_id) FROM deployment_openapi_snapshots t),
 'deliveries',(SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM app_webhook_deliveries t))::text)`).Scan(&supplemental)
	if err != nil {
		t.Fatal(err)
	}
	return pgTrafficAppBindingIntent(t, pool) + supplemental
}

func TestPgTrafficRevisionRevivalPublication(t *testing.T) {
	registerTrafficRevivalCapture(t)
	for _, mode := range trafficRevisionRevivalModes {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficRevisionRevival(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficRevisionIntent(t, pool) })
		})
	}
}

func TestPgTrafficRevisionAppPublication(t *testing.T) {
	for _, mode := range []string{"restore", "visibility", "rename"} {
		t.Run(mode, func(t *testing.T) {
			store, pool, account, app := trafficHostPGFixture(t)
			testTrafficRevisionAppPublication(t, store, account, app, mode, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficRevisionIntent(t, pool) })
		})
	}
}

func TestPgTrafficAliasRevivalHiddenPrimary(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	store := NewPgStore(pool, WithTrafficAppsDomain("apps.example.test"))
	testTrafficAliasRevivalHiddenPrimary(t, store, store, account, app, func(rule EdgeRule) { seedPgDomainRemovalLegacy(t, pool, rule) }, func() string { return pgTrafficRevisionIntent(t, pool) })
}

func TestPgTrafficRevisionMetadataEligibilityAndScalarBounds(t *testing.T) {
	_, pool, account, app := trafficHostPGFixture(t)
	store := NewPgStore(pool, WithTrafficAppsDomain(""))
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	read := func(t *testing.T) trafficHostAnalysis {
		t.Helper()
		tx, err := pool.Begin(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
		view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), "")
		if err != nil {
			t.Fatal(err)
		}
		return view
	}
	host := hostidentity.BuildDeploymentHost(hostidentity.DeployWildcardSuffix, deployment.Revision, app.Slug)
	for _, status := range []DeploymentStatus{DeployPending, DeployBuilding, DeployImaging, DeploySnapshotting, DeployLive, DeploySuperseded, DeployFailed, DeployCancelled} {
		t.Run(string(status), func(t *testing.T) {
			if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status=$2 WHERE id=$1`, deployment.ID, string(status)); err != nil {
				t.Fatal(err)
			}
			view := read(t)
			if slices.Contains(view.RevisionHosts, host) != (Deployment{Status: status}).DeploymentPreviewActive() || len(view.PrimaryHosts) != 0 {
				t.Fatalf("revision metadata/runtime disagreement: %+v", view.RevisionHosts)
			}
		})
	}
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET status='pending' WHERE id=$1`, deployment.ID); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct{ name, query string }{
		{"zero-revision", `UPDATE deployments SET revision=0 WHERE id=$1`},
		{"deleted-target", `UPDATE deployments SET deleted_at=now() WHERE id=$1`},
		{"deleted-owner", `UPDATE apps SET deleted_at=now() WHERE id=$2`},
		{"internal-owner", `UPDATE apps SET visibility='internal' WHERE id=$2`},
		{"invalid-slug", `UPDATE apps SET slug='legacy.invalid' WHERE id=$2`},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			tx, err := pool.Begin(t.Context())
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = tx.Rollback(context.WithoutCancel(t.Context())) }()
			// Use both UUID parameters so fixtures remain typed even when a
			// particular mutation only refers to one row.
			if _, err := tx.Exec(t.Context(), fixture.query+` AND $1::uuid IS NOT NULL AND $2::uuid IS NOT NULL`, deployment.ID, app.ID); err != nil {
				t.Fatal(err)
			}
			view, err := readTrafficHostAnalysis(t.Context(), tx, uuidToPgtype(account.ID), "")
			if err != nil || len(view.RevisionHosts) != 0 {
				t.Fatalf("unservable revision retained allowance: %+v %v", view.RevisionHosts, err)
			}
		})
	}
	second, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage, CanaryTotalSteps: 2})
	if err != nil {
		t.Fatal(err)
	}
	if second.Revision <= deployment.Revision {
		t.Fatal("fixture revision did not advance")
	}
	for _, fixture := range []struct {
		name   string
		inputs int32
		bytes  int64
	}{
		{"inputs", 1, api.TrafficPolicyMaxAnalysisMetadataBytes},
		{"bytes", api.TrafficPolicyMaxAnalysisInputs, 1},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			row, err := sqlc.New().ReadTrafficHostAnalysis(t.Context(), pool, sqlc.ReadTrafficHostAnalysisParams{AccountID: uuidToPgtype(account.ID), DeploySuffix: hostidentity.DeployWildcardSuffix, MaxInputs: fixture.inputs, MaxBytes: fixture.bytes})
			if err != nil || len(row.Data) != 0 || fixture.name == "inputs" && row.Inputs <= int64(fixture.inputs) || fixture.name == "bytes" && row.Bytes <= fixture.bytes {
				t.Fatalf("revision metadata escaped scalar transfer bound: %+v %v", row, err)
			}
		})
	}
}

func TestPgTrafficRevisionMutationLocksCapturedMembership(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peer, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "revision-membership-peer"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	guard, err := store.beginDeploymentTrafficMutation(t.Context(), deployment.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = guard.Rollback(context.WithoutCancel(t.Context())) }()
	writer, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = writer.Rollback(context.WithoutCancel(t.Context())) }()
	if _, err := writer.Exec(t.Context(), `SET LOCAL lock_timeout='100ms'`); err != nil {
		t.Fatal(err)
	}
	_, err = writer.Exec(t.Context(), `UPDATE deployments SET app_id=$2 WHERE id=$1`, deployment.ID, peer.ID)
	var refusal *pgconn.PgError
	if !errors.As(err, &refusal) || refusal.Code != "55P03" {
		t.Fatalf("membership changed while guarded writer owned row: %v", err)
	}
}

func TestPgTrafficRevisionMutationRejectsStaleCapturedMembership(t *testing.T) {
	store, pool, account, app := trafficHostPGFixture(t)
	peer, err := store.CreateApp(t.Context(), App{AccountID: account.ID, Slug: "revision-stale-peer"})
	if err != nil {
		t.Fatal(err)
	}
	deployment, err := store.CreateDeployment(t.Context(), Deployment{AppID: app.ID, Kind: DeploymentKindImage})
	if err != nil {
		t.Fatal(err)
	}
	_, release, busy, err := store.tryAcquireTrafficPolicySession(t.Context(), uuidToPgtype(account.ID), false)
	if err != nil || busy {
		t.Fatalf("serialization holder: busy=%v err=%v", busy, err)
	}
	defer release(t.Context())
	waiting, application := domainRemovalWaitingStore(t, pool)
	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
		defer cancel()
		result <- waiting.UpdateDeploymentStatus(ctx, deployment.ID, DeployImaging, "stale caller")
	}()
	awaitDomainRemovalSessionWait(t, pool, application)
	// Inject an uncoordinated legacy ownership move during the verified wait.
	// There is no supported deployment-transfer API.
	if _, err := pool.Exec(t.Context(), `UPDATE deployments SET app_id=$2 WHERE id=$1`, deployment.ID, peer.ID); err != nil {
		t.Fatal(err)
	}
	before := pgTrafficRevisionIntent(t, pool)
	release(t.Context())
	if err := <-result; !errors.Is(err, ErrConflict) {
		t.Fatalf("stale captured membership accepted: %v", err)
	}
	if before != pgTrafficRevisionIntent(t, pool) {
		t.Fatal("stale deployment caller changed the new owner's intent")
	}
	if err := store.UpdateDeploymentStatus(t.Context(), deployment.ID, DeployImaging, "fresh caller"); err != nil {
		t.Fatalf("fresh owner retry or lock release failed: %v", err)
	}
}
