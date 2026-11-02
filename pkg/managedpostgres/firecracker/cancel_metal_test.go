//go:build metal && linux

// adr: 1027 — query cancellation reaches a database VM: through pgproxyd,
// vmmd's session relay and the guest, a client's cancel stops its query on
// the direct and the pooled endpoint, and the session goes on.
package firecracker

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestMetalCancelThroughTheProxy(t *testing.T) {
	workdir, base := os.Getenv("FAAS_DBVM_WORKDIR"), os.Getenv("FAAS_DBVM_BASE")
	if workdir == "" || base == "" {
		t.Skip("set FAAS_DBVM_WORKDIR and FAAS_DBVM_BASE to run the driver metal test")
	}
	st := newMetalStack(t, workdir, base)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	id := uuid.NewString()
	t.Cleanup(func() { _ = st.sched.Delete(context.Background(), id) })
	if _, err := st.p.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: id, Spec: devSpec()}); err != nil {
		t.Fatal(err)
	}
	m, err := st.p.IssueCredentials(ctx, managedpostgres.CredentialRequest{ProviderResourceID: id, IdentityKey: "rw", Access: managedpostgres.CredentialReadWrite})
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(st.proxyAddr)
	for _, ep := range m.Endpoints {
		pc, err := pgx.ParseConfig("host=" + ep.Host + " port=" + port + " user=" + m.Username + " password=" + m.Password +
			" dbname=" + m.Database + " sslmode=" + m.TLSMode + " connect_timeout=30")
		if err != nil {
			t.Fatal(err)
		}
		pc.LookupFunc = func(context.Context, string) ([]string, error) { return []string{host}, nil }
		pc.TLSConfig.ServerName = ep.Host
		conn, err := pgx.ConnectConfig(ctx, pc)
		if err != nil {
			t.Fatalf("%s: %v", ep.Role, err)
		}
		done := make(chan error, 1)
		go func() {
			_, err := conn.Exec(ctx, "SELECT pg_sleep(60)")
			done <- err
		}()
		time.Sleep(time.Second)
		started := time.Now()
		if err := conn.PgConn().CancelRequest(ctx); err != nil {
			t.Fatalf("%s: cancel: %v", ep.Role, err)
		}
		select {
		case err := <-done:
			if pgState(err) != "57014" {
				t.Fatalf("%s: query ended with %v, want 57014", ep.Role, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("%s: the query was not canceled", ep.Role)
		}
		canceled := time.Since(started)
		var one int
		if err := conn.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
			t.Fatalf("%s: the session after the cancel: %v", ep.Role, err)
		}
		_ = conn.Close(ctx)
		t.Logf("%s endpoint: cancel to query end %v", ep.Role, canceled.Round(time.Millisecond))
	}
}
