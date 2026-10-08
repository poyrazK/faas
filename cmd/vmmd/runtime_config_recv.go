package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sort"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/vmmdgrpc"
)

// A Scale app can receive up to 100 secrets of 32 KiB each; JSON can expand
// control characters sixfold, so cap responses at 24 MiB.
const runtimeConfigMaxFrame = 24 << 20
const runtimeConfigMaxRequestFrame = 32 << 10

const VsockRuntimeConfigHostPort uint32 = fcvm.VsockRuntimeConfigHostPort

type runtimeConfigRequest struct {
	Generation              string `json:"generation,omitempty"`
	PreviousGeneration      string `json:"previous_generation,omitempty"`
	Kind                    string `json:"kind,omitempty"`
	Scope                   string `json:"scope"`
	WorkloadName            string `json:"workload_name,omitempty"`
	Revision                string `json:"revision,omitempty"`
	Projection              string `json:"projection,omitempty"`
	Signal                  string `json:"signal,omitempty"`
	ErrorCode               string `json:"error_code,omitempty"`
	ApplicationAck          string `json:"application_ack,omitempty"`
	ApplicationAckErrorCode string `json:"application_ack_error_code,omitempty"`
	// PatchGeneration is the last developer live patch the guest applied
	// (ADR-740); only valid on dev_patch requests.
	PatchGeneration int64 `json:"patch_generation,omitempty"`
	// PatchApplyMS is how long the guest took to apply a patch; only valid
	// on dev_patch_ack requests.
	PatchApplyMS int64 `json:"patch_apply_ms,omitempty"`
}

type runtimeConfigResponse struct {
	Generation string             `json:"generation,omitempty"`
	Env        map[string]string  `json:"env,omitempty"`
	Secrets    *map[string]string `json:"secrets,omitempty"`
	Revision   string             `json:"revision,omitempty"`
	Unchanged  bool               `json:"unchanged,omitempty"`
	Accepted   bool               `json:"accepted,omitempty"`
	Error      string             `json:"error,omitempty"`
	DevPatch   *runtimeDevPatch   `json:"dev_patch,omitempty"`
}

type runtimeConfigStore interface {
	state.RuntimeAppEnvStore
}

type runtimeSecretsStore interface {
	state.RuntimeAppValuesStore
}

type runtimeSecretReloadStore interface {
	RecordAppSecretRuntimeReload(context.Context, state.AppSecretRuntimeReloadResult) (int, error)
}

type runtimeSecretReloadAckStore interface {
	RecordAppSecretRuntimeReloadAck(context.Context, state.AppSecretRuntimeReloadAckResult) (int, error)
}

type runtimeConfigReceiver struct {
	ctx   context.Context
	log   *slog.Logger
	mgr   *fcvm.Manager
	store runtimeConfigStore
	// ADR-740: serving a developer live patch marks the instance so vmmd
	// never snapshots it; delivery also needs the operator flag.
	diverged        *vmmdgrpc.DivergedInstances
	devPatchEnabled bool
}

func (*runtimeConfigReceiver) Close() {}

