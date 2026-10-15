package mcpmanagement

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Stateless JSON responses cannot carry intermediate notifications. Select a
// request-scoped SSE response only for a watch with an explicit progress token;
// ordinary calls preserve their JSON wire response. Authentication and the
// existing MaxBytesReader run before this bounded dispatch.
func watchProgressTransport(normal, watch, progress http.Handler) http.Handler {
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
		if json.Unmarshal(data, &envelope) == nil && envelope.Method == "subscriptions/listen" {
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

func newWatchHTTPHandler(getServer func(*http.Request) *mcp.Server) http.Handler {
	normal := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	watch := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true, PropagateRequestCancellation: true})
	progress := mcp.NewStreamableHTTPHandler(getServer, &mcp.StreamableHTTPOptions{Stateless: true, PropagateRequestCancellation: true})
	return watchProgressTransport(normal, watch, progress)
}
