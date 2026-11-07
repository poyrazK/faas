// adr: 20261016151947 — replace legacy root logins without losing their slot.
package neon

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/managedpostgres"
)

func TestLegacyLiveLoginUpgradePreservesBindingsAndSlot(t *testing.T) {
	f := newCredentialFixture(t)
	const project = "quiet-river-12345678"
	const pin = project + "/br-original-123"
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/connection_uri") || r.URL.Query().Get("branch_id") != "br-original-123" {
			t.Errorf("live operation followed an alias: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		writeResponse(t, w, 200, map[string]any{"uri": "postgres://" + ownerLogin + ":test@test.invalid/" + f.config.Database + "?sslmode=require"})
	}))
	p.databaseName, p.roles = f.config.Database, f.manager
	manager := &sqlLiveLogins{connect: f.manager.connect}
	p.liveLogins = manager
	old := p.credentialRole(managedpostgres.CredentialRequest{ProviderResourceID: project, IdentityKey: "old-binding", Access: managedpostgres.CredentialReadWrite})
	f.runtime = old
	if err := f.manager.Ensure(t.Context(), f.material, old); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Issue(t.Context(), f.material, old, "gregale_live"); err != nil {
		t.Fatal(err)
	}
	oldLogin := liveLogin(old)
	f.extraRoles = append(f.extraRoles, oldLogin.name)
	oldSession := f.connect(t, oldLogin.name)
	var walLevel string
	if err := f.admin.QueryRow(t.Context(), "SHOW wal_level").Scan(&walLevel); err != nil {
		t.Fatal(err)
	}
	slot := "live_upgrade_" + old.scope[:12]
	var slotPosition string
	if walLevel == "logical" {
		executeSQL(t, f.admin, `SELECT pg_create_logical_replication_slot($1,'pgoutput')`, slot)
		if err := f.admin.QueryRow(t.Context(), `SELECT restart_lsn::text FROM pg_replication_slots WHERE slot_name=$1`, slot).Scan(&slotPosition); err != nil {
			t.Fatal(err)
		}
	}
	registry, backend := datasetTestRegistry(t, p)
	store := managedpostgres.NewMemoryStore()
	database := datasetTestReady(t, store, backend, "upgrade-live", project, pin, time.Now().UTC())
	service, err := managedpostgres.NewService(registry, store, managedpostgres.ServiceOptions{})
	if err != nil {
		t.Fatal(err)
	}
	current, ok, err := service.IssueLiveLogin(t.Context(), database, slot)
	if err != nil || !ok || current.Username == oldLogin.name {
		t.Fatalf("upgrade did not issue the pinned login: ok=%t err=%v", ok, err)
	}
	f.extraRoles = append(f.extraRoles, current.Username)
	if _, err := oldSession.Exec(t.Context(), "SELECT 1"); err == nil {
		t.Fatal("legacy live session survived replacement")
	}
	modern := f.connect(t, current.Username)
	if _, err := modern.Exec(t.Context(), "SET ROLE "+roleIdentifier(old.name)); err != nil {
		t.Fatal("the replacement lost a pre-backfill binding", err)
	}
	if walLevel == "logical" {
		var position string
		if err := f.admin.QueryRow(t.Context(), `SELECT restart_lsn::text FROM pg_replication_slots WHERE slot_name=$1`, slot).Scan(&position); err != nil || position != slotPosition {
			t.Fatal("migration discarded the replication position", err)
		}
	}
	// An N-1 writer can recreate the old identity. Disable must stop both
	// generations before dropping their shared slot, and remain replayable.
	if _, err := manager.Issue(t.Context(), f.material, old, "gregale_live"); err != nil {
		t.Fatal(err)
	}
	oldSession = f.connect(t, oldLogin.name)
	for range 2 {
		if err := service.RevokeLiveLogin(t.Context(), database, slot); err != nil {
			t.Fatal("disable/replay", err)
		}
	}
	if _, err := oldSession.Exec(t.Context(), "SELECT 1"); err == nil {
		t.Fatal("N-1 live session survived disable")
	}
	if _, err := modern.Exec(t.Context(), "SELECT 1"); err == nil {
		t.Fatal("current live session survived disable")
	}
	var leftovers bool
	if err := f.admin.QueryRow(t.Context(), `SELECT EXISTS(SELECT 1 FROM pg_roles WHERE rolname IN ($1,$2)) OR EXISTS(SELECT 1 FROM pg_replication_slots WHERE slot_name=$3)`, oldLogin.name, current.Username, slot).Scan(&leftovers); err != nil || leftovers {
		t.Fatal("disable left a live identity or slot", err)
	}
}

func TestLiveLoginRejectsUnrelatedLegacyScopeBeforeIO(t *testing.T) {
	p := testProvider(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("invalid legacy scope reached the provider: %s", r.URL.Path)
		http.NotFound(w, r)
	}))
	for _, alias := range []string{"another-project-123", "quiet-river-12345678/br-copy-123"} {
		request := managedpostgres.LiveLoginRequest{ProviderResourceID: "quiet-river-12345678/br-original-123", LegacyResourceID: alias, Publication: "gregale_live"}
		if _, err := p.IssueLiveLogin(t.Context(), request); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatal("invalid legacy issuance scope", err)
		}
		if err := p.RevokeLiveLogin(t.Context(), request); !errors.Is(err, managedpostgres.ErrInvalid) {
			t.Fatal("invalid legacy revocation scope", err)
		}
	}
}
