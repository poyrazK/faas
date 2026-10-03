//go:build linux

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
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

// A Scale app can receive up to 100 secrets of 32 KiB each; JSON can expand
// control characters sixfold, so cap responses at 24 MiB.
const runtimeConfigMaxFrame = 24 << 20
const runtimeConfigMaxRequestFrame = 32 << 10

const runtimeConfigCacheTTL = 5 * time.Second

const VsockRuntimeConfigHostPort uint32 = fcvm.VsockRuntimeConfigHostPort

type runtimeConfigRequest struct {
	Kind                    string `json:"kind,omitempty"`
	Scope                   string `json:"scope"`
	WorkloadName            string `json:"workload_name,omitempty"`
	Revision                string `json:"revision,omitempty"`
	Projection              string `json:"projection,omitempty"`
	Signal                  string `json:"signal,omitempty"`
	ErrorCode               string `json:"error_code,omitempty"`
	ApplicationAck          string `json:"application_ack,omitempty"`
	ApplicationAckErrorCode string `json:"application_ack_error_code,omitempty"`
	Generation              string `json:"generation,omitempty"`
	PreviousGeneration      string `json:"previous_generation,omitempty"`
}

type runtimeConfigResponse struct {
	Env        map[string]string  `json:"env,omitempty"`
	Secrets    *map[string]string `json:"secrets,omitempty"`
	Revision   string             `json:"revision,omitempty"`
	Unchanged  bool               `json:"unchanged,omitempty"`
	Accepted   bool               `json:"accepted,omitempty"`
	Generation string             `json:"generation,omitempty"`
	Error      string             `json:"error,omitempty"`
}

type runtimeConfigStore interface {
	ListAppEnv(context.Context, string, string) ([]state.AppEnv, error)
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
	cache *runtimeConfigCache
}

type runtimeConfigCacheEntry struct {
	response runtimeConfigResponse
	loadedAt time.Time
}

type runtimeConfigCache struct {
	mu      sync.Mutex
	entries map[string]runtimeConfigCacheEntry
}

func newRuntimeConfigCache() *runtimeConfigCache {
	return &runtimeConfigCache{entries: make(map[string]runtimeConfigCacheEntry)}
}

func (c *runtimeConfigCache) get(accountID, appID string, now time.Time) (runtimeConfigResponse, bool) {
	if c == nil {
		return runtimeConfigResponse{}, false
	}
	key := runtimeConfigCacheKey(accountID, appID)
	c.mu.Lock()
	entry, ok := c.entries[key]
	if ok && now.Sub(entry.loadedAt) >= runtimeConfigCacheTTL {
		delete(c.entries, key)
		ok = false
	}
	c.mu.Unlock()
	if !ok {
		return runtimeConfigResponse{}, false
	}
	return cloneRuntimeConfigResponse(entry.response), true
}

func (c *runtimeConfigCache) put(accountID, appID string, response runtimeConfigResponse, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.entries[runtimeConfigCacheKey(accountID, appID)] = runtimeConfigCacheEntry{
		response: cloneRuntimeConfigResponse(response),
		loadedAt: now,
	}
	c.mu.Unlock()
}

func (c *runtimeConfigCache) invalidate(accountID, appID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	if accountID == "" {
		for key := range c.entries {
			if strings.HasSuffix(key, "\x00"+appID) {
				delete(c.entries, key)
			}
		}
	} else {
		delete(c.entries, runtimeConfigCacheKey(accountID, appID))
	}
	c.mu.Unlock()
}

func runtimeConfigCacheKey(accountID, appID string) string {
	return accountID + "\x00" + appID
}

func cloneRuntimeConfigResponse(response runtimeConfigResponse) runtimeConfigResponse {
	clone := runtimeConfigResponse{
		Revision:  response.Revision,
		Unchanged: response.Unchanged,
		Accepted:  response.Accepted,
		Error:     response.Error,
	}
	if response.Env != nil {
		clone.Env = make(map[string]string, len(response.Env))
		for key, value := range response.Env {
			clone.Env[key] = value
		}
	}
	if response.Secrets != nil {
		secrets := make(map[string]string, len(*response.Secrets))
		for key, value := range *response.Secrets {
			secrets[key] = value
		}
		clone.Secrets = &secrets
	}
	return clone
}

