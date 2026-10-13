//go:build !no_pg

// adr: 590
package apidserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/onebox-faas/faas/migrations"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectstorage"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/state/statetest"
)

type checkpointPgConnectionFailureStore struct {
	*checkpointCredentialFailureStore
	loseReserve, loseConnectionSeal bool
}

func (s *checkpointPgConnectionFailureStore) ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(ctx context.Context, l state.ProjectEnvironmentCloneLease, id string, p state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	in, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(ctx, l, id, p)
	if err == nil && s.loseReserve {
		s.loseReserve = false
		return state.ProjectEnvironmentCloneCheckpointPgConnectionValue{}, objectstorage.ErrUnavailable
	}
	return in, err
}
func (s *checkpointPgConnectionFailureStore) SealProjectEnvironmentCloneCheckpointPgConnectionValue(ctx context.Context, l state.ProjectEnvironmentCloneLease, id, kid string, cipher []byte, p state.ProjectEnvironmentCloneCheckpointPgConnectionProof) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, error) {
	in, err := s.PgStore.SealProjectEnvironmentCloneCheckpointPgConnectionValue(ctx, l, id, kid, cipher, p)
	if err == nil && s.loseConnectionSeal {
		s.loseConnectionSeal = false
		return state.ProjectEnvironmentCloneCheckpointPgConnectionValue{}, objectstorage.ErrUnavailable
	}
	return in, err
}

type checkpointPgConnectionTestDriver struct {
	*checkpointCredentialTestDriver
	endpoints      map[string]state.ProjectEnvironmentCloneCheckpointPgEndpointObservation
	editEndpoint   func(*state.ProjectEnvironmentCloneCheckpointPgEndpointObservation)
	beforeEndpoint func()
	host           string
}

