// adr: 20261023115741 — the SDKs are generated from the GA and beta spec.
package main

import (
	"bytes"
	"os"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestSDKSpecInSync(t *testing.T) {
	want, err := render()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile("../../sdk/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatal("sdk/openapi.yaml is stale; run 'make sdk-spec' and regenerate the SDKs ('make sdk-gen-node sdk-gen-python')")
	}
}

func TestSDKSpecKeepsNativePostgresOperationsInPreview(t *testing.T) {
	spec, err := render()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"\n  /v1/postgres/databases/{id}/restore:", "\n  /v1/apps/{slug}/database-switches:"} {
		if !strings.Contains(string(spec), path) {
			t.Errorf("public PostgreSQL path %q is missing", strings.TrimSpace(path))
		}
	}
	for _, token := range []string{
		"/query-insights:", "/masked-branches:", "/upgrade-rehearsals:", "/backup-schedule:",
		"/database-switches/migration:", "/database-switches/migration/", "MigrationDatabaseSwitch:", "replica_only", "migration_operation_id:", "masked_branch:",
		"ManagedPostgresStorageStatus:", "ManagedPostgresBackupStatus:", "ManagedPostgresReplicaStatus:",
	} {
		if strings.Contains(string(spec), token) {
			t.Errorf("native PostgreSQL preview token %q leaked into the default SDK", token)
		}
	}
	var doc struct {
		Components struct {
			Schemas map[string]struct{ Required []string }
		}
	}
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(doc.Components.Schemas["DatabaseSwitch"].Required, "restore_point") {
		t.Fatal("the stable database switch requires a restore point")
	}
}

func TestSDKSpecKeepsGAAndDropsPreviewAndOperatorPaths(t *testing.T) {
	spec, err := render()
	if err != nil {
		t.Fatal(err)
	}
	text := string(spec)
	for _, path := range []string{"\n  /v1/apps:", "\n  /v1/apps/{slug}/env:", "\n  /v1/crons:"} {
		if !strings.Contains(text, path) {
			t.Errorf("GA path %q missing from the SDK spec", strings.TrimSpace(path))
		}
	}
	for _, prefix := range []string{
		"\n  /v1/realtime/", "\n  /v1/admin/", "\n  /v1/service-map",
		"\n  /v1/tool-publications", "\n  /v1/tool-registry", "\n  /v1/tool-grants",
		"\n  /v1/tool-invocations", "\n  /v1/delegated-tool-invocations",
		"\n  /v1/delegated-tool-connections", "\n  /v1/tool-oauth-connections",
	} {
		if strings.Contains(text, prefix) {
			t.Errorf("preview, internal or operator path %q is in the SDK spec", strings.TrimSpace(prefix))
		}
	}
}
