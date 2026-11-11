package main

// `gregale dev --debug` (ADR-741): start the Node.js inspector in the remote
// developer environment and expose it on a local loopback port. Each
// debugger connection becomes one authenticated WebSocket to the compute
// gateway, which forwards it to the inspector port of a running instance.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
// only runtime with debugger support so far. Bun and Deno projects also ship a
// package.json but ignore NODE_OPTIONS, so the inspector would never start.
func devDebugSupported(sourceDir string, config devSourceConfig) bool {
	if config.shape == shapeFunction {
		return strings.HasPrefix(config.runtime, "node")
	}
	if _, err := os.Stat(filepath.Join(sourceDir, "package.json")); err != nil {
		return false
	}
	for _, marker := range []string{"bun.lockb", "bun.lock", "bunfig.toml", "deno.json", "deno.jsonc", "deno.lock"} {
		if _, err := os.Stat(filepath.Join(sourceDir, marker)); err == nil {
			return false
		}
	}
	return true
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

// devDebugHandshakeTimeout covers the server's wake of a parked environment
// (api.DevDebugWakeTimeout) before it answers the upgrade, plus the hops.
const devDebugHandshakeTimeout = api.DevDebugWakeTimeout + 30*time.Second

func newDevDebugDialer(tunnelURL, token string) devDebugDialer {
	dialer := websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: devDebugHandshakeTimeout}
	headers := http.Header{"Authorization": {"Bearer " + token}}
	return func(ctx context.Context) (net.Conn, error) {
		socket, response, err := dialer.DialContext(ctx, tunnelURL, headers)
		if err != nil {
			if response != nil {
				return nil, devDebugRefusal(response, err)
			}
			return nil, err
		}
		if response != nil && response.Body != nil {
			_ = response.Body.Close()
		}
		return devbridge.NewWebSocketConn(socket), nil
	}
}

// devDebugRefusal explains a refused tunnel. An agent key's tunnel waits
// for a person's approval (ADR-20261111150000): the error names the review
// link, and the next debugger connection after they approve goes through.
func devDebugRefusal(response *http.Response, err error) error {
	var problem api.Problem
	if response.Body != nil {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 64<<10))
		_ = response.Body.Close()
		_ = json.Unmarshal(body, &problem)
	}
	if response.StatusCode == http.StatusPreconditionRequired {
		if problem.AgentApproval != nil && problem.AgentApproval.ReviewURL != "" {
			return fmt.Errorf("debugger access needs a person's approval: ask them to open %s; once approved, debugger connections work for %s", problem.AgentApproval.ReviewURL, api.DevDebugApprovalWindow)
		}
		return errors.New("debugger access needs a person's approval: ask them to review this agent's pending approvals")
	}
	if problem.Detail != "" {
		return fmt.Errorf("debugger tunnel refused (HTTP %d): %s", response.StatusCode, problem.Detail)
	}
	return fmt.Errorf("debugger tunnel refused (HTTP %d): %w", response.StatusCode, err)
}

// serveDevDebugProxy accepts local debugger connections until ctx ends and
// joins each to its own tunnel connection. Failures are reported through
// onError and never stop the developer loop.
//
// Target discovery (GET /json, /json/list, /json/version) is answered from a
// short cache: chrome://inspect and editors poll it every second or so, and
// each poll would otherwise be a full authenticated tunnel that also keeps
// the environment from parking.
func serveDevDebugProxy(ctx context.Context, listener net.Listener, dial devDebugDialer, onError func(error)) {
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	discovery := &devDebugDiscoveryCache{ttl: devDebugDiscoveryTTL}
	for {
		local, err := listener.Accept()
		if err != nil {
			return
		}
		go func() {
			defer func() { _ = local.Close() }()
			reader := bufio.NewReaderSize(local, 8<<10)
			if path, ok := devDebugDiscoveryRequest(reader); ok {
				if err := discovery.serve(ctx, local, reader, path, dial); err != nil {
					onError(err)
				}
				return
			}
			remote, err := dial(ctx)
			if err != nil {
				onError(err)
				return
			}
			defer func() { _ = remote.Close() }()
			joinDevDebugConns(&devDebugBufferedConn{Conn: local, reader: reader}, remote)
		}()
	}
}

