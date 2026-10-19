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
	// The guest only sees secrets_unavailable and its app never starts, so
	// name the refusal here (identifiers and error class only, no values).
	refuse := func(reason string, err error) (string, error) {
		if r.log != nil {
			r.log.Warn("runtime secret execution refused", "instance", instance, "kind", req.Kind, "reason", reason, "err", err)
		}
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	store, ok := r.store.(runtimeSecretsStore)
	processStore, processOK := r.store.(runtimeSecretProcessStore)
	if !ok || !processOK || r.mgr == nil {
		return refuse("store_unavailable", nil)
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil {
		return refuse("instance_unknown", err)
	}
	ctx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, accountID, appID, deploymentID)
	if err != nil || snapshot.DeploymentID != deploymentID || snapshot.AppID != appID || snapshot.AccountID != accountID {
		return refuse("deployment_values_unavailable", err)
	}
	deployment := state.Deployment{ID: deploymentID, AppID: appID, Scope: snapshot.Scope, Sidecars: snapshot.SecretGrants.Sidecars, SecretReloadSignal: snapshot.SecretGrants.ReloadSignal}
	if err = runtimeSecretProcessWorkload(deployment, req.WorkloadName); err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
	}
	enabled := runtimeSecretReloadEnabled(snapshot.SecretGrants, req.WorkloadName)
	if !enabled {
		return refuse("reload_not_enabled", nil)
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
		return refuse("state_write_failed", err)
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
