package faasschema_test

import (
	"strings"
	"testing"

	faasschema "github.com/onebox-faas/faas"
	"github.com/onebox-faas/faas/migrations"
)

func TestCanonicalSchemaBoundToMigrationSources(t *testing.T) {
	digest, err := migrations.SourceDigest()
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(faasschema.MigrationSourceSHA256()) != digest {
		t.Fatal("regenerate schema-dump: canonical schema is stale for the embedded migration set")
	}
	if !strings.Contains(faasschema.CanonicalSQL(), "application_standard_ledger_recoveries") {
		t.Fatal("canonical schema omitted the immutable recovery receipt")
	}
}
