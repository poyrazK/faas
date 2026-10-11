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
	if inv.Source == state.InvocationSource("workflow") {
		var headers map[string]string
		if err := json.Unmarshal(inv.Headers, &headers); err != nil {
			return inv, fmt.Errorf("%w: workflow headers are invalid", schedpkg.ErrPermanentInvoke)
		}
		runID := strings.TrimSpace(headers["X-Faas-Workflow-Run-Id"])
		if runID == "" {
			return inv, fmt.Errorf("%w: workflow run identity is missing", schedpkg.ErrPermanentInvoke)
		}
		run, err := store.GetWorkflowRun(ctx, runID)
		if err != nil || run.AppID != appID || run.Status != state.WorkflowRunStatusRunning || run.PlatformTenantID != inv.PlatformTenantID {
			return inv, fmt.Errorf("%w: workflow tenant identity mismatch", schedpkg.ErrPermanentInvoke)
		}
		if inv.PlatformTenantID == "" {
			return inv, nil
		}
		app, err := store.AppByID(ctx, appID)
		if err != nil {
			return inv, fmt.Errorf("%w: workflow app identity unavailable", schedpkg.ErrPermanentInvoke)
		}
		tenants, ok := store.(state.PlatformTenantStore)
		if !ok {
			return inv, fmt.Errorf("%w: workflow tenant store unavailable", schedpkg.ErrPermanentInvoke)
		}
		if err := state.ValidatePlatformTenantAppBinding(ctx, tenants, app.AccountID, inv.PlatformTenantID, appID); err != nil {
			return inv, fmt.Errorf("%w: workflow tenant is no longer authorized for app", schedpkg.ErrPermanentInvoke)
		}
		return inv, nil
	}
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