func (r *runtimeConfigReceiver) handleGuestStream(instance string, conn net.Conn) (string, error) {
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return "read", fmt.Errorf("runtime config set deadline: %w", err)
	}
	body, err := readRuntimeConfigRequestFrame(conn)
	if err != nil {
		_ = writeRuntimeConfigResponse(conn, runtimeConfigResponse{Error: "invalid_request"})
		return "read", fmt.Errorf("runtime config read: %w", err)
	}
	var req runtimeConfigRequest
	if err := json.Unmarshal(body, &req); err != nil || (req.Scope != "" && req.Scope != api.DefaultEnvScope) {
		_ = writeRuntimeConfigResponse(conn, runtimeConfigResponse{Error: "unsupported_scope"})
		return "protocol", errors.New("runtime config request has unsupported scope")
	}
	if req.Kind == runtimeDevPatchKind {
		if req.Scope != "" || req.PatchGeneration < 0 || req.PatchApplyMS != 0 || req.WorkloadName != "" || req.Revision != "" || req.Projection != "" || req.Signal != "" ||
			req.ErrorCode != "" || req.ApplicationAck != "" || req.ApplicationAckErrorCode != "" || req.Generation != "" || req.PreviousGeneration != "" {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeDevPatch(instance, req, conn)
	}
	if req.Kind == runtimeDevPatchAckKind {
		if !validRuntimeDevPatchAck(req) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeDevPatchAck(instance, req, conn)
	}
	if req.PatchGeneration != 0 || req.PatchApplyMS != 0 {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
	}
	if req.Kind == "secret_generation_start" || req.Kind == "secret_generation_retire" {
		if !validRuntimeSecretProcessRequest(req) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeSecretProcess(instance, req, conn)
	}
	if req.Kind == "secrets" {
		if req.Scope != "" {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "unsupported_scope"})
		}
		if !state.ValidSecretRuntimeWorkloadName(req.WorkloadName) || !validRuntimeSecretRevision(req.Revision) || req.Projection != "" || req.Signal != "" || req.ErrorCode != "" ||
			req.ApplicationAck != "" || req.ApplicationAckErrorCode != "" || req.Generation != "" || req.PreviousGeneration != "" {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeSecrets(instance, req.WorkloadName, req.Revision, conn)
	}
	if req.Kind == "secret_reload_status" {
		if req.Scope != "" || !state.ValidSecretRuntimeWorkloadName(req.WorkloadName) || !validRuntimeSecretRevision(req.Revision) || req.Revision == "" || !validRuntimeSecretReloadRequest(req) ||
			req.ApplicationAck != "" || req.ApplicationAckErrorCode != "" || req.Generation != "" || req.PreviousGeneration != "" {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeSecretReloadStatus(instance, req, conn)
	}
	if req.Kind == "secret_reload_ack" {
		if req.Scope != "" || !state.ValidSecretRuntimeWorkloadName(req.WorkloadName) || !state.ValidSecretApplicationReloadAck(req.Revision,
			state.SecretApplicationReloadAckStatus(req.ApplicationAck), req.ApplicationAckErrorCode) ||
			req.Projection != "" || req.Signal != "" || req.ErrorCode != "" || req.PreviousGeneration != "" || (req.Generation != "" && !state.ValidSecretProcessGeneration(req.Generation)) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeSecretReloadAck(instance, req, conn)
	}
	if (req.Kind != "" && req.Kind != "env") || req.WorkloadName != "" || req.Revision != "" || req.Projection != "" || req.Signal != "" || req.ErrorCode != "" ||
		req.ApplicationAck != "" || req.ApplicationAckErrorCode != "" || req.Generation != "" || req.PreviousGeneration != "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
	}
	return r.handleRuntimeEnv(instance, conn)
}

func (r *runtimeConfigReceiver) handleRuntimeEnv(instance string, conn net.Conn) (string, error) {
	if r.store == nil || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "config_unavailable"})
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" || accountID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "instance_not_found"})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	response, err := loadRuntimeConfig(requestCtx, r.store, deploymentID, appID, accountID)
	if err != nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "config_unavailable"})
	}
	return responseRuntimeConfig(r.log, conn, response)
}

func (r *runtimeConfigReceiver) handleRuntimeSecrets(instance, workloadName, knownRevision string, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeSecretsStore)
	if !ok || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" || accountID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	response, err := loadRuntimeSecretsForWorkloadIfChanged(requestCtx, store, r.mgr, deploymentID, appID, accountID, workloadName, knownRevision)
	if err != nil {
		r.log.Debug("runtime secrets refresh unavailable", "instance", instance, "err_kind", "refresh_failed")
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	return responseRuntimeConfig(r.log, conn, response)
}

func validRuntimeSecretReloadRequest(req runtimeConfigRequest) bool {
	return state.ValidSecretReloadOutcome(req.Revision,
		state.SecretReloadProjectionStatus(req.Projection),
		state.SecretReloadSignalStatus(req.Signal), req.ErrorCode)
}