func (d *checkpointPgConnectionTestDriver) ObserveCheckpointPgEndpoint(_ context.Context, r state.ProjectEnvironmentCloneCheckpointPostgresCredentialRequest, until time.Time) (state.ProjectEnvironmentCloneCheckpointPgEndpointObservation, error) {
	if d.beforeEndpoint != nil {
		d.beforeEndpoint()
	}
	id := state.ProjectEnvironmentCloneCheckpointPgEndpointRequestID(r.Plan)
	out, ok := d.endpoints[id]
	if !ok {
		host := d.host
		if host == "" {
			host = "private-stage.example"
		}
		out = state.ProjectEnvironmentCloneCheckpointPgEndpointObservation{RequestID: id, CredentialPlanSHA256: state.ProjectEnvironmentCloneCheckpointPostgresCredentialPlanSHA256(r.Plan), CredentialCiphertextSHA256: r.CiphertextSHA256, EndpointID: "endpoint-" + r.Plan.TargetBindingID, OwnerID: r.Plan.Runtime.Verification.Isolation.OwnerID, NativeResourceID: r.Plan.Runtime.Verification.Isolation.NativeResourceID, DataResourceID: r.Plan.DataResourceID, ProviderIdentityID: r.Plan.ProviderIdentityID, Host: host, Database: "stage_data", Port: 5432, PrivateScope: true, TLSVerified: true, IdentityDisabled: true, NoSourceAuthority: true, SQLScopeUnchanged: true, AssignedAt: time.Now().UTC().Truncate(time.Microsecond)}
		d.endpoints[id] = out
	}
	out.ValidThrough = until.Add(time.Hour)
	if d.editEndpoint != nil {
		d.editEndpoint(&out)
	}
	return out, nil
}
func checkpointPgConnectionFixture(t *testing.T) (*cloneCoordinatorFixture, *checkpointPgConnectionFailureStore, *checkpointPgConnectionTestDriver, string, string) {
	t.Helper()
	f, s, d, bindingID, id := checkpointCredentialFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPostgresCredentials(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	store := &checkpointPgConnectionFailureStore{checkpointCredentialFailureStore: s}
	f.srv.store = statetest.NewPg(store)
	return f, store, &checkpointPgConnectionTestDriver{checkpointCredentialTestDriver: d, endpoints: make(map[string]state.ProjectEnvironmentCloneCheckpointPgEndpointObservation)}, bindingID, id
}
func checkpointPgConnectionJournal(t *testing.T, f *cloneCoordinatorFixture, s *checkpointPgConnectionFailureStore, d *checkpointPgConnectionTestDriver, id, want string) (state.ProjectEnvironmentCloneCheckpointPgConnectionValue, state.ProjectEnvironmentCloneCheckpointPostgresCredential, state.ProjectEnvironmentCloneCheckpointPgConnectionProof) {
	t.Helper()
	c, err := s.PgStore.ReserveProjectEnvironmentCloneCheckpointPostgresCredential(t.Context(), f.lease, id)
	if err != nil {
		t.Fatal(err)
	}
	proof, err := f.srv.checkpointPgConnectionProof(t.Context(), f.lease, d, c)
	if err != nil {
		t.Fatal(err)
	}
	in, err := s.ReserveProjectEnvironmentCloneCheckpointPgConnectionValue(t.Context(), f.lease, id, proof)
	if err != nil || in.State != want {
		t.Fatal("connection journal", in.State, err)
	}
	var bindings, secrets int
	if err = f.pool.QueryRow(t.Context(), "select count(*) from managed_postgres_bindings where id=$1", c.Plan.TargetBindingID).Scan(&bindings); err != nil {
		t.Fatal(err)
	}
	if err = f.pool.QueryRow(t.Context(), "select count(*) from app_secrets where managed_postgres_binding_id=$1", c.Plan.TargetBindingID).Scan(&secrets); err != nil || bindings != 0 || secrets != 0 {
		t.Fatal("private connection value was published")
	}
	return in, c, proof
}
func assertCheckpointPgConnectionEnvelope(t *testing.T, in state.ProjectEnvironmentCloneCheckpointPgConnectionValue, c state.ProjectEnvironmentCloneCheckpointPostgresCredential, key *age.X25519Identity) {
	t.Helper()
	namespace, raw, err := secretbox.OpenBytes(key, in.Ciphertext)
	if err != nil {
		t.Fatal("sealed connection unreadable")
	}
	defer clear(raw)
	var envelope checkpointPgConnectionEnvelope
	if json.Unmarshal(raw, &envelope) != nil {
		t.Fatal("connection envelope")
	}
	defer clear(envelope.ConnectionValue)
	if namespace != checkpointPgConnectionNamespace || envelope.PlanSHA256 != state.ProjectEnvironmentCloneCheckpointPgConnectionPlanSHA256(in.Plan) {
		t.Fatal("unbound connection value")
	}
	ns, rawCredential, err := secretbox.OpenBytes(key, c.Ciphertext)
	if err != nil {
		t.Fatal("original credential unreadable")
	}
	defer clear(rawCredential)
	var credential checkpointPostgresCredentialEnvelope
	if json.Unmarshal(rawCredential, &credential) != nil || ns != checkpointPostgresCredentialNamespace {
		t.Fatal("credential envelope")
	}
	defer clear(credential.Password)
	parsed, err := url.Parse(string(envelope.ConnectionValue))
	if err != nil {
		t.Fatal("connection parse")
	}
	if parsed.User == nil {
		t.Fatal("connection missing user")
	}
	password, ok := parsed.User.Password()
	if !ok || password != string(credential.Password) || parsed.Scheme != "postgresql" || parsed.User.Username() != c.Plan.ProviderIdentityID || parsed.Hostname() != in.Plan.Endpoint.Host || parsed.Port() != "5432" || parsed.Path != "/stage_data" || parsed.RawQuery != "sslmode=verify-full" || parsed.Fragment != "" {
		t.Fatal("connection value differs from original target plan")
	}
	plan, err := json.Marshal(in.Plan)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(plan, credential.Password) || bytes.Contains(in.Ciphertext, credential.Password) || bytes.Contains(in.Ciphertext, envelope.ConnectionValue) {
		t.Fatal("plaintext persisted outside sealed envelope")
	}
}
func TestPGCloneCheckpointPgConnectionLostReplies(t *testing.T) {
	for _, kind := range []string{"reserve", "seal"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, id := checkpointPgConnectionFixture(t)
			if kind == "reserve" {
				s.loseReserve = true
			} else {
				s.loseConnectionSeal = true
			}
			var err error
			f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
			if !errors.Is(err, objectstorage.ErrUnavailable) {
				t.Fatal("expected lost connection reply", err)
			}
			want := "planned"
			if kind == "seal" {
				want = "sealed"
			}
			original, _, _ := checkpointPgConnectionJournal(t, f, s, d, bindingID, want)
			reclaimCommonCheckpointWorker(t, f)
			before := f.lease.Operation
			if _, err = f.pool.Exec(t.Context(), "update managed_postgres_databases set state='provisioning' where id=$1", id); err != nil {
				t.Fatal(err)
			}
			f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
			if err != nil || d.prepares != d.bindingCount || !reflect.DeepEqual(before, f.lease.Operation) {
				t.Fatal("connection recovery changed original capture", err)
			}
			in, c, _ := checkpointPgConnectionJournal(t, f, s, d, bindingID, "sealed")
			if !reflect.DeepEqual(in.Plan, original.Plan) || kind == "seal" && (!bytes.Equal(in.Ciphertext, original.Ciphertext) || in.KeyID != original.KeyID) {
				t.Fatal("connection recovery replaced original envelope")
			}
			assertCheckpointPgConnectionEnvelope(t, in, c, d.key)
			setSecretRecipient = nil
			mfaIdentities = nil
			f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("sealed replay required credential keys", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgConnectionIPv6(t *testing.T) {
	f, s, d, bindingID, _ := checkpointPgConnectionFixture(t)
	d.host = "2001:db8::1"
	var err error
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	in, c, _ := checkpointPgConnectionJournal(t, f, s, d, bindingID, "sealed")
	assertCheckpointPgConnectionEnvelope(t, in, c, d.key)
}
func TestPGCloneCheckpointPgConnectionRejectsEndpoint(t *testing.T) {
	cases := map[string]func(*state.ProjectEnvironmentCloneCheckpointPgEndpointObservation){
		"request": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.RequestID = "source" },
		"credential-plan": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) {
			o.CredentialPlanSHA256 = strings.Repeat("f", 64)
		},
		"credential-material": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) {
			o.CredentialCiphertextSHA256 = strings.Repeat("f", 64)
		},
		"owner":            func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.OwnerID = "source" },
		"native":           func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.NativeResourceID = "source" },
		"dataset":          func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.DataResourceID = "source" },
		"identity":         func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.ProviderIdentityID = "source" },
		"tls":              func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.TLSVerified = false },
		"login":            func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.IdentityDisabled = false },
		"scope":            func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.PrivateScope = false },
		"source-authority": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.NoSourceAuthority = false },
		"sql-roster":       func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.SQLScopeUnchanged = false },
		"horizon":          func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.ValidThrough = time.Now() },
		"host-options": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) {
			o.Host = "stage.example?sslmode=disable"
		},
		"database-path": func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.Database = "stage/data" },
		"port":          func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.Port = 0 },
	}
	for name, edit := range cases {
		t.Run(name, func(t *testing.T) {
			f, _, d, _, _ := checkpointPgConnectionFixture(t)
			d.editEndpoint = edit
			if _, err := f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("untrusted endpoint accepted", err)
			}
			var count int
			if err := f.pool.QueryRow(t.Context(), "select count(*) from project_environment_clone_checkpoint_pg_connection_values where operation_id=$1", f.lease.Operation.ID).Scan(&count); err != nil || count != 0 {
				t.Fatal("invalid endpoint reserved connection ownership")
			}
			d.editEndpoint = nil
			var err error
			f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("valid endpoint recovery", err)
			}
		})
	}
}
func TestPGCloneCheckpointPgConnectionRechecksAfterSealing(t *testing.T) {
	for _, kind := range []string{"endpoint", "credential", "runtime", "worker"} {
		t.Run(kind, func(t *testing.T) {
			f, s, d, bindingID, _ := checkpointPgConnectionFixture(t)
			observes := 0
			d.beforeEndpoint = func() {
				observes++
				trigger := 2
				if kind == "credential" || kind == "runtime" {
					trigger = 1
				}
				if observes != trigger {
					return
				}
				switch kind {
				case "endpoint":
					d.editEndpoint = func(o *state.ProjectEnvironmentCloneCheckpointPgEndpointObservation) { o.Host = "replacement.example" }
				case "credential":
					d.editCredential = func(o *state.ProjectEnvironmentCloneCheckpointPostgresCredentialObservation) {
						o.IdentityDisabled = false
					}
				case "runtime":
					d.editRuntime = func(o *state.ProjectEnvironmentCloneCheckpointPostgresRuntimeObservation) { o.RuntimeReady = false }
				case "worker":
					reclaimCommonCheckpointWorker(t, f)
				}
			}
			if _, err := f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
				t.Fatal("late proof change accepted", err)
			}
			var status string
			if err := f.pool.QueryRow(t.Context(), "select state from project_environment_clone_checkpoint_pg_connection_values where operation_id=$1 and source_binding_id=$2", f.lease.Operation.ID, bindingID).Scan(&status); err != nil || status != "planned" {
				t.Fatal("late failure persisted sealed value", err)
			}
			d.beforeEndpoint = nil
			d.editEndpoint = nil
			d.editCredential = nil
			d.editRuntime = nil
			var err error
			f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
			if err != nil {
				t.Fatal("late failure recovery", err)
			}
			checkpointPgConnectionJournal(t, f, s, d, bindingID, "sealed")
		})
	}
}
func TestPGCloneCheckpointPgConnectionKeyRotationAndTampering(t *testing.T) {
	f, s, d, bindingID, _ := checkpointPgConnectionFixture(t)
	originalKey := d.key
	current, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	setSecretRecipient = func() *age.X25519Recipient { return current.Recipient() }
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current} }
	if _, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d); !errors.Is(err, errCloneCheckpointUnavailable) {
		t.Fatal("missing original key sealed connection")
	}
	checkpointPgConnectionJournal(t, f, s, d, bindingID, "planned")
	mfaIdentities = func() []*age.X25519Identity { return []*age.X25519Identity{current, nil, originalKey} }
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	in, _, proof := checkpointPgConnectionJournal(t, f, s, d, bindingID, "sealed")
	if in.KeyID != current.Recipient().String() {
		t.Fatal("connection did not use current recipient")
	}
	namespace, raw, err := secretbox.OpenBytes(current, in.Ciphertext)
	if err != nil || namespace != checkpointPgConnectionNamespace {
		t.Fatal("rotated connection unreadable")
	}
	clear(raw)
	modified := append([]byte(nil), in.Ciphertext...)
	modified[len(modified)-1] ^= 1
	if _, err = s.SealProjectEnvironmentCloneCheckpointPgConnectionValue(t.Context(), f.lease, bindingID, in.KeyID, modified, proof); !errors.Is(err, state.ErrConflict) {
		t.Fatal("sealed connection replaced")
	}
	if _, err = s.SealProjectEnvironmentCloneCheckpointPgConnectionValue(t.Context(), f.lease, bindingID, in.KeyID, make([]byte, api.PostgresCheckpointConnectionCiphertextMaxBytes+1), proof); !errors.Is(err, state.ErrInvalidArgument) {
		t.Fatal("oversized connection accepted")
	}
}
func TestPGCloneCheckpointPgConnectionLateFailureAndMigration(t *testing.T) {
	f, s, d, bindingID, _ := checkpointPgConnectionFixture(t)
	raw, err := migrations.FS.ReadFile("20261011232006336_clone_checkpoint_pg_connection_values.sql")
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.SplitN(string(raw), "-- +goose Down", 2)
	tx, err := f.pool.Begin(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	publication, err := migrations.FS.ReadFile("20261011232006343_clone_checkpoint_pg_publications.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(publication), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	activation, err := migrations.FS.ReadFile("20261011232006339_clone_checkpoint_pg_activations.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(t.Context(), strings.SplitN(string(activation), "-- +goose Down", 2)[1]); err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{parts[1], parts[0], parts[1]} {
		if _, err = tx.Exec(t.Context(), sql); err != nil {
			_ = tx.Rollback(t.Context())
			t.Fatal(err)
		}
	}
	_ = tx.Rollback(t.Context())
	if _, err = f.pool.Exec(t.Context(), `create function reject_sealed_connection() returns trigger language plpgsql as $$ begin if NEW.state='sealed' then raise exception 'injected late connection failure' using errcode='40001'; end if; return NEW; end $$;
 create trigger reject_sealed_connection before update on project_environment_clone_checkpoint_pg_connection_values for each row execute function reject_sealed_connection()`); err != nil {
		t.Fatal(err)
	}
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err == nil {
		t.Fatal("late connection failure ignored")
	}
	checkpointPgConnectionJournal(t, f, s, d, bindingID, "planned")
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded planned connection ownership")
	}
	var pgerr *pgconn.PgError
	if !errors.As(err, &pgerr) || pgerr.Code != "23514" || pgerr.ConstraintName != "clone_checkpoint_pg_connection_values_down_no_ownership" {
		t.Fatal("wrong connection downgrade guard", err)
	}
	if _, err = f.pool.Exec(t.Context(), "drop trigger reject_sealed_connection on project_environment_clone_checkpoint_pg_connection_values; drop function reject_sealed_connection()"); err != nil {
		t.Fatal(err)
	}
	reclaimCommonCheckpointWorker(t, f)
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal("late connection recovery", err)
	}
	in, c, _ := checkpointPgConnectionJournal(t, f, s, d, bindingID, "sealed")
	assertCheckpointPgConnectionEnvelope(t, in, c, d.key)
	if _, err = f.pool.Exec(t.Context(), parts[1]); err == nil {
		t.Fatal("downgrade discarded sealed connection ownership")
	}
}

