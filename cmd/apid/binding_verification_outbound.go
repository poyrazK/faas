package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/auth/middleware"
	"github.com/onebox-faas/faas/pkg/state"
)

func (s *server) outboundProbeSnapshots(ctx context.Context, accountID, appID string, count int) (map[string]state.OutboundBindingProbeSnapshot, error) {
	out := map[string]state.OutboundBindingProbeSnapshot{}
	if count == 0 {
		return out, nil
	}
	store, ok := s.store.(state.OutboundBindingProbeStore)
	if !ok {
		return nil, fmt.Errorf("outbound probe catalog unavailable")
	}
	rows, err := store.ListOutboundBindingProbeSnapshots(ctx, accountID, appID)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		out[row.IntegrationID] = row
	}
	return out, nil
}
func (s *server) captureOutboundVerificationPin(r *http.Request, acct state.Account, app state.App, request api.ResolvedCreateAppTaskRequest) (*state.BindingVerificationPin, error) {
	if !middleware.HasScope(r, api.ScopesReadSurface...) || !api.ValidOutboundProbeGateway(s.outboundProbeGatewayURL) {
		return nil, nil
	}
	section := s.outboundBindingInventory(r.Context(), acct.ID, app.ID)
	if len(section.issues) > 0 {
		return nil, fmt.Errorf("outbound probe catalog unavailable")
	}
	stamp, _, err := s.store.AppRuntimeConfigChangedAt(r.Context(), app.ID)
	if err != nil {
		return nil, err
	}
	for _, item := range section.items {
		bindingID := section.privateBindingIDs[bindingVerificationKey(item.Type, item.Name, item.Scope)]
		if bindingID == request.Command[1] && item.OutboundProbe != nil && item.State == "enabled" {
			spec := api.OutboundBindingProbeSpec{IntegrationID: bindingID, GatewayURL: s.outboundProbeGatewayURL, Policy: *item.OutboundProbe}
			base := section.revisions[bindingVerificationKey(item.Type, bindingID, item.Scope)]
			return &state.BindingVerificationPin{Type: api.BindingTypeOutbound, Binding: bindingID, Revision: bindingVerificationRevision(base, stamp), OutboundProbe: &spec}, nil
		}
	}
	return nil, nil
}

func bindingVerificationTaskCommand(command []string, pin *state.BindingVerificationPin) ([]string, error) {
	if pin == nil || pin.OutboundProbe == nil {
		return command, nil
	}
	spec, err := json.Marshal(pin.OutboundProbe)
	if err != nil {
		return nil, err
	}
	return append(append([]string(nil), command...), string(spec)), nil
}