func (r *runtimeConfigReceiver) handleRuntimeSecretReloadStatus(instance string, req runtimeConfigRequest, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeSecretsStore)
	reloadStore, reloadOK := r.store.(runtimeSecretReloadStore)
	if !ok || !reloadOK || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" || accountID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	selection, err := selectRuntimeSecretRowsForWorkload(requestCtx, store, deploymentID, appID, accountID, req.WorkloadName)
	if err != nil {
		r.log.Debug("runtime secret reload status unavailable", "instance", instance, "err_kind", "refresh_failed")
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	if selection.Revision != req.Revision {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secret_reload_stale"})
	}
	candidates := make([]state.AppSecretDeliveryCandidate, 0, len(selection.Rows))
	for _, row := range selection.Rows {
		candidates = append(candidates, state.AppSecretDeliveryCandidate{Scope: row.Scope, Key: row.Key, Version: row.DeliveryVersion})
	}
	_, err = reloadStore.RecordAppSecretRuntimeReload(requestCtx, state.AppSecretRuntimeReloadResult{
		Fence:     selection.Fence,
		AccountID: accountID, AppID: appID, InstanceID: instance, WorkloadName: req.WorkloadName, Revision: req.Revision,
		Projection: state.SecretReloadProjectionStatus(req.Projection),
		Signal:     state.SecretReloadSignalStatus(req.Signal), ErrorCode: req.ErrorCode,
		AttemptedAt: time.Now().UTC(), Candidates: candidates,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secret_reload_stale"})
		}
		r.log.Debug("runtime secret reload status write failed", "instance", instance, "err_kind", "state_write_failed")
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Accepted: true, Revision: req.Revision})
}

func (r *runtimeConfigReceiver) handleRuntimeSecretReloadAck(instance string, req runtimeConfigRequest, conn net.Conn) (string, error) {
	store, ok := r.store.(runtimeSecretsStore)
	ackStore, ackOK := r.store.(runtimeSecretReloadAckStore)
	if !ok || !ackOK || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	deploymentID, appID, accountID, err := r.mgr.InstanceRuntimeSecretIdentity(instance)
	if err != nil || deploymentID == "" || appID == "" || accountID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	selection, err := selectRuntimeSecretRowsForWorkload(requestCtx, store, deploymentID, appID, accountID, req.WorkloadName)
	if err != nil {
		r.log.Debug("runtime secret application ack unavailable", "instance", instance, "err_kind", "refresh_failed")
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	if selection.Revision != req.Revision {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secret_reload_stale"})
	}
	candidates := make([]state.AppSecretDeliveryCandidate, 0, len(selection.Rows))
	for _, row := range selection.Rows {
		candidates = append(candidates, state.AppSecretDeliveryCandidate{Scope: row.Scope, Key: row.Key, Version: row.DeliveryVersion})
	}
	_, err = ackStore.RecordAppSecretRuntimeReloadAck(requestCtx, state.AppSecretRuntimeReloadAckResult{
		Fence:     selection.Fence,
		AccountID: accountID, AppID: appID, InstanceID: instance, WorkloadName: req.WorkloadName, Revision: req.Revision, Generation: req.Generation,
		Status: state.SecretApplicationReloadAckStatus(req.ApplicationAck), ErrorCode: req.ApplicationAckErrorCode,
		AttemptedAt: time.Now().UTC(), Candidates: candidates,
	})
	if err != nil {
		if errors.Is(err, state.ErrConflict) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secret_reload_stale"})
		}
		r.log.Debug("runtime secret application ack write failed", "instance", instance, "err_kind", "state_write_failed")
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "secrets_unavailable"})
	}
	return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Accepted: true, Revision: req.Revision})
}

func loadRuntimeSecrets(ctx context.Context, store runtimeSecretsStore, mgr *fcvm.Manager, deploymentID, appID, accountID string) (runtimeConfigResponse, error) {
	return loadRuntimeSecretsIfChanged(ctx, store, mgr, deploymentID, appID, accountID, "")
}

