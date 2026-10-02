package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

var _ ObjectVersionReferenceStore = (*PgStore)(nil)

type objectVersionReferenceInput struct {
	Key      string `json:"object_key"`
	Native   string `json:"native_version_id"`
	Observed bool   `json:"versions_observed"`
}

func (s *PgStore) RecordObjectVersions(ctx context.Context, account, bucket string, items []ObjectVersionIdentity) ([]ObjectVersionIdentity, error) {
	if !validVersionReferences(items) {
		return nil, ErrConflict
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck
	q := sqlc.New()
	if _, err = q.ObjectUsageLockAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return nil, mapErr(err)
	}
	if _, err = q.ObjectVersionBucketOwned(ctx, tx, sqlc.ObjectVersionBucketOwnedParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)}); err != nil {
		return nil, mapErr(err)
	}
	// Marshal a private database input rather than making native IDs serializable
	// on the shared reference type.
	wire := make([]objectVersionReferenceInput, 0, len(items))
	for _, v := range items {
		wire = append(wire, objectVersionReferenceInput{v.Key, v.ProviderVersionID, v.ProviderVersionID != "null" || v.DeleteMarker})
	}
	raw, err := json.Marshal(wire)
	if err != nil {
		return nil, err
	}
	rows, err := q.ObjectVersionReferencesRecord(ctx, tx, sqlc.ObjectVersionReferencesRecordParams{BucketID: mustPgUUID(bucket), Items: raw})
	if err != nil {
		return nil, mapErr(err)
	}
	ids := map[string]string{}
	for _, v := range rows {
		ids[v.ObjectKey+"\x00"+v.NativeVersionID] = pgUUIDString(v.ID)
	}
	out := make([]ObjectVersionIdentity, 0, len(items))
	for _, v := range items {
		v.ID = ids[v.Key+"\x00"+v.ProviderVersionID]
		if !ValidObjectVersionID(v.ID) {
			return nil, ErrConflict
		}
		if v.ProviderVersionID == "null" {
			v.ID = "null"
		}
		out = append(out, v)
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) ResolveObjectVersion(ctx context.Context, account, bucket, key, id string) (string, error) {
	if !ValidObjectVersionID(id) || !validVersionReferenceText(key, api.MaxObjectS3ListTextBytes) {
		return "", ErrNotFound
	}
	q := sqlc.New()
	if id == "null" {
		_, err := q.ObjectVersionBucketOwned(ctx, s.pool, sqlc.ObjectVersionBucketOwnedParams{ID: mustPgUUID(bucket), AccountID: mustPgUUID(account)})
		return "null", mapErr(err)
	}
	idValue, err := q.ObjectVersionReferenceResolve(ctx, s.pool, sqlc.ObjectVersionReferenceResolveParams{ID: mustPgUUID(id), AccountID: mustPgUUID(account), BucketID: mustPgUUID(bucket), ObjectKey: key})
	return idValue, mapErr(err)
}
