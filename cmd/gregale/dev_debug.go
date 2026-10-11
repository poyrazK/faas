package main

// `gregale dev --debug` (ADR-741): start the Node.js inspector in the remote
// developer environment and expose it on a local loopback port. Each
// debugger connection becomes one authenticated WebSocket to the compute
// gateway, which forwards it to the inspector port of a running instance.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/devbridge"
)

// devDebugSupported reports whether the source is a Node.js workload, the
// only runtime with debugger support so far.
func devDebugSupported(sourceDir string, config devSourceConfig) bool {
	if config.shape == shapeFunction {
		return strings.HasPrefix(config.runtime, "node")
	}
	_, err := os.Stat(filepath.Join(sourceDir, "package.json"))
	return err == nil
}

type devDebugSecrets interface {
	SetSecretWithScope(ctx context.Context, slug, key, value, scope string) error
	ListSecretsWithScope(ctx context.Context, slug, scope string) (api.AppSecretListResponse, error)
	UnsetSecretWithScope(ctx context.Context, slug, key, scope string) error
}

// configureDevDebug turns the inspector on or off for the next deploy of the
// developer app. Turning it off removes a value left by an earlier --debug
// run, so a plain `gregale dev` never keeps an inspector listening.
func configureDevDebug(ctx context.Context, secrets devDebugSecrets, slug string, enabled bool) error {
	if enabled {
		return secrets.SetSecretWithScope(ctx, slug, api.DevDebugEnv, api.DevDebugRuntimeNode, "")
	}
	list, err := secrets.ListSecretsWithScope(ctx, slug, "")
	if err != nil {
		return err
	}
	for _, secret := range list.Secrets {
		if secret.Key == api.DevDebugEnv {
			return secrets.UnsetSecretWithScope(ctx, slug, api.DevDebugEnv, "")
		}
	}
	return nil
}

// devDebugTunnelURL is the WebSocket endpoint for slug's debugger.
func devDebugTunnelURL(base, slug string) (string, error) {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid API base %q", base)
	}
	u.Path = strings.TrimRight(u.Path, "/") + "/v1/apps/" + url.PathEscape(slug) + "/debug"
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	return u.String(), nil
}

type devDebugDialer func(ctx context.Context) (net.Conn, error)

func newDevDebugDialer(tunnelURL, token string) devDebugDialer {
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 30 * time.Second}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	return func(ctx context.Context) (net.Conn, error) {
		socket, response, err := dialer.DialContext(ctx, tunnelURL, headers)
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		if err != nil {
			if response != nil {
				return nil, fmt.Errorf("debugger tunnel refused (HTTP %d): %w", response.StatusCode, err)
			}
			return nil, err
		}
		return devbridge.NewWebSocketConn(socket), nil
	}
}

// serveDevDebugProxy accepts local debugger connections until ctx ends and
// joins each to its own tunnel connection. Failures are reported through
// onError and never stop the developer loop.
func serveDevDebugProxy(ctx context.Context, listener net.Listener, dial devDebugDialer, onError func(error)) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = local.Close() }()
			remote, err := dial(ctx)
			if err != nil {
				onError(err)
				return
			}
			defer func() { _ = remote.Close() }()
			joinDevDebugConns(local, remote)
		}()
	}
}

func joinDevDebugConns(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	pipe := func(dst, src net.Conn) {
		defer wg.Done()
		_, _ = io.Copy(dst, src)
		// Unblock the other direction once either side ends.
		_ = dst.Close()
		_ = src.Close()
	}
	go pipe(a, b)
	go pipe(b, a)
	wg.Wait()
}

// applyDevDebugSetting turns the inspector on or off before the first sync.
// Enabling must succeed; a failure to clean up an old setting only warns.
func applyDevDebugSetting(client *Client, slug string, enabled bool) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	err := configureDevDebug(ctx, client, slug, enabled)
	switch {
	case err != nil && enabled:
		return printErr("Could not enable the developer debugger", err)
	case err != nil:
		PrintWarn(osStderr, "could not turn off a previous --debug setting (%v); run `gregale secrets unset --app %s %s`", err, slug, api.DevDebugEnv)
	}
	return 0
}

// startDevDebugSession opens the local debugger port once the environment
// is live and tells the developer how to attach.
func startDevDebugSession(ctx context.Context, slug string, port int) {
	tunnelURL, err := devDebugTunnelURL(apiBase(), slug)
	if err != nil {
		_ = printErr("Could not start the debugger tunnel", err)
		return
	}
	dial := newDevDebugDialer(tunnelURL, loadToken())
	address, err := startDevDebugProxy(ctx, port, dial, func(err error) {
		PrintWarn(osStderr, "debugger connection failed: %v", err)
	})
	if err != nil {
		_ = printErr("Could not open the local debugger port", err)
		return
	}
	if jsonOutput {
		_ = writeJSON(map[string]string{"event": "developer_debugger", "address": address})
		return
	}
	PrintOK(osStdout, "Debugger listening on %s (VS Code: \"Attach to Node.js\"; Chrome: chrome://inspect).", address)
	PrintProgress(osStdout, "a request paused at a breakpoint still times out at the edge; resume within the request deadline")
}

// startDevDebugProxy listens on the local debugger port.
func startDevDebugProxy(ctx context.Context, port int, dial devDebugDialer, onError func(error)) (string, error) {
	listener, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", fmt.Sprint(port)))
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			return "", fmt.Errorf("port %d is in use; pass --debug-port", port)
		}
		return "", err
	}
	go serveDevDebugProxy(ctx, listener, dial, onError)
	return listener.Addr().String(), nil
}
