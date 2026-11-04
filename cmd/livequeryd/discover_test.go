package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/onebox-faas/faas/pkg/livequery/liveserver"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state/livekeys"
)

func sealLogin(t *testing.T, identity *age.X25519Identity, namespace, dsn string) []byte {
	t.Helper()
	sealed, err := secretbox.SealBytes(identity.Recipient(), namespace, []byte(dsn), 4096)
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

// adr: 20261016151947 — a discovered database is served from its sealed
// login: apid prepared it, so it neither installs nor creates; it releases
// only when its provider names a Neon compute to poll and the process has a
// Neon key; a login sealed for anything else, or to another identity, is
// refused.
func TestDiscoveredDatabase(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	stranger, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "neon")
	if err := os.WriteFile(keyFile, []byte("napi_test"), 0o600); err != nil {
		t.Fatal(err)
	}
	saved, savedDir := *neonKeyFile, *checkpointDir
	*neonKeyFile, *checkpointDir = keyFile, dir
	defer func() { *neonKeyFile, *checkpointDir = saved, savedDir }()
	ids := []*age.X25519Identity{identity}
	row := livekeys.LiveDatabase{DatabaseID: "3f1c0b6e-9a2b-44d1-8c7e-0123456789ab", BackendID: "neon-a", Slot: "gregale_live_3f1c",
		Sealed: sealLogin(t, identity, livekeys.LoginSealNamespace, "postgresql://live:pw@ep-a.neon.tech/neondb"), Awake: "neon:proj-1/ep-a"}

	db, err := discoveredDatabase(row, ids, discovery{releaseAfter: 5 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if db.DSN != "postgresql://live:pw@ep-a.neon.tech/neondb" || db.Slot != "gregale_live_3f1c" || db.Install || db.CreatePublication ||
		db.ReleaseAfter != 5*time.Minute || db.Awake == nil || db.Checkpoint != filepath.Join(dir, row.DatabaseID+".checkpoint") {
		t.Fatalf("discovered %+v", db)
	}
	if db, _ := discoveredDatabase(row, ids, discovery{}); db.ReleaseAfter != 0 || db.Awake != nil {
		t.Error("released without -discover-release-after")
	}
	plain := row
	plain.Awake = ""
	if db, _ := discoveredDatabase(plain, ids, discovery{releaseAfter: time.Minute}); db.ReleaseAfter != 0 {
		t.Error("released a database its provider cannot wake-check")
	}
	// Release 2 seals a login to its database's namespace; this release opens
	// both that and the legacy unbound namespace apid still writes.
	bound := row
	bound.Sealed = sealLogin(t, identity, livekeys.LoginSealNamespaceFor(row.DatabaseID), "postgresql://live:pw@ep-a.neon.tech/neondb")
	if db, err := discoveredDatabase(bound, ids, discovery{}); err != nil || db.DSN != "postgresql://live:pw@ep-a.neon.tech/neondb" {
		t.Fatalf("database-bound login = %+v, %v", db, err)
	}
	for name, bad := range map[string]livekeys.LiveDatabase{
		"a login bound to another database": {DatabaseID: row.DatabaseID,
			Sealed: sealLogin(t, identity, livekeys.LoginSealNamespaceFor("0a1b2c3d-0000-4000-8000-000000000001"), "postgresql://x")},
		"a live key bound to this database": {DatabaseID: row.DatabaseID, Sealed: sealLogin(t, identity, livekeys.KeySealNamespaceFor(row.DatabaseID), "glsk1_x")},
		"a live key, not a login":           {DatabaseID: row.DatabaseID, Sealed: sealLogin(t, identity, livekeys.SealNamespace, "glsk1_x")},
		"another identity's":                {DatabaseID: row.DatabaseID, Sealed: sealLogin(t, stranger, livekeys.LoginSealNamespace, "postgresql://x")},
		"a bad database ID":                 {DatabaseID: "db/1", Sealed: sealLogin(t, identity, livekeys.LoginSealNamespace, "postgresql://x")},
	} {
		if _, err := discoveredDatabase(bad, ids, discovery{}); err == nil {
			t.Errorf("%s: opened", name)
		}
	}
}

// adr: 20261016151947 — the fleet serves what discovery finds beside the
// listed databases: new ones start, changed logins restart, and databases no
// longer discovered stop, lose their route, series and checkpoint. Listed
// databases are left alone.
func TestFleetSync(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reg := prometheus.NewRegistry()
	routes := newRoutes(nil)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	f := newFleet(ctx, routes, liveserver.NewMetricsSet(reg), nil, log)
	dir := t.TempDir()
	// Nothing listens here: each database keeps retrying, which is enough
	// to watch the fleet.
	db := func(id, password string) database {
		return database{ID: id, DSN: "postgres://live:" + password + "@127.0.0.1:1/x?connect_timeout=1", Slot: "gregale_live_" + id,
			Publication: "gregale_live", Checkpoint: filepath.Join(dir, id+".checkpoint")}
	}
	f.start(db("listed", "pw"), true)
	f.sync([]database{db("a", "pw"), db("b", "pw")})
	if got := statusIDs(routes); got != "a,b,listed" {
		t.Fatalf("after discovering a and b: %s", got)
	}
	first := f.members["b"]
	if err := os.WriteFile(filepath.Join(dir, "a.checkpoint"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.sync([]database{db("b", "rotated")})
	if got := statusIDs(routes); got != "b,listed" {
		t.Fatalf("after a disappeared: %s", got)
	}
	if f.members["b"] == first {
		t.Error("a changed login did not restart b")
	}
	if _, err := os.Stat(filepath.Join(dir, "a.checkpoint")); !os.IsNotExist(err) {
		t.Error("a stopped database kept its checkpoint")
	}
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	for _, family := range families {
		for _, m := range family.GetMetric() {
			for _, l := range m.GetLabel() {
				if l.GetName() == "database" && l.GetValue() == "a" {
					t.Fatalf("a's series %s outlived it", family.GetName())
				}
			}
		}
	}
	srv := httptest.NewServer(realtimedMux(routes, func(*liveserver.Hub) http.Handler { return http.NotFoundHandler() }))
	defer srv.Close()
	for path, want := range map[string]int{"/a/live": http.StatusNotFound, "/b/live": http.StatusServiceUnavailable} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
	f.sync(nil)
	if got := statusIDs(routes); got != "listed" {
		t.Fatalf("after discovering nothing: %s", got)
	}
	cancel()
	f.wait()
}

func statusIDs(r *routes) string {
	var ids []string
	for _, s := range r.status() {
		ids = append(ids, s.ID)
	}
	return strings.Join(ids, ",")
}
