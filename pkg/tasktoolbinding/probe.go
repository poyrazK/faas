package tasktoolbinding

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"strconv"

	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/credentialprobe"
	"github.com/onebox-faas/faas/pkg/publicdest"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding/probedb"
)

// Driver errors and credentials must never enter a public receipt or log.
var ErrProbe = errors.New("hosted Task binding probe failed")

type Connect func(context.Context, *pgx.ConnConfig) (*pgx.Conn, error)

// SQLConfig pins public destinations at dial time, verifies TLS server identity,
// excludes host PGOPTIONS and does not offer host client certificates.
func SQLConfig(dsn string) (*pgx.ConnConfig, error) {
	c, err := credentialprobe.CredentialSQLConfig(dsn)
	if err != nil {
		return nil, ErrProbe
	}
	if _, forbidden := api.TenantEgressForbiddenPort(int(c.Port)); forbidden {
		return nil, ErrProbe
	}
	secure := func(t *tls.Config) *tls.Config {
		t = t.Clone()
		t.InsecureSkipVerify = false
		t.ServerName = c.Host
		t.MinVersion = tls.VersionTLS12
		t.Certificates = nil
		t.GetClientCertificate = nil
		t.VerifyConnection = nil
		t.VerifyPeerCertificate = nil
		return t
	}
	c.TLSConfig = secure(c.TLSConfig)
	for _, f := range c.Fallbacks {
		if f.Host != c.Host || f.Port != c.Port || f.TLSConfig == nil {
			return nil, ErrProbe
		}
		f.TLSConfig = secure(f.TLSConfig)
	}
	c.ConnectTimeout = api.HostedTaskBindingSQLTimeout
	c.RuntimeParams["statement_timeout"] = strconv.FormatInt(api.HostedTaskBindingSQLTimeout.Milliseconds(), 10)
	c.RuntimeParams["lock_timeout"] = strconv.FormatInt(api.HostedTaskBindingSQLTimeout.Milliseconds(), 10)
	c.RuntimeParams["row_security"] = "on"
	c.LookupFunc = func(_ context.Context, host string) ([]string, error) { return []string{host}, nil }
	dialer := publicdest.NewDialer()
	c.DialFunc = func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}
	return c, nil
}

type databaseFacts struct {
	identity       probedb.TaskBindingIdentityRow
	tables         []probedb.TaskBindingTablePrivilegesRow
	observerUnsafe bool
}

// Probe opens at most one private connection at a time. Every query runs in a
// read-only transaction and reads fixed catalog SQL; customer code is not run.
func Probe(ctx context.Context, dsns [4]string, connect Connect) ([]api.HostedTaskBindingResult, error) {
	ctx, cancel := context.WithTimeout(ctx, api.HostedTaskBindingProbeTimeout)
	defer cancel()
	results := Results("not_checked")[3:]
	if connect == nil {
		connect = pgx.ConnectConfig
	}
	var facts [4]databaseFacts
	for i, dsn := range dsns {
		var err error
		facts[i], err = probeDatabase(ctx, dsn, connect, i == 3)
		if err != nil {
			results[0].Status = "failed"
			return results, ErrProbe
		}
	}
	checks := []bool{sameDatabase(facts), runtimeRoles(facts), migrationOwner(facts[2].identity), observerReadOnly(facts)}
	for i, ok := range checks {
		results[i].Status = "failed"
		if ok {
			results[i].Status = "passed"
		}
	}
	return results, nil
}

func probeDatabase(ctx context.Context, dsn string, connect Connect, observer bool) (databaseFacts, error) {
	var out databaseFacts
	cfg, err := SQLConfig(dsn)
	if err != nil {
		return out, ErrProbe
	}
	conn, err := connect(ctx, cfg)
	if err != nil {
		return out, ErrProbe
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostedTaskBindingCleanupTimeout)
		defer cancel()
		_ = conn.Close(cleanup)
	}()
	tx, err := conn.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, ErrProbe
	}
	q := probedb.New()
	out.identity, err = q.TaskBindingIdentity(ctx, tx)
	if err != nil {
		return out, ErrProbe
	}
	out.tables, err = q.TaskBindingTablePrivileges(ctx, tx, int32(api.HostedTaskBindingCatalogMaxTables+1))
	if err != nil || len(out.tables) > api.HostedTaskBindingCatalogMaxTables {
		return out, ErrProbe
	}
	if observer {
		out.observerUnsafe, err = q.TaskBindingObserverUnsafe(ctx, tx)
		if err != nil {
			return out, ErrProbe
		}
	}
	return out, nil
}

func sameDatabase(f [4]databaseFacts) bool {
	a := f[0].identity
	if a.ServerAddress == "" || a.ServerPort == 0 || a.DatabaseName == "" || a.SchemaName == "" {
		return false
	}
	for _, v := range f {
		b := v.identity
		if b.DatabaseName != a.DatabaseName || b.SchemaName != a.SchemaName || b.ServerAddress != a.ServerAddress || b.ServerPort != a.ServerPort || b.SchemaOwner != a.SchemaOwner || !b.ReadOnly || !b.RowSecurity {
			return false
		}
	}
	return true
}
func migrationOwner(v probedb.TaskBindingIdentityRow) bool {
	return !v.Privileged && !v.UnsafeMembership && !v.Temporary && v.RoleName == v.SchemaOwner && v.CanCreate
}
func runtimeRoles(f [4]databaseFacts) bool {
	for _, i := range []int{0, 1} {
		v := f[i].identity
		if v.Privileged || v.UnsafeMembership || v.CanCreate || v.DatabaseCreate || v.Temporary || v.OwnerMember || v.Login != v.RoleName || v.RoleName == f[2].identity.RoleName || v.RoleName == f[3].identity.RoleName || !taskPrivileges(f[i].tables, false) {
			return false
		}
	}
	return true
}
func observerReadOnly(f [4]databaseFacts) bool {
	v := f[3].identity
	return !v.Privileged && !v.UnsafeMembership && !v.CanCreate && !v.DatabaseCreate && !v.Temporary && !v.OwnerMember && v.Login == v.RoleName && v.RoleName != f[2].identity.RoleName && !f[3].observerUnsafe && taskPrivileges(f[3].tables, true)
}
func taskPrivileges(rows []probedb.TaskBindingTablePrivilegesRow, observer bool) bool {
	reads := []string{"gregale_mcp_tasks", "gregale_mcp_task_owners", "gregale_mcp_task_workers", "gregale_mcp_task_worker_handlers", "gregale_mcp_task_schema", "gregale_mcp_task_admission_namespaces"}
	writes := map[string]bool{"gregale_mcp_tasks": true, "gregale_mcp_task_owners": true, "gregale_mcp_task_fairness": true, "gregale_mcp_task_workers": true, "gregale_mcp_task_worker_handlers": true}
	if !observer {
		reads = append(reads, "gregale_mcp_task_fairness", "gregale_mcp_task_crypto_keys", "gregale_mcp_task_admission")
	}
	seen := map[string]bool{}
	for _, r := range rows {
		if !r.OwnedBySchemaOwner || r.CanAdminister || observer && r.CanWrite || !observer && r.CanWrite && !writes[r.Name] {
			return false
		}
		if r.CanRead {
			seen[r.Name] = true
		}
		if !observer && writes[r.Name] && !r.CanDml {
			return false
		}
	}
	for _, n := range reads {
		if !seen[n] {
			return false
		}
	}
	return true
}
