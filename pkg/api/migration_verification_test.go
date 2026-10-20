package api

import (
	"encoding/json"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestMigrationVerificationValidation(t *testing.T) {
	base := CreateMigrationVerificationRequest{ID: uuid.NewString(), DeploymentID: uuid.NewString(), Checks: MigrationDependencyChecks{PostgresEnvKey: "DATABASE_URL"}, SmokePath: "/ready", ColdWake: true}
	if err := base.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"https://other.test/", "//other.test/path", "/ready?token=secret", "/ready#fragment", ""} {
		r := base
		r.SmokePath = path
		if r.Validate() == nil {
			t.Fatalf("accepted path %q", path)
		}
	}
	if _, ok := ParseMigrationDependenciesCommand(MigrationDependenciesCommand(base.Checks), false); !ok {
		t.Fatal("valid dependency selector rejected")
	}
	if _, ok := ParseMigrationDependenciesCommand([]string{AppTaskMigrationDependenciesCommand, `{"postgres_env_key":"DATABASE_URL","url":"secret"}`}, false); ok {
		t.Fatal("arbitrary probe field accepted")
	}
}

func TestCancelMigrationVerificationWireContract(t *testing.T) {
	id := uuid.NewString()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/apps/shop-api/migration-verifications/"+id+"/cancel" {
			t.Error("wrong cancellation operation")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(MigrationVerification{ID: id, Status: "pending", Phase: "restoring", RecoveryStatus: "pending"}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	run, err := NewClient(server.URL, "test-key").CancelMigrationVerification(t.Context(), "shop-api", id)
	if err != nil || run.ID != id || run.RecoveryStatus != "pending" || run.Status != "pending" {
		t.Fatalf("cancellation lost cleanup state: %+v %v", run, err)
	}
}
