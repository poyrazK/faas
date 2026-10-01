package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"github.com/onebox-faas/faas/pkg/api"
	schedpkg "github.com/onebox-faas/faas/pkg/sched"
	"github.com/onebox-faas/faas/pkg/state"
)

func admitPlatformTenantInvocation(ctx context.Context, store state.Store, appID string, inv state.Invocation) (state.Invocation, error) {
	if inv.ExclusiveClaim != nil {
		owners, ok := store.(state.ExclusiveWorkStore)
		if !ok {
			return inv, fmt.Errorf("%w: exclusive operation store unavailable", schedpkg.ErrPermanentInvoke)
		}
		claim := *inv.ExclusiveClaim
		op, err := owners.ValidateExclusiveOperation(ctx, claim)
		if err != nil {
			return inv, fmt.Errorf("%w: exclusive operation authority: %w", schedpkg.ErrPermanentInvoke, err)
		}
		parts := strings.Split(claim.IncarnationID, "/")
		if len(parts) != 3 || op.ID != inv.ID || op.AppID != appID || op.PlatformTenantID != inv.PlatformTenantID || parts[0] != inv.InstanceID {
			return inv, fmt.Errorf("%w: exclusive operation dispatch identity mismatch", schedpkg.ErrPermanentInvoke)
		}
		var request api.InvokeRequest
		if err := json.Unmarshal(op.Request, &request); err != nil {
			return inv, fmt.Errorf("%w: stored exclusive request is invalid", schedpkg.ErrPermanentInvoke)
		}
		if request.Method == "" {
			request.Method = http.MethodPost
		}
		if request.Path == "" {
			request.Path = "/"
		}
		inv.Method, inv.Path, inv.Payload, inv.Headers = request.Method, request.Path, request.Payload, request.Headers
		return inv, nil
	}
	if store == nil {
		if _, err := uuid.Parse(inv.ID); err == nil && inv.Source == "esm" {
			return inv, fmt.Errorf("%w: durable queue invocation store unavailable", schedpkg.ErrPermanentInvoke)
		}
		if inv.PlatformTenantID == "" {
			return inv, nil
		}
		return inv, fmt.Errorf("%w: tenant invocation store unavailable", schedpkg.ErrPermanentInvoke)
	}
	admitted, err := state.AdmitPlatformTenantInvocation(ctx, store, appID, inv)
	if err != nil {
		return inv, fmt.Errorf("%w: tenant invocation admission: %w", schedpkg.ErrPermanentInvoke, err)
	}
	return admitted, nil
}