func loadRuntimeSecretsIfChanged(ctx context.Context, store runtimeSecretsStore, mgr *fcvm.Manager, deploymentID, appID, accountID, knownRevision string) (runtimeConfigResponse, error) {
	return loadRuntimeSecretsForWorkloadIfChanged(ctx, store, mgr, deploymentID, appID, accountID, "", knownRevision)
}

func loadRuntimeSecretsForWorkloadIfChanged(ctx context.Context, store runtimeSecretsStore, mgr *fcvm.Manager, deploymentID, appID, accountID, workloadName, knownRevision string) (runtimeConfigResponse, error) {
	if ctx == nil || store == nil || mgr == nil || deploymentID == "" || appID == "" || accountID == "" {
		return runtimeConfigResponse{}, errors.New("runtime secrets dependencies are not configured")
	}
	selection, err := selectRuntimeSecretRowsForWorkload(ctx, store, deploymentID, appID, accountID, workloadName)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	if knownRevision != "" && knownRevision == selection.Revision {
		return runtimeConfigResponse{Revision: selection.Revision, Unchanged: true}, nil
	}
	secrets, err := mgr.UnsealRuntimeSecrets(selection.Entries)
	if err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("unseal runtime secrets: %w", err)
	}
	return runtimeConfigResponse{Secrets: &secrets, Revision: selection.Revision}, nil
}

type runtimeSecretSelection struct {
	Fence    state.RuntimeAppSecretFence
	Rows     []state.AppSecret
	Entries  []fcvm.SealedEnvEntry
	Revision string
}

func selectRuntimeSecretRowsForWorkload(ctx context.Context, store runtimeSecretsStore, deploymentID, appID, accountID, workloadName string) (runtimeSecretSelection, error) {
	if ctx == nil || store == nil || deploymentID == "" || appID == "" || accountID == "" {
		return runtimeSecretSelection{}, errors.New("runtime secret selection dependencies are not configured")
	}
	if !state.ValidSecretRuntimeWorkloadName(workloadName) {
		return runtimeSecretSelection{}, errors.New("invalid runtime secret workload name")
	}
	snapshot, err := store.RuntimeAppValuesForDeployment(ctx, accountID, appID, deploymentID)
	if err != nil {
		return runtimeSecretSelection{}, fmt.Errorf("load deployment runtime values: %w", err)
	}
	if snapshot.DeploymentID != deploymentID || snapshot.AppID != appID || snapshot.AccountID != accountID || api.ValidateScope(snapshot.Scope) != nil {
		return runtimeSecretSelection{}, errors.New("live instance and runtime value owner mismatch")
	}
	deployment := state.Deployment{ID: deploymentID, AppID: appID, Scope: snapshot.Scope,
		OverrideEnvSecrets: snapshot.SecretGrants.OverrideEnvSecrets, Sidecars: snapshot.SecretGrants.Sidecars,
		SecretReloadSignal: snapshot.SecretGrants.ReloadSignal}
	allowedKeys, err := runtimeSecretWorkloadAllowlist(deployment, workloadName)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	scope := snapshot.Scope
	rows := snapshot.Secrets
	requested := make(map[string]string)
	for _, row := range rows {
		if _, ok := allowedKeys[row.Key]; ok {
			requested[row.Key] = api.SecretRefPrefix + row.Key
		}
	}
	rows, err = state.SelectAppSecretsForDelivery(rows, requested, false)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	selected := make([]state.AppSecret, 0, len(rows))
	entries := make([]fcvm.SealedEnvEntry, 0, len(rows))
	foundKeys := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.AccountID != accountID || row.AppID != appID || row.Scope != scope {
			return runtimeSecretSelection{}, errors.New("secret store returned a mismatched identity")
		}
		if api.ValidateEnvKey(row.Key) != nil {
			return runtimeSecretSelection{}, errors.New("secret store returned an invalid key")
		}
		if _, duplicate := foundKeys[row.Key]; duplicate {
			return runtimeSecretSelection{}, errors.New("secret store returned a duplicate key")
		}
		if allowedKeys != nil {
			if _, ok := allowedKeys[row.Key]; !ok {
				continue
			}
		}
		selected = append(selected, row)
		entries = append(entries, fcvm.SealedEnvEntry{Key: row.Key, Ciphertext: row.Ciphertext})
		foundKeys[row.Key] = struct{}{}
	}
	// A missing grant must remain an error for restart-only workloads, since
	// they cannot receive a live projection update. For a workload that opted
	// into reload, an absent granted key is a revocation: return the current
	// projection so guest-init removes the key and signals that workload.
	if allowedKeys != nil {
		var missing []string
		for key := range allowedKeys {
			if _, ok := foundKeys[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			enabled := runtimeSecretReloadEnabled(snapshot.SecretGrants, workloadName)
			if !enabled {
				return runtimeSecretSelection{}, fmt.Errorf("deployment references missing secrets: %s", strings.Join(missing, ", "))
			}
		}
	}
	revision, err := runtimeSecretSelectedRevision(snapshot, workloadName, selected)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	fence, err := state.NewRuntimeAppSecretFence(snapshot)
	if err != nil {
		return runtimeSecretSelection{}, err
	}
	return runtimeSecretSelection{Rows: selected, Entries: entries, Revision: revision, Fence: fence}, nil
}

