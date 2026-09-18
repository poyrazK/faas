//go:build linux

package main

import (
	"context"
	"encoding/binary"
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

	"github.com/onebox-faas/faas/pkg/db"
	"github.com/onebox-faas/faas/pkg/fcvm"
	"github.com/onebox-faas/faas/pkg/state"
)

const runtimeConfigMaxFrame = 32 << 10

const runtimeConfigCacheTTL = 5 * time.Second

const VsockRuntimeConfigHostPort uint32 = fcvm.VsockRuntimeConfigHostPort

type runtimeConfigRequest struct {
	Scope string `json:"scope"`
}

type runtimeConfigResponse struct {
	Env      map[string]string `json:"env,omitempty"`
	Revision string            `json:"revision,omitempty"`
	Error    string            `json:"error,omitempty"`
}

type runtimeConfigStore interface {
	ListAppEnv(context.Context, string, string) ([]state.AppEnv, error)
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
		Revision: response.Revision,
		Error:    response.Error,
		Env:      make(map[string]string, len(response.Env)),
	}
	for key, value := range response.Env {
		clone.Env[key] = value
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
	body, err := readRuntimeConfigFrame(conn)
	if err != nil {
		_ = writeRuntimeConfigResponse(conn, runtimeConfigResponse{Error: "invalid_request"})
		return "read", fmt.Errorf("runtime config read: %w", err)
	}
	var req runtimeConfigRequest
	if err := json.Unmarshal(body, &req); err != nil || (req.Scope != "" && req.Scope != "default") {
		_ = writeRuntimeConfigResponse(conn, runtimeConfigResponse{Error: "unsupported_scope"})
		return "protocol", errors.New("runtime config request has unsupported scope")
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

func readRuntimeConfigFrame(r io.Reader) ([]byte, error) {
	var header [4]byte
	if _, err := io.ReadFull(r, header[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(header[:])
	if n == 0 || n > runtimeConfigMaxFrame {
		return nil, fmt.Errorf("invalid frame length %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return body, nil
}
