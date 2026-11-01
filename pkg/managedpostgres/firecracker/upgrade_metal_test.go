//go:build metal && linux

// adr: 1027 — a customer's major upgrade end to end: the managed
// PostgreSQL service journals it, its reconciler drives the driver, schedd
// runs pg_upgrade in the database's VM, and the database serves its data
// and logins on the new major; an upgrade PostgreSQL refuses ends failed
// for good, the database still serving on its major.
package firecracker

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/onebox-faas/faas/pkg/dbvm"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestMetalMajorUpgradeThroughService(t *testing.T) {
	workdir, guestInit := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_GUEST_INIT")
	if workdir == "" || guestInit == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_GUEST_INIT to run the driver upgrade metal test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	digest, err := dbvm.FileDigest(guestInit)
	if err != nil {
		t.Fatal(err)
	}
	bases := map[int]string{}
	for _, major := range []int{14, 17} {
		path := dbvm.BasePath(filepath.Join(workdir, "major-bases"), major, dbvm.PostgresImages[major], digest)
		if _, err := os.Stat(path); err != nil {
			if err := dbvm.BuildBase(ctx, dbvm.PostgresImages[major], guestInit, path); err != nil {
				t.Fatalf("build the PostgreSQL %d base: %v", major, err)
			}
		}
		bases[major] = path
	}
	st := newMetalStackWith(t, workdir, bases)
	connect := connectThroughProxy(st.proxyAddr)
	registry, err := managedpostgres.NewRegistry(managedpostgres.Config{DefaultRegion: "eu-central", MaxDatabasesPerAccount: 3,
		Defaults: map[string]string{"eu-central": "native"},
		Backends: []managedpostgres.BackendConfig{{ID: "native", Driver: Driver, Region: "eu-central", Namespace: "native"}}},
		func(string) string { return "" }, map[string]managedpostgres.Factory{Driver: func(managedpostgres.BackendConfig, func(string) string) (managedpostgres.Provider, error) {
			return st.p, nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	svc, err := managedpostgres.NewService(registry, managedpostgres.NewMemoryStore(), managedpostgres.ServiceOptions{ProvisioningEnabled: func() bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	account := uuid.NewString()
	spec14 := devSpec()
	spec14.PostgresMajor = 14
	// settle reconciles until the database is ready and done reports true.
	settle := func(id string, done func(managedpostgres.Database) bool) managedpostgres.Database {
		t.Helper()
		for {
			d, err := svc.Reconcile(ctx, account, id)
			if err == nil && d.State == managedpostgres.StateReady && done(d) {
				return d
			}
			if ctx.Err() != nil {
				t.Fatalf("database %s never settled: %+v %v", id, d, err)
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	// create makes a PostgreSQL 14 database with a credential and runs sql
	// through pgproxy.
	create := func(name, sql string) (managedpostgres.Database, managedpostgres.CredentialMaterial) {
		t.Helper()
		created, err := svc.Create(ctx, managedpostgres.CreateRequest{AccountID: account, Name: name, Spec: spec14})
		if err != nil {
			t.Fatal(err)
		}
		database := settle(created.ID, func(d managedpostgres.Database) bool { return d.Spec == spec14 })
		t.Cleanup(func() { _ = st.sched.Delete(context.WithoutCancel(ctx), database.ProviderResourceID) })
		writer, err := st.p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: database.ProviderResourceID,
			IdentityKey: name + "-writer", IdempotencyKey: name + "-writer", Access: managedpostgres.CredentialMigration})
		if err != nil {
			t.Fatal(err)
		}
		metalQuery(ctx, t, connect, writer, sql)
		return database, writer
	}
	serverMajor := func(writer managedpostgres.CredentialMaterial) string {
		t.Helper()
		return metalQuery(ctx, t, connect, writer, "SELECT current_setting('server_version_num')")[:2]
	}

	database, writer := create("orders", "CREATE TABLE notes AS SELECT g AS id, repeat('x', 100) AS body FROM generate_series(1, 5000) g")
	if v := serverMajor(writer); v != "14" {
		t.Fatalf("created on %s", v)
	}
	request := managedpostgres.UpgradeMajorRequest{AccountID: account, DatabaseID: database.ID, RequestID: uuid.NewString(), PostgresMajor: 17}
	started := time.Now()
	if _, err := svc.UpgradeMajor(ctx, request); err != nil {
		t.Fatal(err)
	}
	upgraded := settle(database.ID, func(d managedpostgres.Database) bool { return d.Spec.PostgresMajor == 17 })
	took := time.Since(started)
	if op, err := svc.GetMajorUpgrade(ctx, account, database.ID, request.RequestID); err != nil || op.State != managedpostgres.ResizeSucceeded {
		t.Fatalf("upgrade receipt = %+v, %v", op, err)
	}
	if upgraded.ProviderResourceID != database.ProviderResourceID || upgraded.DataResourceID != database.DataResourceID {
		t.Fatalf("the upgrade changed the database's identity: %+v", upgraded)
	}
	if v := serverMajor(writer); v != "17" {
		t.Fatalf("server after the upgrade = %s", v)
	}
	if n := metalQuery(ctx, t, connect, writer, "SELECT count(*)::text FROM notes"); n != "5000" {
		t.Fatalf("rows after the upgrade = %s", n)
	}

	// PostgreSQL refuses a cluster with a reg* column: the upgrade ends
	// failed for good and the database serves on 14 as before.
	blocked, blockedWriter := create("blocked", "CREATE TABLE blocker (r regprocedure); INSERT INTO blocker VALUES ('now()')")
	refusedRequest := managedpostgres.UpgradeMajorRequest{AccountID: account, DatabaseID: blocked.ID, RequestID: uuid.NewString(), PostgresMajor: 17}
	if _, err := svc.UpgradeMajor(ctx, refusedRequest); err != nil {
		t.Fatal(err)
	}
	var refused managedpostgres.ResizeOperation
	settle(blocked.ID, func(managedpostgres.Database) bool {
		refused, err = svc.GetMajorUpgrade(ctx, account, blocked.ID, refusedRequest.RequestID)
		return err == nil && refused.State != managedpostgres.ResizePending
	})
	if refused.State != managedpostgres.ResizeFailed || refused.ErrorCode != "upgrade_refused" {
		t.Fatalf("refused upgrade = %+v", refused)
	}
	if d, err := svc.Get(ctx, account, blocked.ID); err != nil || d.Spec != spec14 || d.State != managedpostgres.StateReady {
		t.Fatalf("after the refusal the database is %+v, %v", d, err)
	}
	if v := serverMajor(blockedWriter); v != "14" {
		t.Fatalf("server after the refusal = %s", v)
	}
	t.Logf("upgraded a PostgreSQL 14 database to 17 through the managed PostgreSQL service in %v; a refused upgrade ended failed (%s)",
		took.Round(time.Millisecond), refused.ErrorCode)
}

// metalQuery runs sql through pgproxy as m and returns a SELECT's one text
// value.
func metalQuery(ctx context.Context, t *testing.T, connect Connector, m managedpostgres.CredentialMaterial, sql string) string {
	t.Helper()
	conn, err := connect(ctx, m)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = conn.Close(ctx) }()
	if strings.HasPrefix(sql, "SELECT") {
		var out string
		if err := conn.QueryRow(ctx, sql).Scan(&out); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
		return out
	}
	if _, err := conn.Exec(ctx, sql); err != nil {
		t.Fatalf("%s: %v", sql, err)
	}
	return ""
}
