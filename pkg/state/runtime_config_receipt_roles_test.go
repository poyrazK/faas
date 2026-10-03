// adr: 521 — runtime evidence must reflect the actual sealed serving inputs.
// adr: 462 — migration credentials are restricted to persisted release tasks.
package state

import (
	"testing"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
)

func TestRuntimeConfigReceiptServingCredentialAudience(t *testing.T) {
	ctx := t.Context()
	store := NewMemStore()
	account, err := store.CreateAccount(ctx, "receipt-audience-"+uuid.NewString()+"@example.test", api.PlanPro)
	if err != nil {
		t.Fatal(err)
	}
	app, err := store.CreateApp(ctx, App{AccountID: account.ID, Slug: "receipt-audience", Type: AppTypeApp, RAMMB: 256})
	if err != nil {
		t.Fatal(err)
	}
	for key, access := range map[string]string{"DATABASE_URL": "read_write", "MIGRATION_DATABASE_URL": "migration"} {
		err = store.PutManagedPostgresSecret(ctx, AppSecret{AccountID: account.ID, AppID: app.ID, Scope: "default", Key: key,
			Ciphertext: []byte("sealed"), Kid: "recipient", ManagedPostgresBindingID: "binding-" + key,
			ManagedPostgresAccess: access, ManagedCredentialRef: "credential-" + key, ManagedCredentialGeneration: 1})
		if err != nil {
			t.Fatal(err)
		}
	}
	rows, err := store.ListAppSecretsInScope(ctx, account.ID, app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	versions := map[string]int64{}
	for _, row := range rows {
		versions[row.Key] = row.DeliveryVersion
	}
	boundary, _, err := RuntimeConfigChangedAtForScope(ctx, store, app.ID, "default")
	if err != nil {
		t.Fatal(err)
	}
	serving := RuntimeConfigInputs{Scope: "default", Boundary: boundary, Variables: map[string]string{},
		SecretVersions: map[string]int64{"default/DATABASE_URL": versions["DATABASE_URL"]},
		SecretRefs:     map[string]string{"DATABASE_URL": "secret:DATABASE_URL"}, AllSecrets: true}
	for _, implicit := range []bool{false, true} {
		inputs := cloneRuntimeConfigInputs(serving)
		if implicit {
			inputs.SecretRefs = map[string]string{}
		}
		if fresh, err := store.RuntimeConfigInputsFresh(ctx, app.ID, inputs); err != nil || !fresh {
			t.Fatalf("serving implicit=%v fresh=%v err=%v", implicit, fresh, err)
		}
	}
	for _, alias := range []bool{false, true} {
		forged := cloneRuntimeConfigInputs(serving)
		forged.AllSecrets = false
		forged.SecretVersions["default/MIGRATION_DATABASE_URL"] = versions["MIGRATION_DATABASE_URL"]
		if alias {
			forged.SecretRefs["SCHEMA_DSN"] = "secret:MIGRATION_DATABASE_URL"
		}
		if fresh, err := store.RuntimeConfigInputsFresh(ctx, app.ID, forged); err != nil || fresh {
			t.Fatalf("migration alias=%v fresh=%v err=%v", alias, fresh, err)
		}
	}
}
