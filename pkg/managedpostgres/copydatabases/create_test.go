// adr:375
package copydatabases

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyarchive"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyinventory"
	"github.com/onebox-faas/faas/pkg/managedpostgres/copyroles"
	"github.com/onebox-faas/faas/pkg/managedpostgres/pgerrors"
	"github.com/onebox-faas/faas/pkg/secretbox"
)

type fixture struct {
	sourceRoot, targetRoot, source, target          *pgx.Conn
	exports                                         copyinventory.ExportPlan
	seed                                            copyroles.Receipt
	baseline                                        copyinventory.Inventory
	pins                                            copyarchive.RestoreTarget
	plan                                            Plan
	dispositions                                    []Disposition
	spaces                                          []TablespaceMapping
	owner, dataOwner, bootstrap, ordinary, template string
	ordinaryOID, templateOID                        uint32
}

func run(t *testing.T, c *pgx.Conn, sql string, args ...any) {
	t.Helper()
	if _, err := c.Exec(t.Context(), sql, args...); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, c *pgx.Conn) copyinventory.Inventory {
	t.Helper()
	cfg := copyinventory.Config{DatabaseName: c.Config().Database, RoleName: c.Config().User}
	cfg.FingerprintKey[0] = 29
	if err := c.QueryRow(t.Context(), "SELECT current_setting('server_version_num')::int/10000,d.oid,r.oid FROM pg_database d,pg_roles r WHERE d.datname=current_database() AND r.rolname=current_user").Scan(&cfg.PostgresMajor, &cfg.DatabaseOID, &cfg.RoleOID); err != nil {
		t.Fatal(err)
	}
	i, err := copyinventory.Read(t.Context(), c, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func newFixture(t *testing.T) *fixture {
	t.Helper()
	sourceURL, targetURL := os.Getenv("DATABASE_URL"), os.Getenv("FAAS_COPY_ROLES_TARGET_DATABASE_URL")
	if sourceURL == "" || targetURL == "" {
		t.Skip("two independent local PostgreSQL 16 clusters required for database contracts")
	}
	f := &fixture{}
	var err error
	f.sourceRoot, err = pgx.Connect(t.Context(), sourceURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.sourceRoot.Close(context.Background()) })
	f.targetRoot, err = pgx.Connect(t.Context(), targetURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.targetRoot.Close(context.Background()) })
	var system [2]string
	for n, c := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
		if !strings.HasPrefix(c.Config().Host, "/") {
			t.Fatal("private Unix sockets required")
		}
		if err = c.QueryRow(t.Context(), "SELECT system_identifier::text FROM pg_control_system()").Scan(&system[n]); err != nil {
			t.Fatal(err)
		}
	}
	if system[0] == system[1] {
		t.Fatal("database contracts require independent physical clusters")
	}
	id := strings.ReplaceAll(uuid.NewString(), "-", "")
	f.owner, f.dataOwner = "db_creator_"+id, "db_owner_"+id
	f.bootstrap, f.ordinary, f.template = "db_boot_"+id, "db/ ?é \" ' ; --"+id, "db_icu_template_"+id
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		for _, c := range []*pgx.Conn{f.source, f.target} {
			if c != nil {
				_ = c.Close(ctx)
			}
		}
		for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
			for _, db := range []string{f.ordinary, f.template, f.bootstrap} {
				var exists bool
				_ = root.QueryRow(ctx, "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", db).Scan(&exists)
				if exists {
					_, _ = root.Exec(ctx, "ALTER DATABASE "+pgx.Identifier{db}.Sanitize()+" IS_TEMPLATE false")
					if _, e := root.Exec(ctx, "DROP DATABASE "+pgx.Identifier{db}.Sanitize()); e != nil {
						t.Error(e)
					}
				}
			}
			for _, r := range []string{f.dataOwner, f.owner} {
				if _, e := root.Exec(ctx, "DROP ROLE IF EXISTS "+pgx.Identifier{r}.Sanitize()); e != nil {
					t.Error(e)
				}
			}
		}
	})
	for _, root := range []*pgx.Conn{f.sourceRoot, f.targetRoot} {
		run(t, root, "CREATE ROLE "+pgx.Identifier{f.owner}.Sanitize()+" LOGIN CREATEROLE CREATEDB")
		run(t, root, "CREATE ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" NOLOGIN")
		run(t, root, "CREATE DATABASE "+pgx.Identifier{f.bootstrap}.Sanitize()+" OWNER "+pgx.Identifier{f.owner}.Sanitize())
	}
	run(t, f.sourceRoot, "CREATE DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" OWNER "+pgx.Identifier{f.dataOwner}.Sanitize()+" TEMPLATE template0 CONNECTION LIMIT 7")
	run(t, f.sourceRoot, "CREATE DATABASE "+pgx.Identifier{f.template}.Sanitize()+" OWNER "+pgx.Identifier{f.dataOwner}.Sanitize()+" TEMPLATE template0 LOCALE_PROVIDER icu ICU_LOCALE 'und' ICU_RULES '&a < b' IS_TEMPLATE true ALLOW_CONNECTIONS false CONNECTION LIMIT 3")
	run(t, f.sourceRoot, "REVOKE ALL ON DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" FROM PUBLIC")
	run(t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" SET timezone TO 'Europe/Istanbul'")
	run(t, f.sourceRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" IN DATABASE "+pgx.Identifier{f.template}.Sanitize()+" SET app.clone_secret TO 'retained-private-stage-value'")
	for _, pair := range []struct {
		root *pgx.Conn
		out  **pgx.Conn
	}{{f.sourceRoot, &f.source}, {f.targetRoot, &f.target}} {
		cfg := pair.root.Config().Copy()
		cfg.User, cfg.Database = f.owner, f.bootstrap
		cfg.RuntimeParams = map[string]string{"search_path": "pg_catalog", "default_transaction_read_only": "off"}
		*pair.out, err = pgx.ConnectConfig(t.Context(), cfg)
		if err != nil {
			t.Fatal(err)
		}
	}
	at := time.Now().UTC().Truncate(time.Microsecond).Add(-time.Hour)
	scope := copyinventory.Scope{PostgresMajor: 16, OperationID: uuid.NewString(), AccountID: uuid.NewString(), ProjectID: uuid.NewString(), SourceDatabaseID: uuid.NewString(), CaptureDatabaseID: uuid.NewString(), SourceVersion: strings.Repeat("a", 64), BackendID: "neon", BackendFingerprint: strings.Repeat("b", 64), SourceProviderResourceID: "source-project", SourceDataResourceID: "source-project/source-branch", ProviderSnapshotID: "snapshot", CaptureProviderResourceID: "native-capture", CapturePoint: at, SnapshotCreatedAt: at.Add(time.Second), CaptureCreatedAt: at.Add(2 * time.Second)}
	sourceInventory := read(t, f.source)
	f.exports, err = sourceInventory.PlanExports(scope, nil)
	if err != nil {
		t.Fatal(err)
	}
	before := read(t, f.target)
	baseRoles, _ := before.RoleCatalogueForWorker()
	sourceRoles, _ := f.exports.RoleCatalogueForWorker()
	baseDBs, _ := before.DatabaseCatalogueForWorker()
	f.pins = copyarchive.RestoreTarget{Scope: scope, OwnerID: uuid.NewString(), ProviderResourceID: "independent-target", DataResourceID: "independent-target/root", EndpointID: "ep-independent", ProviderCreatedAt: at.Add(3 * time.Second), EndpointCreatedAt: at.Add(4 * time.Second), DatabaseName: f.bootstrap, DatabaseOID: baseDBs.DatabaseOID, RoleName: f.owner, RoleOID: baseDBs.RoleOID}
	var roles []copyroles.Disposition
	for _, s := range sourceRoles.Roles {
		var targetOID uint32
		for _, d := range baseRoles.Roles {
			if s.Name == d.Name {
				targetOID = d.OID
			}
		}
		roles = append(roles, copyroles.Disposition{SourceOID: s.OID, ExistingTargetOID: targetOID})
	}
	rolePlan, err := copyroles.NewPlan(f.exports, before, f.pins, roles)
	if err != nil {
		t.Fatal(err)
	}
	f.seed, err = copyroles.Prepare(t.Context(), f.target, rolePlan, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	f.baseline = read(t, f.target)
	src, _ := f.exports.DatabaseCatalogueForWorker()
	base, _ := f.baseline.DatabaseCatalogueForWorker()
	for n, s := range src.Databases {
		choice := Disposition{SourceOID: s.OID, CreateTargetOID: 3000000000 + uint32(n)}
		for _, d := range base.Databases {
			if s.Name == d.Name {
				choice.ExistingTargetOID, choice.CreateTargetOID = d.OID, 0
			}
		}
		f.dispositions = append(f.dispositions, choice)
		if s.Name == f.ordinary {
			f.ordinaryOID = s.OID
		}
		if s.Name == f.template {
			f.templateOID = s.OID
		}
	}
	for _, s := range src.Tablespaces {
		for _, d := range base.Tablespaces {
			if s.Name == d.Name {
				f.spaces = append(f.spaces, TablespaceMapping{s.OID, d.OID})
			}
		}
	}
	f.plan, err = NewPlan(f.exports, f.seed, f.baseline, f.dispositions, f.spaces)
	if err != nil {
		t.Fatal(err)
	}
	return f
}
func (f *fixture) authorize(_ context.Context, t copyarchive.RestoreTarget) error {
	if t != f.pins {
		return pgerrors.ErrConflict
	}
	return nil
}
func (f *fixture) prepare(t *testing.T, id uint32) Receipt {
	t.Helper()
	if err := f.seed.VerifyForWorker(t.Context(), f.target); err != nil {
		t.Fatal("original seed proof", err)
	}
	r, err := Prepare(t.Context(), f.target, f.exports, f.plan, id, f.authorize)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func (f *fixture) exists(t *testing.T, name string) bool {
	t.Helper()
	var b bool
	if err := f.targetRoot.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_database WHERE datname=$1)", name).Scan(&b); err != nil {
		t.Fatal(err)
	}
	return b
}
func (f *fixture) state(t *testing.T, id uint32) string {
	t.Helper()
	cfg := f.targetRoot.Config().Copy()
	cfg.Database = f.bootstrap
	c, err := pgx.ConnectConfig(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close(context.Background())
	var state string
	_ = c.QueryRow(t.Context(), "SELECT state FROM gregale_copy_databases.databases WHERE source_oid=$1::oid", id).Scan(&state)
	return state
}

func TestCopyDatabasesCompleteClosedPreparationAndRetry(t *testing.T) {
	f := newFixture(t)
	originalSource := read(t, f.source)
	s := f.plan.Summary()
	if s.Created != 2 || s.Databases != len(f.dispositions) || s.Templates != 3 || s.Settings != 2 {
		t.Fatalf("incomplete plan: %+v", s)
	}
	raw, err := f.plan.PrivatePayloadForSealing()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("retained-private-stage-value")) || !bytes.Contains(raw, []byte(`\u0026a \u003c b`)) {
		t.Fatal("original scoped config or ICU rules omitted")
	}
	for _, d := range f.dispositions {
		r := f.prepare(t, d.SourceOID)
		child, err := r.TargetForWorker()
		if err != nil {
			t.Fatal(err)
		}
		again := f.prepare(t, d.SourceOID)
		if !r.CreatedAt().Equal(again.CreatedAt()) {
			t.Fatal("original receipt time changed")
		}
		if child.OwnerID != f.pins.OwnerID || child.ProviderResourceID != f.pins.ProviderResourceID || child.EndpointID != f.pins.EndpointID || !child.Scope.Equal(f.pins.Scope) {
			t.Fatal("child placement pins changed")
		}
	}
	i := read(t, f.target)
	c, _ := i.DatabaseCatalogueForWorker()
	for _, id := range []uint32{f.ordinaryOID, f.templateOID} {
		d, _, _ := f.plan.creationDatabase(id)
		var actual copyinventory.Database
		for _, db := range c.Databases {
			if db.OID == d.OID {
				actual = db
			}
		}
		if !reflect.DeepEqual(actual, d) || actual.AllowConnections || actual.Template || actual.OwnerOID != f.pins.RoleOID || actual.OID == id {
			t.Fatal("database is not exact, independently identified and closed")
		}
	}
	if read(t, f.source).Summary().Fingerprint != originalSource.Summary().Fingerprint {
		t.Fatal("source catalogue mutated")
	}
	for _, v := range []any{f.plan, f.prepare(t, f.ordinaryOID), f.dispositions[0], f.spaces[0]} {
		b, _ := json.Marshal(v)
		out := fmt.Sprintf("%+v %#v %s", v, v, b)
		for _, secret := range []string{f.ordinary, f.owner, "retained-private-stage-value", "&a < b"} {
			if strings.Contains(out, secret) {
				t.Fatal("private SQL metadata leaked")
			}
		}
	}
}

func TestCopyDatabasesRejectIncompleteAndUnpinnedPlans(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name   string
		mutate func([]Disposition) []Disposition
	}{
		{"missing", func(d []Disposition) []Disposition { return d[1:] }},
		{"duplicate", func(d []Disposition) []Disposition { d[1] = d[0]; return d }},
		{"unobserved existing", func(d []Disposition) []Disposition {
			for n := range d {
				if d[n].SourceOID == f.ordinaryOID {
					d[n].ExistingTargetOID, d[n].CreateTargetOID = d[n].CreateTargetOID, 0
				}
			}
			return d
		}},
		{"source identity reused", func(d []Disposition) []Disposition {
			for n := range d {
				if d[n].SourceOID == f.ordinaryOID {
					d[n].CreateTargetOID = d[n].SourceOID
				}
			}
			return d
		}},
		{"system identity", func(d []Disposition) []Disposition {
			for n := range d {
				if d[n].SourceOID == f.ordinaryOID {
					d[n].CreateTargetOID = 10
				}
			}
			return d
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewPlan(f.exports, f.seed, f.baseline, tc.mutate(append([]Disposition(nil), f.dispositions...)), f.spaces)
			if err == nil {
				t.Fatal("invalid complete plan accepted")
			}
		})
	}
	if _, err := NewPlan(f.exports, f.seed, f.baseline, f.dispositions, f.spaces[1:]); err == nil {
		t.Fatal("omitted tablespace accepted")
	}
	if _, err := Prepare(t.Context(), f.target, f.exports, f.plan, f.ordinaryOID, nil); !errors.Is(err, pgerrors.ErrInvalid) {
		t.Fatal(err)
	}
	if f.exists(t, f.ordinary) {
		t.Fatal("rejected planning mutated target")
	}
}

func TestCopyDatabasesOriginalSealedRecoveryAndTampering(t *testing.T) {
	f := newFixture(t)
	old, _ := age.GenerateX25519Identity()
	current, _ := age.GenerateX25519Identity()
	s, err := Seal(old.Recipient(), f.plan)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open([]*age.X25519Identity{current, old}, f.exports, f.pins, s)
	if err != nil || !reflect.DeepEqual(opened.body, f.plan.body) {
		t.Fatal("original key recovery failed", err)
	}
	if _, err = Open([]*age.X25519Identity{current}, f.exports, f.pins, s); !errors.Is(err, pgerrors.ErrUnavailable) {
		t.Fatal(err)
	}
	relabel := s
	relabel.KeyID = current.Recipient().String()
	if _, err = Open([]*age.X25519Identity{current, old}, f.exports, f.pins, relabel); err == nil {
		t.Fatal("key relabeling accepted")
	}
	wrong := f.pins
	wrong.OwnerID = uuid.NewString()
	if _, err = Open([]*age.X25519Identity{old}, f.exports, wrong, s); err == nil {
		t.Fatal("replacement owner accepted")
	}
	raw, _ := f.plan.PrivatePayloadForSealing()
	for _, ns := range []string{"gregale-postgres-copy-role-seed-plan-v1", namespace} {
		body := raw
		if ns == namespace {
			body = append(append([]byte(nil), raw...), []byte(" {}")...)
		}
		cipher, e := secretbox.SealBytes(old.Recipient(), ns, body, api.PostgresCopyEnvelopeMaxBytes)
		if e != nil {
			t.Fatal(e)
		}
		damaged := s
		damaged.Ciphertext = cipher
		h := sha256.Sum256(cipher)
		damaged.CiphertextSHA256 = hex.EncodeToString(h[:])
		if _, err = Open([]*age.X25519Identity{old}, f.exports, f.pins, damaged); err == nil {
			t.Fatal("namespace/trailing payload accepted")
		}
	}
	// Detached database/settings input must not mutate the sealed plan.
	c, _ := f.exports.DatabaseCatalogueForWorker()
	for n := range c.Databases {
		c.Databases[n].Name = "mutated"
	}
	c.Settings[0].Config[0] = "mutated"
	after, _ := f.plan.PrivatePayloadForSealing()
	if !bytes.Equal(raw, after) {
		t.Fatal("private plan aliases caller data")
	}
	run(t, f.sourceRoot, "ALTER DATABASE "+pgx.Identifier{f.ordinary}.Sanitize()+" CONNECTION LIMIT 13")
	if _, err = Open([]*age.X25519Identity{old}, f.exports, f.pins, s); err != nil {
		t.Fatal("recovery reread live source", err)
	}
	changed, _ := read(t, f.source).PlanExports(f.pins.Scope, nil)
	if _, err = Open([]*age.X25519Identity{old}, changed, f.pins, s); err == nil {
		t.Fatal("different original inventory accepted")
	}
}

func TestCopyDatabasesRecoverCreatingAndCommittedReplyLoss(t *testing.T) {
	for _, phase := range []string{"creating", "created"} {
		t.Run(phase, func(t *testing.T) {
			f := newFixture(t)
			var failed bool
			authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				if !failed && f.exists(t, f.ordinary) && f.state(t, f.ordinaryOID) == phase {
					failed = true
					return pgerrors.ErrUnavailable
				}
				return f.authorize(ctx, target)
			}
			r, err := Prepare(t.Context(), f.target, f.exports, f.plan, f.ordinaryOID, authorize)
			if !errors.Is(err, pgerrors.ErrUnavailable) || !r.CreatedAt().IsZero() || !failed || !f.exists(t, f.ordinary) {
				t.Fatal("lost reply returned success or removed owned database", err)
			}
			var beforeOID uint32
			if err = f.targetRoot.QueryRow(t.Context(), "SELECT oid FROM pg_database WHERE datname=$1", f.ordinary).Scan(&beforeOID); err != nil {
				t.Fatal(err)
			}
			recovered := f.prepare(t, f.ordinaryOID)
			target, e := recovered.TargetForWorker()
			if e != nil || target.DatabaseOID != beforeOID {
				t.Fatal("recovery replaced physical database", e)
			}
			if !f.prepare(t, f.ordinaryOID).CreatedAt().Equal(recovered.CreatedAt()) {
				t.Fatal("recovery changed first receipt")
			}
		})
	}
}

