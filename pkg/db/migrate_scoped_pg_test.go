package db_test

// adr: 592

import (
	"testing"

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
)

func TestMigrateUpScopesIssuedStandardsFunctions(t *testing.T) {
	pool := pgtest.Open(t)
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		"application_standard_exception_deadline", "application_standard_enroll_app",
		"application_standard_lock_native_boot", "application_standard_runtime_snapshot",
		"application_standard_runtime_inputs_match", "application_standard_instance_runtime_guard",
		"application_standard_native_residency_guard", "application_standard_native_publication_guard",
		"application_standard_enrollment_generation_guard", "application_standard_native_artifact_protocol",
	} {
		var scoped, escaped bool
		err := pool.QueryRow(t.Context(), `SELECT
            EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname=$1 AND n.nspname=current_schema()),
            EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE p.proname=$1 AND n.nspname='public')`, name).Scan(&scoped, &escaped)
		if err != nil || !scoped || escaped {
			t.Fatalf("function %s: scoped=%v escaped=%v err=%v", name, scoped, escaped, err)
		}
	}
	var bound bool
	err := pool.QueryRow(t.Context(), `SELECT proargtypes[0]='apps'::regtype AND proargtypes[1]='deployments'::regtype
        FROM pg_proc WHERE oid='application_standard_runtime_root_producer(apps,deployments,text)'::regprocedure`).Scan(&bound)
	if err != nil || !bound {
		t.Fatal("producer function uses another schema's row types", err)
	}
	if err := db.MigrateUp(t.Context(), pool); err != nil {
		t.Fatal("repeat migration:", err)
	}
}
