package main

import (
	"bufio"
	"crypto/rand"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Restore reseed (GHSA-24j2-p895-mwc9, ADR-687).
//
// The resume hook reseeds the guest kernel, but a restored process also
// resumes with every userspace generator it held at capture: OpenSSL's DRBG
// (Node crypto, Python ssl, TLS), V8's Math.random, Python's random, and
// Node's UUID/randomInt caches. Every restore of one snapshot, and every
// sibling instance, would replay the same "random" values. guest-init
// therefore injects a preload into Node and Python processes. Each preload
// registers on RestoreReseedSocketPath, and on resume guest-init sends every
// registered process a fresh nonce and waits for it to confirm its reseed,
// before acknowledging the resume to vmmd. A process that fails to confirm
// fails the resume closed; vmmd then cold-boots (ADR-005).
const (
	RestoreReseedSocketPath = "/run/guest-init/reseed.sock"
	RestoreReseedAssetDir   = "/run/guest-init/rng"
	RestoreReseedSocketMode = 0o660
	// RestoreReseedTimeout bounds the whole barrier. Parked processes are
	// idle, so their event loops answer in well under a millisecond; the
	// margin covers lazily faulted pages right after a snapshot restore.
	RestoreReseedTimeout = 250 * time.Millisecond

	restoreReseedOptOutEnv = "GREGALE_RESTORE_RESEED"
	restoreReseedSocketEnv = "GREGALE_RESEED_SOCKET"
	restoreReseedHelloWait = 2 * time.Second
	restoreReseedMaxLine   = 256
	restoreReseedNonceSize = 32
)

//go:embed rngpreload/node-reseed.cjs rngpreload/reseed.node rngpreload/python/sitecustomize.py
var restoreReseedAssets embed.FS

// restoreReseedEnabled is set once the socket and assets are in place. Env
// stamping reads it: pointing NODE_OPTIONS at a missing --require file would
// stop every Node process from starting.
var restoreReseedEnabled atomic.Bool

// StampRestoreReseedEnv injects the Node and Python preloads. A workload may
// opt out with GREGALE_RESTORE_RESEED=off, which the docs flag as unsafe for
// any snapshot-restored app that generates secrets or nonces.
func StampRestoreReseedEnv(env []string) []string {
	if !restoreReseedEnabled.Load() {
		return env
	}
	return stampRestoreReseedEnv(env, RestoreReseedAssetDir, RestoreReseedSocketPath)
}

func stampRestoreReseedEnv(env []string, assetDir, socketPath string) []string {
	if strings.EqualFold(envValue(env, restoreReseedOptOutEnv), "off") {
		return env
	}
	require := "--require=" + filepath.Join(assetDir, "node-reseed.cjs")
	nodeOptions := envValue(env, "NODE_OPTIONS")
	if !strings.Contains(nodeOptions, require) {
		nodeOptions = strings.TrimSpace(nodeOptions + " " + require)
	}
	pythonDir := filepath.Join(assetDir, "python")
	pythonPath := envValue(env, "PYTHONPATH")
	if !containsPathEntry(pythonPath, pythonDir) {
		if pythonPath == "" {
			pythonPath = pythonDir
		} else {
			pythonPath = pythonDir + string(os.PathListSeparator) + pythonPath
		}
	}
	env = setEnvValue(env, "NODE_OPTIONS", nodeOptions)
	env = setEnvValue(env, "PYTHONPATH", pythonPath)
	return setEnvValue(env, restoreReseedSocketEnv, socketPath)
}

func envValue(env []string, key string) string {
	value := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			value = v // last assignment wins, as in execve
		}
	}
	return value
}

func setEnvValue(env []string, key, value string) []string {
	out := make([]string, 0, len(env)+1)
	for _, kv := range env {
		if k, _, ok := strings.Cut(kv, "="); ok && k == key {
			continue
		}
		out = append(out, kv)
	}
	return append(out, key+"="+value)
}

func containsPathEntry(list, entry string) bool {
	for _, p := range filepath.SplitList(list) {
		if filepath.Clean(p) == filepath.Clean(entry) {
			return true
		}
	}
	return false
}

// writeRestoreReseedAssets materializes the embedded preloads under dir.
func writeRestoreReseedAssets(dir string) error {
	files := map[string]string{
		"node-reseed.cjs":         "rngpreload/node-reseed.cjs",
		"reseed.node":             "rngpreload/reseed.node",
		"python/sitecustomize.py": "rngpreload/python/sitecustomize.py",
	}
	for dst, src := range files {
		body, err := restoreReseedAssets.ReadFile(src)
		if err != nil {
			return fmt.Errorf("read embedded %s: %w", src, err)
		}
		target := filepath.Join(dir, dst)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("mkdir %s: %w", filepath.Dir(target), err)
		}
		// Read-only for the workload: it must not be able to swap the
		// preload that every later Node or Python process loads.
		if err := os.WriteFile(target, body, 0o444); err != nil {
			return fmt.Errorf("write %s: %w", target, err)
		}
	}
	return nil
}

