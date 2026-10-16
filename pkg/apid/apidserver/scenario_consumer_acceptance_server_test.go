//go:build !no_pg && !windows

package apidserver_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/apid/apidserver"
)

// This subprocess fixture exposes the production route table, authentication and
// PgStore to the CLI acceptance binary without exporting test hooks in apid.
func TestScenarioConsumerAcceptanceServer(t *testing.T) {
	// adr: 984 — CLI recovery must cross the authenticated HTTP and SQL boundary.
	if os.Getenv("GREGALE_CONSUMER_ACCEPTANCE_SERVER") != "1" {
		t.Skip("subprocess fixture only")
	}
	if os.Getenv("DATABASE_URL") == "" || os.Getenv("FAAS_SKIP_PG_TESTS") != "" {
		t.Fatal("acceptance requires PostgreSQL")
	}
	e := apidserver.SetupPGHandler(t, api.PlanPro)
	server := httptest.NewServer(*e.H())
	defer server.Close()
	var database, schema string
	if err := (*e.Pool()).QueryRow(context.Background(), `select current_database(),current_schema()`).Scan(&database, &schema); err != nil {
		t.Fatal(err)
	}
	// FD 3 is a private pipe: the bearer is never written to logs or bundle files.
	ready := os.NewFile(3, "acceptance-ready")
	if ready == nil {
		t.Fatal("readiness pipe unavailable")
	}
	defer ready.Close()
	if err := json.NewEncoder(ready).Encode(struct{ URL, Token, AccountID, Database, Schema string }{server.URL, *e.Key(), e.Acct().ID, database, schema}); err != nil {
		t.Fatal("write readiness", err)
	}
	if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
		t.Fatal(err)
	}
}
