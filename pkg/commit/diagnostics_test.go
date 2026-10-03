package commit

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestPostgresCommitDoctorReadOnly(t *testing.T) {
	cluster := pgtest.OpenTLSCluster(t)
	t.Setenv("PGSSLROOTCERT", cluster.CAPath)
	ctx := t.Context()
	source, event := uuid.NewString(), uuid.NewString()
	if _, err := cluster.Admin.Exec(ctx, Schema); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `CREATE ROLE doctor LOGIN PASSWORD 'private-doctor-fixture'; GRANT SELECT ON public.gregale_outbox,public.gregale_commit_binding TO doctor`); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `INSERT INTO public.gregale_commit_binding(source_id) VALUES($1::uuid)`, source); err != nil {
		t.Fatal(err)
	}
	if _, err := cluster.Admin.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES($1::uuid,'order.created','{}')`, event); err != nil {
		t.Fatal(err)
	}
	checks := CheckDatabase(ctx, cluster.URL("doctor", "private-doctor-fixture"), source)
	if len(checks) != 4 || checks[3].Code != "permissions_unqualified" {
		t.Fatalf("missing grant qualified: %+v", checks)
	}
	if _, err := cluster.Admin.Exec(ctx, `GRANT INSERT,UPDATE,DELETE ON public.gregale_outbox TO doctor`); err != nil {
		t.Fatal(err)
	}
	for _, check := range CheckDatabase(ctx, cluster.URL("doctor", "private-doctor-fixture"), source) {
		if check.State != "pass" {
			t.Fatalf("qualified check=%+v", check)
		}
	}
	checks = CheckDatabase(ctx, cluster.URL("doctor", "private-doctor-fixture"), uuid.NewString())
	if len(checks) != 4 || checks[2].Code != "source_binding_unqualified" {
		t.Fatalf("wrong binding passed: %+v", checks)
	}
	var untouched bool
	if err := cluster.Admin.QueryRow(ctx, `SELECT attempts=0 AND lease_token IS NULL AND accepted_at IS NULL FROM public.gregale_outbox WHERE event_id=$1::uuid`, event).Scan(&untouched); err != nil || !untouched {
		t.Fatalf("doctor mutated outbox: %v %v", untouched, err)
	}
	checks = CheckDatabase(ctx, cluster.URL("doctor", "wrong-secret-password"), source)
	body, err := json.Marshal(checks)
	if err != nil {
		t.Fatal(err)
	}
	if len(checks) != 1 || checks[0].Code != "database_unavailable" || strings.Contains(string(body), "wrong-secret-password") || strings.Contains(string(body), "postgres://") {
		t.Fatalf("unsafe failed diagnostic: %s", body)
	}
}
