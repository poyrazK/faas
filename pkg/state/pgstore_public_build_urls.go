package state

import (
	"context"
	"encoding/json"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *PgStore) SetAppPublicBuildURLs(ctx context.Context, account, app string, urls map[string]string) error {
	if !validStandardResourceRead(account, app) || api.ValidatePublicBuildURLs(urls) != nil {
		return ErrInvalidArgument
	}
	if urls == nil {
		urls = map[string]string{}
	}
	raw, err := json.Marshal(urls)
	if err != nil {
		return err
	}
	n, err := sqlc.New().SetAppPublicBuildURLs(ctx, s.pool, sqlc.SetAppPublicBuildURLsParams{AccountID: mustPgUUID(account), ID: mustPgUUID(app), Column3: raw})
	if err != nil {
		return mapErr(err)
	}
	if n != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *PgStore) DeploymentPublicBuildURLs(ctx context.Context, deployment string) (map[string]string, error) {
	if !validStandardResourceRead(deployment, deployment) {
		return nil, ErrInvalidArgument
	}
	raw, err := sqlc.New().GetDeploymentPublicBuildURLs(ctx, s.pool, mustPgUUID(deployment))
	if err != nil {
		return nil, mapErr(err)
	}
	out := map[string]string{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}
