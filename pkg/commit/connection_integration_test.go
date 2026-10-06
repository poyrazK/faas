package commit

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPostgresTLSConnectionRecovery(t *testing.T) {
	cluster := pgtest.OpenTLSCluster(t)
	admin := cluster.Admin
	cert := cluster.CAPath
	t.Setenv("PGSSLROOTCERT", cert)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := admin.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `CREATE ROLE relay LOGIN PASSWORD 'initial-fixture-password'; CREATE SCHEMA shadow; ALTER ROLE relay SET search_path=shadow; GRANT SELECT,INSERT,UPDATE,DELETE ON public.gregale_outbox TO relay; GRANT SELECT ON public.gregale_commit_binding TO relay`); err != nil {
		t.Fatal(err)
	}
	connection := func(host, password string) string {
		u, err := url.Parse(cluster.URL("relay", password))
		if err != nil {
			t.Fatal(err)
		}
		u.Host = net.JoinHostPort(host, u.Port())
		return u.String()
	}
	policy := NetworkPolicy{Hosts: map[string]bool{"localhost": true}, Prefixes: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/32")}}
	open := func(password string) *pgxpool.Pool {
		t.Helper()
		pool, err := OpenPool(ctx, connection("localhost", password), policy)
		if err != nil {
			t.Fatalf("qualified TLS connection: %v\n%s", err, cluster.Log())
		}
		return pool
	}
	pool := open("initial-fixture-password")
	defer func() { pool.Close() }()
	if err := QualifySchema(ctx, pool); err != nil {
		t.Fatal(err)
	}
	var searchPath string
	if err := pool.QueryRow(ctx, "SHOW search_path").Scan(&searchPath); err != nil || searchPath != "public" {
		t.Fatalf("role search path escaped qualification: %q %v", searchPath, err)
	}
	deny := NetworkPolicy{Hosts: policy.Hosts, Prefixes: []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}}
	if denied, err := OpenPool(ctx, connection("localhost", "initial-fixture-password"), deny); err == nil {
		denied.Close()
		t.Fatal("unqualified address accepted")
	}
	mismatch := NetworkPolicy{Hosts: map[string]bool{"127.0.0.1": true}, Prefixes: policy.Prefixes}
	if wrong, err := OpenPool(ctx, connection("127.0.0.1", "initial-fixture-password"), mismatch); err == nil {
		wrong.Close()
		t.Fatal("certificate hostname mismatch accepted")
	}
	otherCluster := pgtest.OpenTLSCluster(t)
	otherCA := otherCluster.CAPath
	t.Setenv("PGSSLROOTCERT", otherCA)
	if untrusted, err := OpenPool(ctx, connection("localhost", "initial-fixture-password"), policy); err == nil {
		untrusted.Close()
		t.Fatal("untrusted server certificate accepted")
	}
	t.Setenv("PGSSLROOTCERT", cert)
	receipt := Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}
	event := Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{}`)}
	pending := Event{ID: uuid.NewString(), Type: "order.created", Data: []byte(`{}`)}
	pendingReceipt := Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}
	relay := Relay{Pool: pool, Acceptor: acceptFunc(func(_ context.Context, delivered Event) (Receipt, error) {
		if delivered.ID == event.ID {
			return receipt, nil
		}
		if delivered.ID == pending.ID {
			return pendingReceipt, nil
		}
		return Receipt{}, fmt.Errorf("unexpected fixture event")
	})}
	tx, err := admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, event); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("TLS relay handoff: %d %v", n, err)
	}
	if _, err := admin.Exec(ctx, `ALTER ROLE relay PASSWORD 'rotated-fixture-password'`); err != nil {
		t.Fatal(err)
	}
	pool.Close()
	if stale, err := OpenPool(ctx, connection("localhost", "initial-fixture-password"), policy); err == nil {
		stale.Close()
		t.Fatal("retired database credential accepted")
	}
	pool = open("rotated-fixture-password")
	pool.Close()
	tx, err = admin.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Insert(ctx, tx, pending); err != nil {
		tx.Rollback(ctx)
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	cluster.Stop()
	if unavailable, err := OpenPool(ctx, connection("localhost", "rotated-fixture-password"), policy); err == nil {
		unavailable.Close()
		t.Fatal("stopped source accepted a connection")
	}
	cluster.Start()
	pool = open("rotated-fixture-password")
	relay.Pool = pool
	if n, err := relay.Tick(ctx); err != nil || n != 1 {
		t.Fatalf("pending event recovery after source restart: %d %v", n, err)
	}
	if n, err := relay.Tick(ctx); err != nil || n != 0 {
		t.Fatalf("source restart redelivered accepted event: %d %v", n, err)
	}
	var recovered string
	if err := pool.QueryRow(ctx, `SELECT receipt_id::text FROM gregale_outbox WHERE event_id=$1`, pending.ID).Scan(&recovered); err != nil || recovered != pendingReceipt.ID {
		t.Fatalf("recovered pending checkpoint: %s %v", recovered, err)
	}
	var accepted string
	if err := pool.QueryRow(ctx, `SELECT receipt_id::text FROM gregale_outbox WHERE event_id=$1`, event.ID).Scan(&accepted); err != nil || accepted != receipt.ID {
		t.Fatalf("restart checkpoint: %s %v", accepted, err)
	}
}