func runtimeSecretReloadEnabled(grants state.RuntimeAppSecretGrants, workloadName string) bool {
	if workloadName == "" {
		return grants.ReloadSignal != ""
	}
	return grants.SidecarReloadSignals[workloadName] != ""
}

func runtimeSecretWorkloadAllowlist(deployment state.Deployment, workloadName string) (map[string]struct{}, error) {
	if workloadName == "" {
		hasSidecars, err := runtimeDeploymentHasSidecars(deployment.Sidecars)
		if err != nil {
			return nil, fmt.Errorf("decode deployment sidecars: %w", err)
		}
		allowed, err := runtimeSecretAllowlist(deployment.OverrideEnvSecrets)
		if err != nil {
			return nil, fmt.Errorf("decode deployment secret allowlist: %w", err)
		}
		if hasSidecars && allowed == nil {
			// Sidecars require explicit grants for the main workload too.
			return map[string]struct{}{}, nil
		}
		return allowed, nil
	}
	var sidecars []api.Sidecar
	if err := json.Unmarshal(deployment.Sidecars, &sidecars); err != nil {
		return nil, fmt.Errorf("decode deployment sidecars: %w", err)
	}
	for _, sidecar := range sidecars {
		if sidecar.Name != workloadName {
			continue
		}
		if sidecar.Type != api.SidecarTypeSidecar {
			return nil, fmt.Errorf("workload %q is not a long-running sidecar", workloadName)
		}
		if len(sidecar.EnvSecrets) == 0 {
			return nil, fmt.Errorf("workload %q has no explicit secret grants", workloadName)
		}
		allowed := make(map[string]struct{}, len(sidecar.EnvSecrets))
		for envKey, ref := range sidecar.EnvSecrets {
			if api.ValidateEnvKey(envKey) != nil || ref != api.SecretRefPrefix+envKey {
				return nil, fmt.Errorf("workload %q has an invalid secret grant", workloadName)
			}
			allowed[envKey] = struct{}{}
		}
		return allowed, nil
	}
	return nil, fmt.Errorf("workload %q is not declared by the deployment", workloadName)
}

func validRuntimeSecretRevision(revision string) bool {
	if revision == "" {
		return true
	}
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size
}

func runtimeDeploymentHasSidecars(raw json.RawMessage) (bool, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" || trimmed == "null" {
		return false, nil
	}
	var sidecars []json.RawMessage
	if err := json.Unmarshal(raw, &sidecars); err != nil {
		return false, err
	}
	return len(sidecars) > 0, nil
}

