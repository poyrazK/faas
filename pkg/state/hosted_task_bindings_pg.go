package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/tasktoolbinding"
	"github.com/onebox-faas/faas/pkg/tasktoolsource"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (s *PgStore) ChangeHostedTaskBindings(ctx context.Context, account, id, action string, in HostedTaskBindingChange) (HostedTaskBindingRecord, error) {
	var zero HostedTaskBindingRecord
	if uuid.Validate(account) != nil || !toolpublication.ValidID(id) {
		return zero, ErrInvalidArgument
	}
	ctx, cancel := context.WithTimeout(ctx, api.HostedTaskBindingProbeTimeout)
	defer cancel()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return zero, err
	}
	defer func() {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), api.HostedTaskBindingCleanupTimeout)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	out, err := changeHostedTaskBindingsPG(ctx, tx, account, id, action, in)
	if err != nil {
		return zero, err
	}
	if err := tx.Commit(ctx); err != nil {
		return zero, err
	}
	return cloneHostedTaskBinding(out), nil
}

func changeHostedTaskBindingsPG(ctx context.Context, tx pgx.Tx, account, id, action string, in HostedTaskBindingChange) (HostedTaskBindingRecord, error) {
	var zero HostedTaskBindingRecord
	q := toolpublicationdb.New()
	acct := mustPgUUID(account)
	if _, err := q.LockToolPublicationAccount(ctx, tx, acct); err != nil {
		return zero, mapErr(err)
	}
	source, err := q.LockHostedTaskBindingSource(ctx, tx, toolpublicationdb.LockHostedTaskBindingSourceParams{AccountID: acct, ID: id})
	if err != nil {
		return zero, mapErr(err)
	}
	var retained api.HostedTaskSourceIntent
	if json.Unmarshal(source.Payload, &retained) != nil || !source.ExpiresAt.Valid || !retained.ExpiresAt.Equal(source.ExpiresAt.Time) {
		return zero, ErrInvalidArgument
	}
	now, err := hostedTaskBindingTime(ctx, tx)
	if err != nil {
		return zero, err
	}
	retained = tasktoolsource.View(retained, len(source.Ciphertext) > 0, now)
	if tasktoolsource.ValidateRecord(retained, account, id) != nil {
		return zero, ErrInvalidArgument
	}
	if action != "get" {
		if err := lockHostedTaskBindingOwner(ctx, tx, account, in.Owner); err != nil {
			return zero, err
		}
	}
	body, err := q.LockHostedTaskBinding(ctx, tx, toolpublicationdb.LockHostedTaskBindingParams{AccountID: acct, ID: id})
	exists := err == nil
	if err != nil && !errors.Is(mapErr(err), ErrNotFound) {
		return zero, err
	}
	var r HostedTaskBindingRecord
	if exists {
		if json.Unmarshal(body, &r) != nil || tasktoolbinding.ValidateRecord(r) != nil || !hostedTaskBindingMatchesSource(r, retained) {
			return zero, ErrInvalidArgument
		}
	} else if action != "select" {
		return zero, ErrNotFound
	}
	if action == "revoke" {
		return saveHostedTaskBindingPG(ctx, tx, account, id, r, action, in, now)
	}
	fresh := retained.ReviewStatus == "approved" && retained.SourceState == "retained"
	if fresh {
		review, decodeErr := tasktoolsource.Decode(retained.Review)
		if decodeErr != nil {
			return zero, ErrInvalidArgument
		}
		fresh, err = taskToolReleaseFresh(ctx, tx, account, retained.RequesterKeyID, tasktoolsource.Intent(review), false)
		if err != nil {
			return zero, err
		}
	}
	selection, policy := in.Selection, in.Policy
	if exists {
		selection, policy = r.Selection, r.Policy
	}
	var pins []tasktoolbinding.Pin
	if fresh {
		pins, err = captureHostedTaskPinsPG(ctx, tx, account, retained, selection, policy)
		if err != nil && !errors.Is(err, errHostedTaskBindingDrift) {
			return zero, err
		}
		fresh = err == nil
	}
	// Re-evaluate real key expiry after all native app, credential and catalog
	// waits, while every acquired lock is still held.
	if action != "get" {
		if err := lockHostedTaskBindingOwner(ctx, tx, account, in.Owner); err != nil {
			return zero, err
		}
	}
	if fresh {
		fresh, err = hostedTaskBindingKeysFresh(ctx, tx, account, retained.RequesterKeyID, r.OwnerKeyID)
		if err != nil {
			return zero, err
		}
	}
	now, err = hostedTaskBindingTime(ctx, tx)
	if err != nil {
		return zero, err
	}
	retained = tasktoolsource.View(retained, len(source.Ciphertext) > 0, now)
	fresh = fresh && retained.ReviewStatus == "approved" && retained.SourceState == "retained"
	if !exists {
		if !fresh {
			return zero, ErrConflict
		}
		r, err = tasktoolbinding.New(retained, selection, policy, pins, in.Owner.KeyID, now)
		if err != nil {
			return zero, hostedTaskBindingError(err)
		}
	} else {
		r = refreshHostedTaskBinding(r, retained, fresh && reflect.DeepEqual(pins, r.Pins), now)
	}
	return saveHostedTaskBindingPG(ctx, tx, account, id, r, action, in, now)
}

