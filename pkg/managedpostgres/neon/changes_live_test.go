// adr: 20261112140000
package neon

import (
	"context"
	"net"
	"net/url"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	commitwork "github.com/onebox-faas/faas/pkg/commit"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

type liveChangesAcceptor struct{ events []commitwork.Event }

func (a *liveChangesAcceptor) Accept(_ context.Context, e commitwork.Event) (commitwork.Receipt, error) {
	a.events = append(a.events, e)
	return commitwork.Receipt{ID: uuid.NewString(), InvocationID: uuid.NewString()}, nil
}

// TestLivePostgresChangesManagedInstall is the Neon qualification of
// Postgres change workers (ADR-20261112140000): install as the customer
// schema owner through the pipeline SQL path, capture writes of a separately
// issued read_write login, relay with the SCRAM-verifier login over verified
// TLS under the public-address policy, and uninstall. Opt-in: it creates a
// paid Neon project in an isolated organization and deletes it afterwards.
func TestLivePostgresChangesManagedInstall(t *testing.T) {
	if os.Getenv("FAAS_NEON_LIVE_TEST") != "1" {
		t.Skip("set FAAS_NEON_LIVE_TEST=1 to run the Neon qualification test")
	}
	organizationID := os.Getenv("FAAS_NEON_TEST_ORG_ID")
	regionID := os.Getenv("FAAS_NEON_TEST_REGION_ID")
	if organizationID == "" || regionID == "" || os.Getenv("FAAS_NEON_TEST_API_KEY") == "" {
		t.Fatal("FAAS_NEON_TEST_ORG_ID, FAAS_NEON_TEST_REGION_ID, and FAAS_NEON_TEST_API_KEY are required")
	}
	providerInterface, err := New(managedpostgres.BackendConfig{
		Region: "qualification", Namespace: organizationID,
		Settings: map[string]string{
			settingRegionID: regionID, settingDatabaseName: "gregale",
			settingMaxStorageBytes: "1073741824", settingMaxRestoreWindow: "0",
		},
		SecretEnv: map[string]string{apiKeySecret: "FAAS_NEON_TEST_API_KEY"},
	}, os.Getenv)
	if err != nil {
		t.Fatal(err)
	}
	provider := providerInterface.(*Provider)
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Minute)
	defer cancel()
	resourceID := uuid.NewString()
	providerResourceID := ""
	t.Cleanup(func() {
		cleanupContext, cleanupCancel := context.WithTimeout(context.Background(), time.Minute) //nolint:contextcheck // Cleanup must outlive the test context.
		defer cleanupCancel()
		if providerResourceID == "" {
			providerResourceID, _ = provider.findProject(cleanupContext, provider.projectName(resourceID))
		}
		if providerResourceID != "" {
			_, _ = provider.Delete(cleanupContext, managedpostgres.DeleteRequest{ResourceID: resourceID, ProviderResourceID: providerResourceID, IdempotencyKey: "delete-" + resourceID})
		}
	})
	spec := managedpostgres.Spec{
		Region: "qualification", PostgresMajor: 17, Class: managedpostgres.ClassDevelopment,
		Availability: managedpostgres.AvailabilitySingleZone, ScaleToZero: true, StorageLimitBytes: 1 << 30,
	}
	observed, err := provider.Provision(ctx, managedpostgres.ProvisionRequest{ResourceID: resourceID, Spec: spec, IdempotencyKey: "provision-" + resourceID})
	if err != nil {
		t.Fatal(err)
	}
	providerResourceID = observed.ProviderResourceID
	for observed.Status != managedpostgres.ProviderStatusReady {
		if observed.Status == managedpostgres.ProviderStatusFailed {
			t.Fatal("Neon provisioning reached failed state")
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(2 * time.Second):
		}
		if observed, err = provider.Inspect(ctx, observed.ProviderResourceID); err != nil {
			t.Fatal(err)
		}
	}
	database := managedpostgres.Database{ProviderResourceID: observed.ProviderResourceID, DataResourceID: observed.DataResourceID, Spec: observed.Spec}
	sourceID := uuid.NewString()
	feeds := []commitwork.ChangeFeed{{Name: "orders", Table: "orders", Columns: []string{"id", "status"}}}
	password, err := commitwork.NewRelayPassword()
	if err != nil {
		t.Fatal(err)
	}

	// 1. The application's table, then capture, both as the schema owner.
	var relayURL, relayRole string
	err = provider.WithPipelineSQL(ctx, database, func(conn *pgx.Conn, owner string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := tx.Exec(ctx, "SET LOCAL ROLE "+pgx.Identifier{owner}.Sanitize()); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `CREATE TABLE public.orders (id int PRIMARY KEY, status text NOT NULL, card text)`); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		tx, err = conn.Begin(ctx)
		if err != nil {
			return err
		}
		res, err := commitwork.InstallChanges(ctx, tx, commitwork.ChangesInstall{SourceID: sourceID, Feeds: feeds, RelayPassword: password, SchemaOwner: owner})
		if err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		cfg := conn.Config()
		relayRole = res.RelayRole
		relayURL, err = commitwork.ChangesRelayURL(cfg.Host, cfg.Port, cfg.Database, res.RelayRole, password)
		return err
	})
	if err != nil {
		t.Fatalf("managed install: %v", err)
	}

	// 2. An application login issued after the install writes the table.
	credentialRequest := managedpostgres.CredentialRequest{
		ProviderResourceID: observed.ProviderResourceID, IdentityKey: "changes-app-" + uuid.NewString(),
		Access: managedpostgres.CredentialReadWrite, IdempotencyKey: "changes-app-" + resourceID,
	}
	material, err := provider.IssueCredentials(ctx, credentialRequest)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = provider.RevokeCredentials(context.Background(), credentialRequest) }) //nolint:contextcheck // Cleanup must outlive the test context.
	appURL := url.URL{Scheme: "postgres", User: url.UserPassword(material.Username, material.Password),
		Host: net.JoinHostPort(material.Endpoints[0].Host, strconv.Itoa(int(material.Endpoints[0].Port))), Path: "/" + material.Database, RawQuery: "sslmode=verify-full"}
	app, err := pgx.Connect(ctx, appURL.String())
	if err != nil {
		t.Fatalf("app login: %v", err)
	}
	defer func() { _ = app.Close(context.Background()) }() //nolint:contextcheck // Close after the test context.
	for _, stmt := range []string{
		`INSERT INTO public.orders VALUES (1, 'new', '4242')`,
		`UPDATE public.orders SET status = 'paid' WHERE id = 1`,
		`UPDATE public.orders SET card = '0000' WHERE id = 1`, // not allowlisted: no event
	} {
		if _, err := app.Exec(ctx, stmt); err != nil {
			t.Fatalf("app write refused by capture: %v", err)
		}
	}
	if _, err := app.Exec(ctx, `INSERT INTO public.gregale_outbox(event_id,event_type,payload) VALUES (gen_random_uuid(),'database.orders.insert','{}')`); err == nil {
		t.Fatal("the app login forged a change event")
	}

	// 3. The relay login: SCRAM verifier, verified TLS, public-address policy.
	pool, err := commitwork.OpenPool(ctx, relayURL, commitwork.NetworkPolicy{PublicInternet: true})
	if err != nil {
		t.Fatalf("relay login %s: %v", relayRole, err)
	}
	defer pool.Close()
	if err := commitwork.QualifySchemaVersion(ctx, pool, 1); err != nil {
		t.Fatal(err)
	}
	if err := commitwork.QualifySource(ctx, pool, sourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `SELECT count(*) FROM public.orders`); err == nil {
		t.Fatal("the relay login can read application tables")
	}
	acceptor := &liveChangesAcceptor{}
	relay := commitwork.Relay{ContractVersion: 1, SourceID: sourceID, Pool: pool, Acceptor: acceptor}
	n, err := relay.Tick(ctx)
	if err != nil || n != 2 || len(acceptor.events) != 2 {
		t.Fatalf("relay tick: accepted=%d err=%v events=%+v", n, err, acceptor.events)
	}
	if acceptor.events[0].Type != "database.orders.insert" || acceptor.events[1].Type != "database.orders.update" {
		t.Fatalf("event types %s, %s", acceptor.events[0].Type, acceptor.events[1].Type)
	}
	for _, e := range acceptor.events {
		if containsBytes(e.Data, "4242") || containsBytes(e.Data, "card") {
			t.Fatalf("event carries a column that is not allowlisted: %s", e.Data)
		}
	}
	pool.Close()

	// 4. Uninstall removes capture and the relay login.
	err = provider.WithPipelineSQL(ctx, database, func(conn *pgx.Conn, owner string) error {
		tx, err := conn.Begin(ctx)
		if err != nil {
			return err
		}
		defer func() { _ = tx.Rollback(ctx) }()
		if _, err := commitwork.RemoveChanges(ctx, tx, sourceID, owner); err != nil {
			return err
		}
		return tx.Commit(ctx)
	})
	if err != nil {
		t.Fatalf("managed uninstall: %v", err)
	}
	if again, err := commitwork.OpenPool(ctx, relayURL, commitwork.NetworkPolicy{PublicInternet: true}); err == nil {
		again.Close()
		t.Fatal("the relay login still works after uninstall")
	}
	if _, err := app.Exec(ctx, `UPDATE public.orders SET status = 'shipped' WHERE id = 1`); err != nil {
		t.Fatalf("app write after uninstall: %v", err)
	}
}

func containsBytes(b []byte, s string) bool {
	return len(s) > 0 && len(b) >= len(s) && (string(b) == s || indexOf(string(b), s) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
