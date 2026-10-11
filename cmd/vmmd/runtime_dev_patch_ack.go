package main

import (
	"context"
	"net"
	"regexp"
	"time"
)

const runtimeDevPatchAckKind = "dev_patch_ack"

var runtimeDevPatchErrorCode = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func validRuntimeDevPatchAck(req runtimeConfigRequest) bool {
	return req.Scope == "" && req.PatchGeneration > 0 && req.PatchApplyMS >= 0 && req.PatchApplyMS <= 600000 &&
		(req.ErrorCode == "" || runtimeDevPatchErrorCode.MatchString(req.ErrorCode)) &&
		req.WorkloadName == "" && req.Revision == "" && req.Projection == "" && req.Signal == "" &&
		req.ApplicationAck == "" && req.ApplicationAckErrorCode == "" && req.Generation == "" && req.PreviousGeneration == ""
}

// handleRuntimeDevPatchAck records the first instance acknowledgement of a
// patch generation so `gregale dev` can report when the edit reached the
// running environment (ADR-740 phase 3). Identity again comes from the
// listener; a guest can only acknowledge patches of its own deployment.
func (r *runtimeConfigReceiver) handleRuntimeDevPatchAck(instance string, req runtimeConfigRequest, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeDevPatchStore)
	if !r.devPatchEnabled || !ok || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchDisabled})
	}
	deploymentID, appID, _, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchDisabled})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	if err := store.RecordDevSourcePatchApplied(requestCtx, appID, deploymentID, req.PatchGeneration, req.PatchApplyMS, req.ErrorCode); err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: runtimeDevPatchUnavailable})
	}
	return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Accepted: true})
}