// StartRuntimeConfigReceiver registers the instance-bound host side of the
// guest metadata endpoint. A missing store keeps the transport available but
// returns config_unavailable, which lets images roll out before the control
// plane has enabled live configuration reads.
func StartRuntimeConfigReceiver(ctx context.Context, log *slog.Logger, mgr *fcvm.Manager, store runtimeConfigStore, jailer *fcvm.JailerVMM) (*runtimeConfigReceiver, error) {
	if ctx == nil {
		return nil, errors.New("runtime config vsock: context is required")
	}
	if jailer == nil {
		return nil, errors.New("runtime config vsock: jailer is required")
	}
	if log == nil {
		log = slog.Default()
	}
	r := &runtimeConfigReceiver{ctx: ctx, log: log, mgr: mgr, store: store, cache: newRuntimeConfigCache()}
	if err := jailer.RegisterGuestVsockStreamHandler(VsockRuntimeConfigHostPort, r.handleGuestStream); err != nil {
		return nil, fmt.Errorf("runtime config receiver register port %d: %w", VsockRuntimeConfigHostPort, err)
	}
	log.Info("runtime config receiver registered", "vsock_host_port", VsockRuntimeConfigHostPort, "transport", "firecracker_uds", "enabled", store != nil)
	return r, nil
}

// StartRuntimeConfigInvalidationWatcher connects vmmd's cache to the
// control-plane mutation channels. Notification payloads carry identity only;
// the next guest request re-reads the authoritative rows from state.Store.
func StartRuntimeConfigInvalidationWatcher(ctx context.Context, pool *pgxpool.Pool, receiver *runtimeConfigReceiver, log *slog.Logger) {
	if ctx == nil || pool == nil || receiver == nil {
		return
	}
	if log == nil {
		log = slog.Default()
	}
	go func() {
		notifications, err := db.SubscribeWithReconnect(ctx, pool, []string{db.NotifyAppEnvChanged, db.NotifySecretRotated}, log)
		if err != nil {
			log.Warn("runtime config invalidation subscriber unavailable", "err", err)
			return
		}
		for notification := range notifications {
			payload, parseErr := db.ParseRuntimeConfigChangedPayload(notification.Payload)
			if parseErr != nil {
				log.Warn("runtime config invalidation payload rejected", "channel", notification.Channel, "err", parseErr)
				continue
			}
			receiver.cache.invalidate(payload.AccountID, payload.AppID)
		}
	}()
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
	if r.store == nil || r.mgr == nil {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "config_unavailable"})
	}
	appID, accountID, err := r.mgr.InstanceIdentity(instance)
	if err != nil || appID == "" || accountID == "" {
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "instance_not_found"})
	}
	requestCtx, cancel := context.WithTimeout(r.ctx, 4*time.Second)
	defer cancel()
	response, ok := r.cache.get(accountID, appID, time.Now())
	if !ok {
		response, err = loadRuntimeConfig(requestCtx, r.store, accountID, appID)
		if err == nil {
			r.cache.put(accountID, appID, response, time.Now())
		}
	}
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

func validRuntimeSecretRevision(revision string) bool {
	if revision == "" {
		return true
	}
	decoded, err := hex.DecodeString(revision)
	return err == nil && len(decoded) == sha256.Size
}

func runtimeSecretErrorKind(err error) string {
	if errors.Is(err, errRuntimeSecretSidecarsUnsupported) {
		return "sidecars_unsupported"
	}
	return "refresh_failed"
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

func loadRuntimeConfig(ctx context.Context, store runtimeConfigStore, accountID, appID string) (runtimeConfigResponse, error) {
	if ctx == nil || store == nil || accountID == "" || appID == "" {
		return runtimeConfigResponse{}, errors.New("runtime config dependencies are not configured")
	}
	rows, err := store.ListAppEnv(ctx, accountID, appID)
	if err != nil {
		return runtimeConfigResponse{}, err
	}
	response := runtimeConfigResponse{Env: make(map[string]string, len(rows))}
	var revision time.Time
	for _, row := range rows {
		if row.Scope != "" && row.Scope != "default" {
			continue
		}
		response.Env[row.Key] = row.Value
		if row.UpdatedAt.After(revision) {
			revision = row.UpdatedAt
		}
	}
	if !revision.IsZero() {
		response.Revision = revision.UTC().Format(time.RFC3339Nano)
	}
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
