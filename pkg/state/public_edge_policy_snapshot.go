// adr: 375
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *publicHostPolicyReader) PublicHostEdgeRules(ctx context.Context, host, account string, routesOnly bool) ([]EdgeRule, error) {
	owner := uuidToPgtype(account)
	if account != "" && !owner.Valid || account == "" && !routesOnly {
		return nil, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadPublicHostEdgeRules(ctx, s.tx, sqlc.ReadPublicHostEdgeRulesParams{
		Host: host, AccountID: owner, RouteOnly: routesOnly,
		MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err == nil && row.Oversized {
		return nil, errors.New("public edge policy exceeds the projection limit")
	}
	key, _ := json.Marshal([]any{host, account, routesOnly})
	s.record("edge-rules:"+string(key), row.Data, err)
	rules, err := decodePublicHostJSON[[]EdgeRule](row.Data, err)
	for index := range rules {
		if rules[index].CorsPresetID != nil && rules[index].Action.CORS != nil {
			rules[index].Action.CORS.CorsPresetID = rules[index].CorsPresetID
		}
	}
	return rules, err
}

func (s *publicHostPolicyReader) GetCorsPresetByID(ctx context.Context, account, id string) (CorsPreset, error) {
	row, err := sqlc.New().ReadPublicHostCorsPreset(ctx, s.tx, sqlc.ReadPublicHostCorsPresetParams{
		AccountID: uuidToPgtype(account), PresetID: uuidToPgtype(id), MaxBytes: api.TrafficPolicyMaxContractBytes})
	if err == nil && row.Oversized {
		return CorsPreset{}, errors.New("public CORS preset exceeds the projection limit")
	}
	key, _ := json.Marshal([]string{account, id})
	s.record("cors-preset:"+string(key), row.Data, err)
	return decodePublicHostJSON[CorsPreset](row.Data, err)
}