func TestCopyDatabasesClaimRollbackAndMissingCreateRecovery(t *testing.T) {
	for _, committed := range []bool{false, true} {
		t.Run(fmt.Sprint(committed), func(t *testing.T) {
			f := newFixture(t)
			var failed bool
			authorize := func(ctx context.Context, target copyarchive.RestoreTarget) error {
				// A second connection cannot see an uncommitted claim. The dedicated
				// connection's transaction status identifies the claim commit boundary.
				state := f.state(t, f.ordinaryOID)
				if !failed && !f.exists(t, f.ordinary) && ((committed && state == "creating") || (!committed && state == "reserved" && f.target.PgConn().TxStatus() == 'T')) {
					failed = true
					return pgerrors.ErrUsageStale
				}
				return f.authorize(ctx, target)
			}
			r, err := Prepare(t.Context(), f.target, f.exports, f.plan, f.ordinaryOID, authorize)
			if !errors.Is(err, pgerrors.ErrUsageStale) || !r.CreatedAt().IsZero() || !failed || f.exists(t, f.ordinary) {
				t.Fatal("claim boundary performed unauthorized DDL", err)
			}
			want := "reserved"
			if committed {
				want = "creating"
			}
			if f.state(t, f.ordinaryOID) != want {
				t.Fatal("claim was not durably preserved or rolled back")
			}
			f.prepare(t, f.ordinaryOID)
		})
	}
}

