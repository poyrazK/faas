// adr: 570
package state

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state/sqlc"
)

func (s *publicHostPolicyReader) PublicHostEdgeRules(ctx context.Context, host, account string, routesOnly bool) ([]EdgeRule, error) {
	return s.PublicHostAppEdgeRules(ctx, host, account, "", routesOnly)
}

// PublicHostAppEdgeRules also returns appID's app-wide rules (ADR-1191): the
// host serves that app, so its "@app" rules join the host's set in order.
func (s *publicHostPolicyReader) PublicHostAppEdgeRules(ctx context.Context, host, account, appID string, routesOnly bool) ([]EdgeRule, error) {
	owner, app := uuidToPgtype(account), uuidToPgtype(appID)
	if account != "" && !owner.Valid || account == "" && !routesOnly || appID != "" && !app.Valid {
		return nil, ErrInvalidArgument
	}
	row, err := sqlc.New().ReadPublicHostEdgeRules(ctx, s.tx, sqlc.ReadPublicHostEdgeRulesParams{
		Host: host, AccountID: owner, AppID: app, RouteOnly: routesOnly,
		MaxRows: api.TrafficPolicyMaxHostRules, MaxBytes: api.TrafficPolicyMaxHostBytes})
	if err == nil && row.Oversized {
		return nil, errors.New("public edge policy exceeds the projection limit")
	}
	parts := []any{host, account, routesOnly}
	if appID != "" {
		parts = append(parts, appID)
	}
	key, _ := json.Marshal(parts)
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
