package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/onebox-faas/faas/pkg/api"
)

// Stateless JSON responses cannot carry intermediate notifications. Select a
// request-scoped SSE response only for a watch with an explicit progress token;
// ordinary calls preserve their JSON wire response. Authentication and the
// existing MaxBytesReader run before this bounded dispatch.
func watchProgressTransport(normal, watch, progress http.Handler, timeouts ...time.Duration) http.Handler {
	timeout := api.DefaultClientTimeout
	if len(timeouts) > 0 {
		timeout = timeouts[0]
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			normal.ServeHTTP(w, r)
			return
		}
		data, err := io.ReadAll(io.LimitReader(r.Body, MaxFrameBytes+1))
		if err != nil || len(data) > MaxFrameBytes {
			var tooLarge *http.MaxBytesError
			status := http.StatusBadRequest
			if len(data) > MaxFrameBytes || errors.As(err, &tooLarge) {
				status = http.StatusRequestEntityTooLarge
			}
			http.Error(w, "Invalid MCP request body", status)
			return
		}
		r.Body = io.NopCloser(bytes.NewReader(data))
		var envelope struct {
			Method string `json:"method"`
			Params struct {
				Name string                     `json:"name"`
				Meta map[string]json.RawMessage `json:"_meta"`
			} `json:"params"`
		}
		valid := json.Unmarshal(data, &envelope) == nil
		if valid && envelope.Method == "tools/call" {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(managementToolTimeout(envelope.Params.Name, timeout) + 15*time.Second))
		}
		if valid && envelope.Method == "subscriptions/listen" {
			// Subscription lifetime and authorization lease bound this stream.
			_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
			progress.ServeHTTP(w, r)
			return
		}
		if json.Unmarshal(data, &envelope) == nil && envelope.Method == "tools/call" && envelope.Params.Name == "gregale_watch_rollout" {
			token := envelope.Params.Meta["progressToken"]
			if len(token) > 0 && !bytes.Equal(bytes.TrimSpace(token), []byte("null")) {
				progress.ServeHTTP(w, r)
				return
			}
			watch.ServeHTTP(w, r)
			return
		}
		normal.ServeHTTP(w, r)
	})
}

// The SDK's localhost protection refuses a request that reaches a loopback
// listener with a non-loopback Host. The hosted bridge listens on loopback
// behind gatewayd-public, which forwards the public Host, so that check
// refused every authenticated call. The remote handler already admits only
// its public Host and Origin, which is the DNS-rebinding defense.
func newWatchHTTPHandler(getServer func(*http.Request) *mcp.Server, timeouts ...time.Duration) http.Handler {
	normal := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	watch := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	progress := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true, DisableLocalhostProtection: true})
	return watchProgressTransport(normal, watch, progress, timeouts...)
}

func managementToolTimeout(name string, fallback time.Duration) time.Duration {
	switch name {
	case "gregale_apply_local_deploy", "gregale_plan_upload_deploy", "gregale_apply_upload_deploy":
		return localDeployTimeout
	case "gregale_apply_provision":
		return provisionTimeout
	}
	return fallback
}