func TestCopyDatabasesStaleLockWaiterAndConcurrentRecovery(t *testing.T) {
	f := newFixture(t)
	second, err := pgx.ConnectConfig(t.Context(), f.target.Config().Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close(context.Background())
	run(t, second, "SELECT pg_advisory_lock(hashtext('gregale copy role seed v1'),0)")
	var stale atomic.Bool
	entered := make(chan struct{}, 1)
	done := make(chan error, 1)
	go func() {
		_, e := Prepare(t.Context(), f.target, f.exports, f.plan, f.ordinaryOID, func(ctx context.Context, target copyarchive.RestoreTarget) error {
			select {
			case entered <- struct{}{}:
			default:
			}
			if stale.Load() {
				return pgerrors.ErrUsageStale
			}
			return f.authorize(ctx, target)
		})
		done <- e
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("waiter did not authenticate")
	}
	// Observe actual lock wait rather than assuming a timing delay.
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting bool
		err = f.targetRoot.QueryRow(t.Context(), "SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid=$1 AND wait_event='advisory')", f.target.PgConn().PID()).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no actual lock wait")
		}
		time.Sleep(5 * time.Millisecond)
	}
	stale.Store(true)
	run(t, second, "SELECT pg_advisory_unlock(hashtext('gregale copy role seed v1'),0)")
	if e := <-done; !errors.Is(e, pgerrors.ErrUsageStale) {
		t.Fatal(e)
	}
	if f.exists(t, f.ordinary) {
		t.Fatal("stale waiter created a database")
	}
	results := make(chan Receipt, 2)
	errs := make(chan error, 2)
	for _, conn := range []*pgx.Conn{f.target, second} {
		go func(c *pgx.Conn) {
			r, e := Prepare(t.Context(), c, f.exports, f.plan, f.ordinaryOID, f.authorize)
			results <- r
			errs <- e
		}(conn)
	}
	for n := 0; n < 2; n++ {
		if e := <-errs; e != nil {
			t.Fatal(e)
		}
	}
	a, b := <-results, <-results
	if a.CreatedAt().IsZero() || !a.CreatedAt().Equal(b.CreatedAt()) {
		t.Fatal("concurrent workers retained different first receipts")
	}
}

