//go:build !no_pg

package state_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/db/pgtest"
	"github.com/onebox-faas/faas/pkg/state"
)

func TestPgApplicationStandardVersions(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	standardStoreLifecycle(t, state.NewPgStore(pool))
	for _, query := range []string{
		`UPDATE application_standard_versions SET description = 'changed'`,
		`DELETE FROM application_standard_versions`,
		`DELETE FROM application_standards`,
	} {
		if _, err := pool.Exec(ctx, query); err == nil {
			t.Fatal("database allowed immutable version mutation")
		}
	}
	// Erasing an organization is the explicit retention boundary. It may
	// remove the immutable history through cascading parent ownership.
	if _, err := pool.Exec(ctx, `DELETE FROM orgs WHERE slug = 'standard-test-org'`); err != nil {
		t.Fatalf("organization erasure: %v", err)
	}
}

func TestPgApplicationStandardPublisherErasure(t *testing.T) {
	ctx := context.Background()
	pool := pgtest.Open(t)
	if err := db.MigrateUp(ctx, pool); err != nil {
		t.Fatal(err)
	}
	store := state.NewPgStore(pool)
	actor, err := store.CreateAccount(ctx, "standard-publisher-erasure@example.com", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	org, err := store.CreateOrg(ctx, state.Org{Slug: "standard-retention-org", Name: "Standard retention", Plan: api.PlanPro})
	if err != nil {
		t.Fatal(err)
	}
	version, err := store.PublishApplicationStandardVersion(ctx, state.ApplicationStandardPublish{OrgID: org.ID, ActorID: actor.ID, Slug: "security-baseline", CreateApplicationStandardVersionRequest: api.CreateApplicationStandardVersionRequest{Definition: json.RawMessage(`{"require_signed":{"mode":"mandatory","value":true}}`)}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM orgs WHERE personal_owner_account_id = $1`, actor.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM accounts WHERE id = $1`, actor.ID); err != nil {
		t.Fatalf("publisher erasure: %v", err)
	}
	retained, err := store.GetApplicationStandardVersion(ctx, org.ID, version.Slug, 1)
	if err != nil || retained.DefinitionHash != version.DefinitionHash || retained.CreatedBy != actor.ID {
		t.Fatalf("immutable retained provenance: %+v %v", retained, err)
	}
}