func hostedTaskBindingTime(ctx context.Context, tx pgx.Tx) (time.Time, error) {
	now, err := toolpublicationdb.New().HostedTaskBindingClock(ctx, tx)
	if err != nil {
		return time.Time{}, err
	}
	if !now.Valid {
		return time.Time{}, ErrInvalidArgument
	}
	return now.Time.UTC().Truncate(time.Microsecond), nil
}
func lockHostedTaskBindingOwner(ctx context.Context, tx pgx.Tx, account string, owner HostedTaskSourceOwner) error {
	if owner.Session {
		if owner.KeyID != "" {
			return ErrNotFound
		}
		return nil
	}
	if uuid.Validate(owner.KeyID) != nil {
		return ErrNotFound
	}
	_, err := toolpublicationdb.New().LockHostedTaskSourceOwnerKey(ctx, tx, toolpublicationdb.LockHostedTaskSourceOwnerKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(owner.KeyID)})
	return mapErr(err)
}
func hostedTaskBindingKeysFresh(ctx context.Context, tx pgx.Tx, account, requester, owner string) (bool, error) {
	q := toolpublicationdb.New()
	_, err := q.LockHostedTaskSourceRequesterKey(ctx, tx, toolpublicationdb.LockHostedTaskSourceRequesterKeyParams{AccountID: mustPgUUID(account), ID: mustPgUUID(requester)})
	if errors.Is(mapErr(err), ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if owner != "" {
		err = lockHostedTaskBindingOwner(ctx, tx, account, HostedTaskSourceOwner{KeyID: owner})
		if errors.Is(err, ErrNotFound) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
	}
	return true, nil
}
func saveHostedTaskBindingPG(ctx context.Context, tx pgx.Tx, account, id string, r HostedTaskBindingRecord, action string, in HostedTaskBindingChange, now time.Time) (HostedTaskBindingRecord, error) {
	var zero HostedTaskBindingRecord
	out, err := applyHostedTaskBinding(r, action, in, now)
	if err != nil {
		return zero, err
	}
	body, err := json.Marshal(out)
	if err != nil || len(body) > api.HostedTaskBindingRecordMaxBytes {
		return zero, ErrInvalidArgument
	}
	n, err := toolpublicationdb.New().SaveHostedTaskBinding(ctx, tx, toolpublicationdb.SaveHostedTaskBindingParams{AccountID: mustPgUUID(account), ID: id, Payload: body, ExpiresAt: pgtype.Timestamptz{Time: out.Public.ExpiresAt, Valid: true}, CheckOwner: action != "get", OwnerSession: in.Owner.Session, OwnerKeyID: in.Owner.KeyID, RequireFresh: out.Public.Status == "selected" || out.Public.Status == "verifying" || out.Public.Status == "passed", RequesterKeyID: out.RequesterKeyID, OriginalOwnerKeyID: out.OwnerKeyID})
	if err != nil {
		return zero, err
	}
	if n != 1 {
		return zero, ErrConflict
	}
	return out, nil
}

func captureHostedTaskPinsPG(ctx context.Context, tx pgx.Tx, account string, source api.HostedTaskSourceIntent, in api.HostedTaskBindingSelection, p tasktoolbinding.Policy) ([]tasktoolbinding.Pin, error) {
	specs, err := hostedTaskSecretSpecs(source, in, p)
	if err != nil {
		return nil, err
	}
	q := toolpublicationdb.New()
	var pins []tasktoolbinding.Pin
	roles := map[string]string{}
	for _, spec := range specs {
		row, err := q.LockHostedTaskBindingSecret(ctx, tx, toolpublicationdb.LockHostedTaskBindingSecretParams{AccountID: mustPgUUID(account), AppID: mustPgUUID(spec.AppID), Key: spec.Key})
		if errors.Is(mapErr(err), ErrNotFound) {
			return nil, errHostedTaskBindingDrift
		}
		if err != nil {
			return nil, err
		}
		secret := AppSecret{AccountID: row.AccountID, AppID: row.AppID, Scope: row.Scope, Key: row.Key, Ciphertext: row.Ciphertext, SecretVersion: row.SecretVersion, ManagedPostgresBindingID: row.BindingID, ManagedCredentialRef: row.CredentialRef, ManagedCredentialGeneration: row.CredentialGeneration, ManagedObjectStorageCredentialID: row.ObjectStorageID}
		if spec.Kind != "key" {
			secret, err = hostedTaskBindingCatalogPG(ctx, tx, account, secret)
			if err != nil {
				return nil, err
			}
		}
		pin, err := hostedTaskSecretPin(account, secret, spec)
		if err != nil {
			return nil, err
		}
		pins = append(pins, pin)
		roles[spec.Role] = spec.AppID
	}
	if !hostedTaskPinsSameDatabase(pins) {
		return nil, errHostedTaskBindingDrift
	}
	for _, n := range hostedTaskNamespaceSpecs(roles, p) {
		row, err := q.ReadHostedTaskBindingNamespace(ctx, tx, toolpublicationdb.ReadHostedTaskBindingNamespaceParams{AppID: mustPgUUID(n.app), EnvKey: n.key})
		if errors.Is(mapErr(err), ErrNotFound) {
			return nil, errHostedTaskBindingDrift
		}
		if err != nil {
			return nil, err
		}
		if !hostedTaskNamespaceMatches(row.ManifestValue, row.ManifestHas, row.EnvValue, row.EnvHas, row.Secret, in.Namespace) {
			return nil, errHostedTaskBindingDrift
		}
	}
	return pins, nil
}
func hostedTaskBindingCatalogPG(ctx context.Context, tx pgx.Tx, account string, s AppSecret) (AppSecret, error) {
	if uuid.Validate(s.ManagedPostgresBindingID) != nil {
		return s, errHostedTaskBindingDrift
	}
	q := toolpublicationdb.New()
	row, err := q.LockHostedTaskBindingCatalog(ctx, tx, toolpublicationdb.LockHostedTaskBindingCatalogParams{AccountID: mustPgUUID(account), ID: mustPgUUID(s.ManagedPostgresBindingID)})
	if errors.Is(mapErr(err), ErrNotFound) {
		return s, errHostedTaskBindingDrift
	}
	if err != nil {
		return s, err
	}
	if row.AccountID != account || row.DatabaseAccountID != account || row.AppID != s.AppID || row.Scope != s.Scope || row.EnvironmentKey != s.Key || row.State != "ready" || row.CredentialRef != s.ManagedCredentialRef || row.CredentialGeneration != s.ManagedCredentialGeneration {
		return s, errHostedTaskBindingDrift
	}
	db, err := q.LockHostedTaskBindingDatabase(ctx, tx, toolpublicationdb.LockHostedTaskBindingDatabaseParams{AccountID: mustPgUUID(account), ID: mustPgUUID(row.DatabaseID)})
	if errors.Is(mapErr(err), ErrNotFound) {
		return s, errHostedTaskBindingDrift
	}
	if err != nil {
		return s, err
	}
	if db.State != "ready" {
		return s, errHostedTaskBindingDrift
	}
	s.ManagedPostgresAccess = row.Access
	s.ManagedPostgresDatabaseID = row.DatabaseID
	return s, nil
}