// devDebugDiscoveryTTL bounds how stale a cached target list may be. A
// restarted process gets a new target ID, which a debugger then sees within
// this interval.
const devDebugDiscoveryTTL = 2 * time.Second

// devDebugDiscoveryRequest peeks at a new connection and reports the path of
// a plain discovery GET. Anything else, notably the WebSocket upgrade that
// attaches the debugger, is left untouched for the tunnel.
func devDebugDiscoveryRequest(reader *bufio.Reader) (string, bool) {
	head, err := reader.Peek(5)
	if err != nil || string(head) != "GET /" {
		return "", false
	}
	// Peek the whole request head, up to its blank line. Each Peek asks for
	// one byte more than is buffered, so it only waits while the head is
	// still arriving and never for bytes a waiting client will not send.
	for {
		block, _ := reader.Peek(reader.Buffered())
		if end := strings.Index(string(block), "\r\n\r\n"); end >= 0 {
			line, headers, _ := strings.Cut(string(block[:end]), "\r\n")
			fields := strings.Fields(line)
			if len(fields) != 3 || strings.Contains(strings.ToLower(headers), "upgrade:") {
				return "", false
			}
			switch path := strings.TrimSuffix(fields[1], "/"); path {
			case "/json", "/json/list", "/json/version":
				return path, true
			}
			return "", false
		}
		if reader.Buffered() >= reader.Size() {
			return "", false
		}
		if _, err := reader.Peek(reader.Buffered() + 1); err != nil {
			return "", false
		}
	}
}

type devDebugDiscoveryCache struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]devDebugDiscoveryEntry
}

type devDebugDiscoveryEntry struct {
	response []byte
	at       time.Time
}

// serve answers one discovery request from the cache, or forwards it through
// a tunnel and caches the complete response.
func (c *devDebugDiscoveryCache) serve(ctx context.Context, local net.Conn, reader *bufio.Reader, path string, dial devDebugDialer) error {
	request, err := http.ReadRequest(reader)
	if err != nil {
		return nil
	}
	_ = request.Body.Close()
	// The inspector builds webSocketDebuggerUrl from the request's Host, so
	// clients that address the port differently get their own entry.
	key := path + "|" + request.Host
	c.mu.Lock()
	entry, ok := c.entries[key]
	c.mu.Unlock()
	if ok && time.Since(entry.at) < c.ttl {
		_, err := local.Write(entry.response)
		return devDebugIgnoreClosed(err)
	}
	remote, err := dial(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = remote.Close() }()
	request.Close = true
	if err := request.Write(remote); err != nil {
		return err
	}
	response, err := http.ReadResponse(bufio.NewReader(remote), request)
	if err != nil {
		return err
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	_ = response.Body.Close()
	if err != nil {
		return err
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	response.ContentLength = int64(len(body))
	response.TransferEncoding = nil
	response.Close = true
	var encoded bytes.Buffer
	if err := response.Write(&encoded); err != nil {
		return err
	}
	if response.StatusCode == http.StatusOK {
		c.mu.Lock()
		if c.entries == nil {
			c.entries = map[string]devDebugDiscoveryEntry{}
		}
		c.entries[key] = devDebugDiscoveryEntry{response: encoded.Bytes(), at: time.Now()}
		c.mu.Unlock()
	}
	_, err = local.Write(encoded.Bytes())
	return devDebugIgnoreClosed(err)
}

func devDebugIgnoreClosed(err error) error {
	if err == nil || errors.Is(err, net.ErrClosed) || errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return nil
	}
	return err
}

// devDebugBufferedConn reads first what the discovery peek buffered.
type devDebugBufferedConn struct {
	net.Conn
	reader *bufio.Reader
}

func (c *devDebugBufferedConn) Read(p []byte) (int, error) { return c.reader.Read(p) }

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
		PrintWarn(osStderr, "could not turn off a previous --debug setting (%v); run `gregale env unset --app %s %s`", err, slug, api.DevDebugEnv)
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
			return "", fmt.Errorf("port %d is in use; pass --debug-port, or set dev.debug_port in gregale.yaml (each app of `gregale dev --all` needs its own)", port)
		}
		return "", err
	}
	go serveDevDebugProxy(ctx, listener, dial, onError)
	return listener.Addr().String(), nil
}
