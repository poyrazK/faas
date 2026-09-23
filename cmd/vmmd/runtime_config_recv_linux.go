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
	"sort"
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
	Kind     string `json:"kind,omitempty"`
	Scope    string `json:"scope"`
	Revision string `json:"revision,omitempty"`
}

type runtimeConfigResponse struct {
	Env       map[string]string  `json:"env,omitempty"`
	Secrets   *map[string]string `json:"secrets,omitempty"`
	Revision  string             `json:"revision,omitempty"`
	Unchanged bool               `json:"unchanged,omitempty"`
	Error     string             `json:"error,omitempty"`
}

type runtimeConfigStore interface {
	ListAppEnv(context.Context, string, string) ([]state.AppEnv, error)
}

type runtimeSecretsStore interface {
	DeploymentByID(context.Context, string) (state.Deployment, error)
	ListAppSecretsInScope(context.Context, string, string, string) ([]state.AppSecret, error)
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
	if req.Kind == "secrets" {
		if req.Scope != "" {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "unsupported_scope"})
		}
		if !validRuntimeSecretRevision(req.Revision) {
			return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: "invalid_request"})
		}
		return r.handleRuntimeSecrets(instance, req.Revision, conn)
	}
	if (req.Kind != "" && req.Kind != "env") || req.Revision != "" {
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

func (r *runtimeConfigReceiver) handleRuntimeSecrets(instance, knownRevision string, conn net.Conn) (string, error) {
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
	response, err := loadRuntimeSecretsIfChanged(requestCtx, store, r.mgr, deploymentID, appID, accountID, knownRevision)
	if err != nil {
		r.log.Debug("runtime secrets refresh unavailable", "instance", instance, "err_kind", runtimeSecretErrorKind(err))
		code := "secrets_unavailable"
		if errors.Is(err, errRuntimeSecretSidecarsUnsupported) {
			code = "secret_reload_unsupported"
		}
		return responseRuntimeConfig(r.log, conn, runtimeConfigResponse{Error: code})
	}
	return responseRuntimeConfig(r.log, conn, response)
}

var errRuntimeSecretSidecarsUnsupported = errors.New("runtime secret reload is unsupported for sidecar deployments")

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
	if ctx == nil || store == nil || mgr == nil || deploymentID == "" || appID == "" || accountID == "" {
		return runtimeConfigResponse{}, errors.New("runtime secrets dependencies are not configured")
	}
	deployment, err := store.DeploymentByID(ctx, deploymentID)
	if err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("load deployment: %w", err)
	}
	if deployment.ID != deploymentID || deployment.AppID != appID {
		return runtimeConfigResponse{}, errors.New("live instance and deployment identity mismatch")
	}
	if hasSidecars, err := runtimeDeploymentHasSidecars(deployment.Sidecars); err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("decode deployment sidecars: %w", err)
	} else if hasSidecars {
		return runtimeConfigResponse{}, errRuntimeSecretSidecarsUnsupported
	}
	scope := deployment.Scope
	if scope == "" {
		scope = api.DefaultEnvScope
	}
	if api.ValidateScope(scope) != nil {
		return runtimeConfigResponse{}, errors.New("deployment has invalid secret scope")
	}
	rows, err := store.ListAppSecretsInScope(ctx, accountID, appID, scope)
	if err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("list app secrets: %w", err)
	}
	allowedKeys, err := runtimeSecretAllowlist(deployment.OverrideEnvSecrets)
	if err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("decode deployment secret allowlist: %w", err)
	}
	selected := make([]state.AppSecret, 0, len(rows))
	entries := make([]fcvm.SealedEnvEntry, 0, len(rows))
	foundKeys := make(map[string]struct{}, len(rows))
	for _, row := range rows {
		if row.AccountID != accountID || row.AppID != appID || row.Scope != scope {
			return runtimeConfigResponse{}, errors.New("secret store returned a mismatched identity")
		}
		if api.ValidateEnvKey(row.Key) != nil {
			return runtimeConfigResponse{}, errors.New("secret store returned an invalid key")
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
	if allowedKeys != nil {
		var missing []string
		for key := range allowedKeys {
			if _, ok := foundKeys[key]; !ok {
				missing = append(missing, key)
			}
		}
		if len(missing) > 0 {
			sort.Strings(missing)
			return runtimeConfigResponse{}, fmt.Errorf("deployment references missing secrets: %s", strings.Join(missing, ", "))
		}
	}
	revision := runtimeSecretRevision(scope, selected)
	if knownRevision != "" && knownRevision == revision {
		return runtimeConfigResponse{Revision: revision, Unchanged: true}, nil
	}
	secrets, err := mgr.UnsealRuntimeSecrets(entries)
	if err != nil {
		return runtimeConfigResponse{}, fmt.Errorf("unseal runtime secrets: %w", err)
	}
	return runtimeConfigResponse{Secrets: &secrets, Revision: revision}, nil
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

// A nil allowlist preserves the existing deployment contract: legacy deploys
// receive all app secrets in their selected scope. A non-empty allowlist is
// enforced as a positive grant, exactly as the boot delivery path does.
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

func runtimeSecretRevision(scope string, rows []state.AppSecret) string {
	rows = append([]state.AppSecret(nil), rows...)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Key < rows[j].Key })
	h := sha256.New()
	_, _ = io.WriteString(h, scope+"\x00")
	for _, row := range rows {
		_, _ = fmt.Fprintf(h, "%s\x00%d\x00", row.Key, row.DeliveryVersion)
	}
	return hex.EncodeToString(h.Sum(nil))
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