type restoreReseedClient struct {
	conn    net.Conn
	reader  *bufio.Reader
	runtime string
	pid     int
}

// restoreReseedBarrier tracks the registered processes and runs the
// reseed round on resume.
type restoreReseedBarrier struct {
	log     *slog.Logger
	mu      sync.Mutex
	clients map[*restoreReseedClient]struct{}
}

func newRestoreReseedBarrier(log *slog.Logger) *restoreReseedBarrier {
	if log == nil {
		log = slog.Default()
	}
	return &restoreReseedBarrier{log: log, clients: map[*restoreReseedClient]struct{}{}}
}

// serve registers every connection that says hello. It returns when ln closes.
func (b *restoreReseedBarrier) serve(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			b.log.Warn("restore reseed: accept", "err", err)
			continue
		}
		go b.register(conn)
	}
}

// register reads "hello <runtime> <pid>" and keeps the connection for the
// next resume. The connection lives in guest memory, so it survives the
// snapshot along with both endpoints.
func (b *restoreReseedBarrier) register(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(restoreReseedHelloWait))
	reader := bufio.NewReaderSize(conn, restoreReseedMaxLine)
	line, err := readRestoreReseedLine(reader)
	if err != nil {
		_ = conn.Close()
		return
	}
	fields := strings.Fields(line)
	if len(fields) != 3 || fields[0] != "hello" {
		b.log.Warn("restore reseed: bad hello", "line", line)
		_ = conn.Close()
		return
	}
	pid, err := strconv.Atoi(fields[2])
	if err != nil || pid <= 0 {
		_ = conn.Close()
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	client := &restoreReseedClient{conn: conn, reader: reader, runtime: fields[1], pid: pid}
	b.mu.Lock()
	b.clients[client] = struct{}{}
	b.mu.Unlock()
}

func (b *restoreReseedBarrier) registered() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.clients)
}

// Reseed sends every registered process a fresh nonce and waits for "ok".
// A process that has exited (EOF, reset, broken pipe) is dropped: it holds
// no state to reseed. Any live process that answers late, answers "err", or
// answers garbage fails the barrier, because its generators may still
// replay the snapshot's values.
func (b *restoreReseedBarrier) Reseed(timeout time.Duration) error {
	b.mu.Lock()
	clients := make([]*restoreReseedClient, 0, len(b.clients))
	for c := range b.clients {
		clients = append(clients, c)
	}
	b.mu.Unlock()
	if len(clients) == 0 {
		return nil
	}
	deadline := time.Now().Add(timeout)
	type outcome struct {
		client *restoreReseedClient
		gone   bool
		err    error
	}
	results := make(chan outcome, len(clients))
	for _, c := range clients {
		go func(c *restoreReseedClient) {
			gone, err := c.reseed(deadline)
			results <- outcome{client: c, gone: gone, err: err}
		}(c)
	}
	var failures []string
	for range clients {
		r := <-results
		if r.gone {
			b.mu.Lock()
			delete(b.clients, r.client)
			b.mu.Unlock()
			_ = r.client.conn.Close()
			continue
		}
		if r.err != nil {
			failures = append(failures, fmt.Sprintf("%s pid=%d: %v", r.client.runtime, r.client.pid, r.err))
		}
	}
	if len(failures) > 0 {
		return fmt.Errorf("restore reseed: %d of %d processes did not reseed: %s",
			len(failures), len(clients), strings.Join(failures, "; "))
	}
	return nil
}

func (c *restoreReseedClient) reseed(deadline time.Time) (gone bool, err error) {
	nonce := make([]byte, restoreReseedNonceSize)
	// crypto/rand reads getrandom(2): the resume hook has already reseeded
	// the kernel CRNG, so the nonce is fresh on every restore.
	if _, err := rand.Read(nonce); err != nil {
		return false, fmt.Errorf("nonce: %w", err)
	}
	_ = c.conn.SetDeadline(deadline)
	defer func() { _ = c.conn.SetDeadline(time.Time{}) }()
	if _, err := io.WriteString(c.conn, "reseed "+hex.EncodeToString(nonce)+"\n"); err != nil {
		return isRestoreReseedPeerGone(err), fmt.Errorf("send: %w", err)
	}
	line, err := readRestoreReseedLine(c.reader)
	if err != nil {
		return isRestoreReseedPeerGone(err), fmt.Errorf("read: %w", err)
	}
	if line != "ok" {
		return false, fmt.Errorf("replied %q", line)
	}
	return false, nil
}

func readRestoreReseedLine(r *bufio.Reader) (string, error) {
	line, err := r.ReadSlice('\n')
	if errors.Is(err, bufio.ErrBufferFull) {
		return "", errors.New("line too long")
	}
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(line)), nil
}

func isRestoreReseedPeerGone(err error) bool {
	return errors.Is(err, io.EOF) || errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, net.ErrClosed)
}