// Sidecar-free legacy deployments receive all eligible app secrets in their
// selected scope. With sidecars, a missing main allowlist becomes an empty grant
// set. Explicit allowlists match the boot delivery path.
func runtimeSecretAllowlist(raw json.RawMessage) (map[string]struct{}, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var refs map[string]string
	if err := json.Unmarshal(raw, &refs); err != nil {
		return nil, err
	}
	if len(refs) == 0 {
		return nil, nil
	}
	allowed := make(map[string]struct{}, len(refs))
	for envKey, ref := range refs {
		if api.ValidateEnvKey(envKey) != nil || !strings.HasPrefix(ref, api.SecretRefPrefix) {
			return nil, errors.New("invalid env_secrets entry")
		}
		name := strings.TrimPrefix(ref, api.SecretRefPrefix)
		if !api.SecretRefNameRe.MatchString(name) {
			return nil, errors.New("invalid env_secrets reference")
		}
		allowed[envKey] = struct{}{}
	}
	return allowed, nil
}

func runtimeSecretSelectedRevision(snapshot state.RuntimeAppValuesSnapshot, workloadName string, rows []state.AppSecret) (string, error) {
	// Guest revisions include deployment/environment and selected row lifetime.
	// An old acknowledgement cannot acquire a fresh host fence merely because
	// a replacement row copied back the same envelope and version counter.
	snapshot.Secrets = rows
	fence, err := state.NewRuntimeAppSecretFence(snapshot)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(fence.Fingerprint + "\x00" + workloadName))
	return hex.EncodeToString(digest[:]), nil
}

func loadRuntimeConfig(ctx context.Context, store runtimeConfigStore, deploymentID, appID, accountID string) (runtimeConfigResponse, error) {
	if ctx == nil || store == nil || deploymentID == "" || accountID == "" || appID == "" {
		return runtimeConfigResponse{}, errors.New("runtime config dependencies are not configured")
	}
	snapshot, err := store.RuntimeAppEnvForDeployment(ctx, accountID, appID, deploymentID)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	if snapshot.AccountID != accountID || snapshot.AppID != appID || snapshot.DeploymentID != deploymentID || api.ValidateScope(snapshot.Scope) != nil {
		return runtimeConfigResponse{}, errors.New("runtime config store returned a mismatched deployment identity")
	}
	response := runtimeConfigResponse{Env: make(map[string]string, len(snapshot.Values))}
	for _, row := range snapshot.Values {
		if row.AccountID != accountID || row.AppID != appID || row.Scope != snapshot.Scope || api.ValidateEnvKey(row.Key) != nil {
			return runtimeConfigResponse{}, errors.New("runtime config store returned a mismatched value identity")
		}
		if _, duplicate := response.Env[row.Key]; duplicate {
			return runtimeConfigResponse{}, errors.New("runtime config store returned a duplicate key")
		}
		response.Env[row.Key] = row.Value
	}
	// A content revision also detects deletion of a key that did not have the
	// maximum timestamp. Deployment/scope/lifetime remain part of its identity.
	encoded, err := json.Marshal(struct {
		DeploymentID, Scope, EnvironmentID string
		Env                                map[string]string
	}{deploymentID, snapshot.Scope, snapshot.EnvironmentID, response.Env})
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	hash := sha256.Sum256(encoded)
	response.Revision = hex.EncodeToString(hash[:])
	return response, nil
}

func responseRuntimeConfig(log *slog.Logger, conn net.Conn, response runtimeConfigResponse) (string, error) {
	if err := writeRuntimeConfigResponse(conn, response); err != nil {
		if log != nil {
			log.Debug("runtime config response failed", "err", err)
		}
		return "write", err
	}
	return "", nil
}

func writeRuntimeConfigResponse(w io.Writer, response runtimeConfigResponse) error {
	body, err := json.Marshal(response)
	if err != nil {
		return err
	}
	return writeRuntimeConfigFrame(w, body)
}

func writeRuntimeConfigFrame(w io.Writer, body []byte) error {
	if len(body) == 0 || len(body) > runtimeConfigMaxFrame {
		return fmt.Errorf("invalid frame length %d", len(body))
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	for len(frame) > 0 {
		n, err := w.Write(frame)
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
		frame = frame[n:]
	}
	return nil
}

func readRuntimeConfigRequestFrame(r io.Reader) ([]byte, error) {
	return readRuntimeConfigFrameLimit(r, runtimeConfigMaxRequestFrame)
}

func readRuntimeConfigFrameLimit(r io.Reader, maxFrame uint32) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > maxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