func TestCopyDatabasesRejectDriftSharedJournalAndMissingCompleted(t *testing.T) {
	for _, kind := range []string{"extra database", "role drift", "shared journal", "database drift", "missing completed"} {
		t.Run(kind, func(t *testing.T) {
			f := newFixture(t)
			switch kind {
			case "extra database":
				run(t, f.targetRoot, "CREATE DATABASE "+pgx.Identifier{f.template}.Sanitize())
			case "role drift":
				run(t, f.targetRoot, "ALTER ROLE "+pgx.Identifier{f.dataOwner}.Sanitize()+" CREATEDB")
			case "shared journal":
				f.prepare(t, f.templateOID)
				run(t, f.target, "GRANT USAGE ON SCHEMA gregale_copy_databases TO PUBLIC")
			case "database drift":
				f.prepare(t, f.templateOID)
				run(t, f.targetRoot, "ALTER DATABASE "+pgx.Identifier{f.template}.Sanitize()+" ALLOW_CONNECTIONS true")
			case "missing completed":
				f.prepare(t, f.ordinaryOID)
				run(t, f.targetRoot, "DROP DATABASE "+pgx.Identifier{f.ordinary}.Sanitize())
			}
			r, err := Prepare(t.Context(), f.target, f.exports, f.plan, f.ordinaryOID, f.authorize)
			if !errors.Is(err, pgerrors.ErrConflict) || !r.CreatedAt().IsZero() {
				t.Fatal("unqualified target drift accepted", err)
			}
			if f.exists(t, f.ordinary) {
				t.Fatal("rejected target still created/recreated database")
			}
		})
	}
}

