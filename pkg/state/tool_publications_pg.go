package state

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/toolpublicationdb"
	"github.com/onebox-faas/faas/pkg/toolpublication"
)

func (s *PgStore) BeginToolPublication(ctx context.Context, account, id string, in api.ToolPublicationIntent) (api.BeginToolPublicationResponse, error) {
	record, err := newToolPublication(account, id, in)
	if err != nil {
		return api.BeginToolPublicationResponse{}, err
	}
	data, err := json.Marshal(record)
	if err != nil {
		return api.BeginToolPublicationResponse{}, err
	}
	q := toolpublicationdb.New()
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return api.BeginToolPublicationResponse{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = q.LockToolPublicationAccount(ctx, tx, mustPgUUID(account)); err != nil {
		return api.BeginToolPublicationResponse{}, mapErr(err)
	}
	old, err := q.GetToolPublication(ctx, tx, toolpublicationdb.GetToolPublicationParams{AccountID: mustPgUUID(account), ID: id})
	if err == nil {
		if err = json.Unmarshal(old, &record); err != nil {
			return api.BeginToolPublicationResponse{}, err
		}
		if !reflect.DeepEqual(record.Intent, in) {
			return api.BeginToolPublicationResponse{}, ErrConflict
		}
		return api.BeginToolPublicationResponse{Record: toolpublication.View(record, time.Now())}, nil
	}
	if !errors.Is(mapErr(err), ErrNotFound) {
		return api.BeginToolPublicationResponse{}, mapErr(err)
	}
	count, err := q.CountToolPublications(ctx, tx, mustPgUUID(account))
	if err != nil {
		return api.BeginToolPublicationResponse{}, mapErr(err)
	}
	if count >= api.ToolPublicationsPerAccount {
		return api.BeginToolPublicationResponse{}, ErrToolPublicationLimit
	}
	rows, err := q.InsertToolPublication(ctx, tx, toolpublicationdb.InsertToolPublicationParams{AccountID: mustPgUUID(account), ID: id, Payload: data})
	if err != nil {
		return api.BeginToolPublicationResponse{}, mapErr(err)
	}
	if rows == 1 {
		return api.BeginToolPublicationResponse{Created: true, Record: record}, tx.Commit(ctx)
	}
	return api.BeginToolPublicationResponse{}, ErrConflict
}

func (s *PgStore) GetToolPublication(ctx context.Context, account, id string) (api.ToolPublicationRecord, error) {
	data, err := toolpublicationdb.New().GetToolPublication(ctx, s.pool, toolpublicationdb.GetToolPublicationParams{AccountID: mustPgUUID(account), ID: id})
	var out api.ToolPublicationRecord
	if err != nil {
		return out, mapErr(err)
	}
	err = json.Unmarshal(data, &out)
	return toolpublication.View(out, time.Now()), err
}

func (s *PgStore) FinishToolPublication(ctx context.Context, account, id string, outcome api.ToolPublicationOutcome, source string) (api.ToolPublicationRecord, error) {
	var out api.ToolPublicationRecord
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := toolpublicationdb.New()
	data, err := q.LockToolPublication(ctx, tx, toolpublicationdb.LockToolPublicationParams{AccountID: mustPgUUID(account), ID: id})
	if err != nil {
		return out, mapErr(err)
	}
	if err = json.Unmarshal(data, &out); err != nil {
		return out, err
	}
	out, err = completeToolPublication(out, outcome, source)
	if err != nil {
		return out, err
	}
	data, err = json.Marshal(out)
	if err != nil {
		return out, err
	}
	err = q.FinishToolPublication(ctx, tx, toolpublicationdb.FinishToolPublicationParams{AccountID: mustPgUUID(account), ID: id, Status: out.Status, Payload: data})
	if err != nil {
		return out, mapErr(err)
	}
	return out, tx.Commit(ctx)
}

func (s *PgStore) SearchToolRegistry(ctx context.Context, account, query, cursor string, limit int) (ToolPublicationPage, error) {
	out := ToolPublicationPage{Publications: []api.ToolPublicationRecord{}}
	if !validToolRegistryQuery(query, cursor, limit) {
		return out, ErrInvalidArgument
	}
	rows, err := toolpublicationdb.New().SearchToolRegistry(ctx, s.pool, toolpublicationdb.SearchToolRegistryParams{AccountID: mustPgUUID(account), ID: cursor, Limit: int32(limit + 1), Query: query})
	if err != nil {
		return out, mapErr(err)
	}
	for _, data := range rows {
		var record api.ToolPublicationRecord
		if err = json.Unmarshal(data, &record); err != nil {
			return out, err
		}
		out.Publications = append(out.Publications, record)
	}
	if len(out.Publications) > limit {
		out.Publications = out.Publications[:limit]
		out.NextCursor = out.Publications[limit-1].ID
	}
	return out, nil
}
