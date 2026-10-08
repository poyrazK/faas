package credentialdelivery

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"errors"
	"time"

	"filippo.io/age"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/managedpostgres"
	probesql "github.com/onebox-faas/faas/pkg/managedpostgres/credentialdelivery/sqlc"
	"github.com/onebox-faas/faas/pkg/secretbox"
	"github.com/onebox-faas/faas/pkg/state"
)

//go:embed probe_schema.sql
var probeSchema string

type SecretReader interface {
	GetAppSecretInScope(context.Context, string, string, string, string) (*state.AppSecret, error)
}

// Observer reads independently committed encrypted secrets. URI returns are
// private worker input; they must never enter a qualification report or log.
type Observer struct {
	Store         SecretReader
	Identity      *age.X25519Identity
	HMACKey       []byte
	PostgresMajor int
	RunID         string
}

func (o Observer) URI(ctx context.Context, binding managedpostgres.Binding) (string, error) {
	if o.Store == nil || o.Identity == nil || len(o.HMACKey) == 0 {
		return "", managedpostgres.ErrInvalid
	}
	row, err := o.Store.GetAppSecretInScope(ctx, binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey)
	if err != nil || row == nil {
		return "", managedpostgres.ErrUnavailable
	}
	ref, err := Ref(binding)
	if err != nil || row.AccountID != binding.AccountID || row.AppID != binding.AppID || row.Scope != binding.Scope || row.Key != binding.EnvironmentKey || row.ManagedPostgresBindingID != binding.ID || row.ManagedCredentialRef != ref || row.ManagedCredentialGeneration != binding.CredentialGeneration || row.Kid != o.Identity.Recipient().String() {
		return "", managedpostgres.ErrConflict
	}
	envelope, err := secretbox.Open(o.Identity, row.Ciphertext)
	if err != nil || len(envelope) != 1 {
		return "", managedpostgres.ErrUnavailable
	}
	value, ok := envelope[binding.EnvironmentKey]
	if !ok || value == "" || len(value) > MaxCredentialBytes {
		return "", managedpostgres.ErrConflict
	}
	hash, err := secretbox.ValueFingerprint([]byte(value), o.HMACKey)
	if err != nil || subtle.ConstantTimeCompare([]byte(hash), []byte(row.ValueHash)) != 1 {
		return "", managedpostgres.ErrConflict
	}
	return value, nil
}

func (o Observer) Credential(ctx context.Context, binding managedpostgres.Binding) error {
	value, err := o.URI(ctx, binding)
	if err != nil {
		return err
	}
	return managedpostgres.VerifyCredentialSQL(ctx, value, binding.Access, o.PostgresMajor)
}

func (o Observer) Deleted(ctx context.Context, binding managedpostgres.Binding) error {
	if o.Store == nil {
		return managedpostgres.ErrInvalid
	}
	_, err := o.Store.GetAppSecretInScope(ctx, binding.AccountID, binding.AppID, binding.Scope, binding.EnvironmentKey)
	if errors.Is(err, state.ErrNotFound) {
		return nil
	}
	return managedpostgres.ErrConflict
}

// Workload seeds a disposable marker only once. Rotation verification performs
// reads and updates without recreating either the relation or missing data.
func (o Observer) Workload(ctx context.Context, writer, migration managedpostgres.Binding, prepare bool) error {
	run, err := uuid.Parse(o.RunID)
	if err != nil || run == uuid.Nil || writer.DatabaseID != migration.DatabaseID || writer.Access != managedpostgres.CredentialReadWrite || migration.Access != managedpostgres.CredentialMigration {
		return managedpostgres.ErrInvalid
	}
	uri, err := o.URI(ctx, writer)
	if err != nil {
		return err
	}
	connection, err := managedpostgres.ConnectCredentialSQL(ctx, uri)
	if err != nil {
		return managedpostgres.ErrUnavailable
	}
	defer closeProbeConnection(ctx, connection)
	key := pgtype.UUID{Bytes: run, Valid: true}
	markerHash := sha256.Sum256([]byte(o.RunID + "/workload"))
	marker := hex.EncodeToString(markerHash[:])
	q := probesql.New()
	if prepare {
		migrationURI, err := o.URI(ctx, migration)
		if err != nil {
			return err
		}
		admin, err := managedpostgres.ConnectCredentialSQL(ctx, migrationURI)
		if err != nil {
			return managedpostgres.ErrUnavailable
		}
		defer closeProbeConnection(ctx, admin)
		if _, err := admin.Exec(ctx, probeSchema); err != nil {
			return managedpostgres.ErrUnavailable
		}
		if err := q.SeedQualificationMarker(ctx, admin, probesql.SeedQualificationMarkerParams{ID: key, Marker: marker}); err != nil {
			return managedpostgres.ErrUnavailable
		}
	}
	before, err := q.ReadQualificationMarker(ctx, connection, key)
	if err != nil || before.Marker != marker || !prepare && before.Counter < 1 {
		return managedpostgres.ErrConflict
	}
	after, err := q.AdvanceQualificationMarker(ctx, connection, key)
	if err != nil || after != before.Counter+1 {
		return managedpostgres.ErrConflict
	}
	return nil
}

func closeProbeConnection(ctx context.Context, c *pgx.Conn) {
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	_ = c.Close(cleanup)
}