func TestCopyDatabasesRecoveredSeedProofRequiresActualTargetJournal(t *testing.T) {
	f := newFixture(t)
	raw, _ := f.plan.PrivatePayloadForSealing()
	var body payload
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	var proof map[string]json.RawMessage
	if err := json.Unmarshal(body.RoleProof, &proof); err != nil {
		t.Fatal(err)
	}
	var receipt map[string]json.RawMessage
	_ = json.Unmarshal(proof["receipt"], &receipt)
	receipt["seeded_at"], _ = json.Marshal(f.seed.SeededAt().Add(time.Second))
	proof["receipt"], _ = json.Marshal(receipt)
	body.RoleProof, _ = json.Marshal(proof)
	forged := Plan{body}
	id, _ := age.GenerateX25519Identity()
	s, err := Seal(id.Recipient(), forged)
	if err != nil {
		t.Fatal(err)
	}
	opened, err := Open([]*age.X25519Identity{id}, f.exports, f.pins, s)
	if err != nil {
		t.Fatal("metadata recovery should defer physical proof", err)
	}
	r, err := Prepare(t.Context(), f.target, f.exports, opened, f.ordinaryOID, f.authorize)
	if !errors.Is(err, pgerrors.ErrConflict) || !r.CreatedAt().IsZero() || f.exists(t, f.ordinary) {
		t.Fatal("substituted seed journal authorized DDL", err)
	}
}
