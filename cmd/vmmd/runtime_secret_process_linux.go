//go:build linux

// adr:438
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/state"
)

type runtimeSecretProcessStore interface {
	BeginAppSecretRuntimeProcess(context.Context, state.AppSecretRuntimeProcess) error
	RetireAppSecretRuntimeProcess(context.Context, state.AppSecretRuntimeProcess) error
}

func validRuntimeSecretProcessRequest(req runtimeConfigRequest) bool {
	return req.Scope == "" && state.ValidSecretRuntimeWorkloadName(req.WorkloadName) && state.ValidSecretProcessGeneration(req.Generation) &&
		(req.PreviousGeneration == "" || req.Kind == "secret_generation_start" && state.ValidSecretProcessGeneration(req.PreviousGeneration)) &&
		req.Revision == "" && req.Projection == "" && req.Signal == "" && req.ErrorCode == "" && req.ApplicationAck == "" && req.ApplicationAckErrorCode == ""
}

func (r *runtimeConfigReceiver) handleRuntimeSecretProcess(instance string, req runtimeConfigRequest, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeSecretsStore)
	processStore, processOK := r.store.(runtimeSecretProcessStore)
	if !ok || !processOK || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	ctx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	deployment, err := store.DeploymentByID(ctx, deploymentID)
	if err != nil || deployment.ID != deploymentID || deployment.AppID != appID {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	if err = runtimeSecretProcessWorkload(deployment, req.WorkloadName); err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
	}
	enabled, err := runtimeSecretReloadEnabled(ctx, store, deployment, req.WorkloadName)
	if err != nil || !enabled {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	p := state.AppSecretRuntimeProcess{AccountID: accountID, AppID: appID, InstanceID: instance, WorkloadName: req.WorkloadName,
		Generation: req.Generation, PreviousGeneration: req.PreviousGeneration, AttemptedAt: time.Now().UTC()}
	if req.Kind == "secret_generation_start" {
		err = processStore.BeginAppSecretRuntimeProcess(ctx, p)
	} else {
		err = processStore.RetireAppSecretRuntimeProcess(ctx, p)
	}
	if errors.Is(err, state.ErrConflict) {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secret_generation_stale"})
	}
	if err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Accepted: true, Generation: req.Generation})
}

// Registration tracks execution identity even when there are no secret grants.
// Secret fetch/ACK authorization continues to use the separate allowlist.
func runtimeSecretProcessWorkload(deployment state.Deployment, workload string) error {
	if workload == "" {
		return nil
	}
	var sidecars []api.Sidecar
	if err := json.Unmarshal(deployment.Sidecars, &sidecars); err != nil {
		return fmt.Errorf("decode execution workloads: %w", err)
	}
	for _, sidecar := range sidecars {
		if sidecar.Name == workload && sidecar.Type == api.SidecarTypeSidecar {
			return nil
		}
	}
	return errors.New("secret execution workload is not a declared sidecar")
}
