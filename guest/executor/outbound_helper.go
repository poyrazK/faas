package executor

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/onebox-faas/faas/pkg/executionproto"
)

const outboundHelperPrefix = "/i/"

type executionOutboundHelper struct {
	server   *http.Server
	listener net.Listener
	endpoint string
}

func startExecutionOutboundHelper(ctx context.Context, broker executionproto.OutboundBroker) (*executionOutboundHelper, error) {
	if broker == nil {
		return nil, errors.New("execution outbound broker is not configured")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, errors.New("execution outbound helper could not start")
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serveExecutionOutboundCall(w, r, broker)
	})
	server := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 2 * time.Second,
		MaxHeaderBytes:    executionproto.MaxOutboundHeaderBytes,
		IdleTimeout:       time.Second,
		ErrorLog:          logDiscarder,
		BaseContext: func(net.Listener) context.Context {
			return ctx
		},
	}
	helper := &executionOutboundHelper{
		server: server, listener: listener,
		endpoint: "http://" + listener.Addr().String(),
	}
	go func() { _ = server.Serve(listener) }()
	return helper, nil
}

func (h *executionOutboundHelper) Close() {
	if h == nil || h.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	if err := h.server.Shutdown(ctx); err != nil && h.listener != nil {
		_ = h.listener.Close()
	}
}

func serveExecutionOutboundCall(w http.ResponseWriter, r *http.Request, broker executionproto.OutboundBroker) {
	if r.Method == http.MethodConnect {
		writeOutboundHelperError(w, http.StatusMethodNotAllowed, "outbound method is not supported")
		return
	}
	integrationID, providerPath, ok := parseExecutionOutboundPath(r.URL.EscapedPath())
	if !ok {
		writeOutboundHelperError(w, http.StatusBadRequest, "outbound request is invalid")
		return
	}
	if r.URL.RawQuery != "" {
		providerPath += "?" + r.URL.RawQuery
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, executionproto.MaxOutboundBodyBytes))
	if err != nil {
		writeOutboundHelperError(w, http.StatusRequestEntityTooLarge, "outbound request body exceeds the Runs limit")
		return
	}
	request := executionproto.OutboundRequest{ID: 1, IntegrationID: integrationID, Method: r.Method, Path: providerPath, Body: body}
	if err := request.Validate(); err != nil {
		writeOutboundHelperError(w, http.StatusBadRequest, "outbound request is invalid")
		return
	}
	response, err := broker.Call(r.Context(), request)
	if err != nil {
		writeOutboundHelperError(w, http.StatusBadGateway, "outbound integration request failed")
		return
	}
	for name, value := range response.Headers {
		w.Header().Set(name, value)
	}
	w.WriteHeader(response.Status)
	_, _ = w.Write(response.Body)
}

func parseExecutionOutboundPath(path string) (string, string, bool) {
	if !strings.HasPrefix(path, outboundHelperPrefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, outboundHelperPrefix)
	integrationPart, suffix, found := strings.Cut(rest, "/")
	if integrationPart == "" {
		return "", "", false
	}
	integrationID, err := url.PathUnescape(integrationPart)
	if err != nil {
		return "", "", false
	}
	providerPath := "/"
	if found {
		providerPath += suffix
	}
	return integrationID, providerPath, true
}

func writeOutboundHelperError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

var logDiscarder = log.New(io.Discard, "", 0)