func TestPGCloneCheckpointPgConnectionRetirementAllowsForkCleanup(t *testing.T) {
	f, s, d, _, _ := checkpointPgConnectionFixture(t)
	var err error
	f.lease, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d)
	if err != nil {
		t.Fatal(err)
	}
	compensateCheckpointCredentialFixture(t, f, s.checkpointCredentialFailureStore)
	retirement := &checkpointCredentialRetirementTestDriver{checkpointCredentialTestDriver: d.checkpointCredentialTestDriver, receipts: make(map[string]state.ProjectEnvironmentCloneCheckpointPostgresCredentialRetirementObservation)}
	setSecretRecipient = nil
	mfaIdentities = nil
	f.lease, err = f.srv.retireCheckpointPostgresCredentials(t.Context(), f.lease, retirement)
	if err != nil {
		t.Fatal(err)
	}
	cleanup := &checkpointPgTargetCompensationTestDriver{t: t, f: f}
	f.lease, err = f.srv.compensateCheckpointPostgresTargets(t.Context(), f.lease, cleanup)
	if err != nil {
		t.Fatal("sealed values blocked retired credential cleanup", err)
	}
	if _, err = f.srv.prepareCheckpointPgConnectionValues(t.Context(), f.lease, d); !errors.Is(err, state.ErrConflict) {
		t.Fatal("compensating worker prepared connection values", err)
	}
}
