//go:build linux

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protowire"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

// Node stack snapshots (ADR-1155). The Node collector runs on the main
// thread, so it cannot report the stack of a main thread stuck in JavaScript.
// For a stacks capture guest-init reaches such a process through its
// inspector instead: it opens it with SIGUSR1 when the collector reported that
// safe, checks whether the event loop still runs, and only for a stuck loop
// pauses the thread, reads its call frames and resumes it at once. An idle
// loop is left to the collector, which reports what the loop waits on.

// nodeInspector is how a Node collector said its inspector can be reached:
// already open on port, or openable with SIGUSR1 on port.
type nodeInspector struct {
	open bool
	port int
	seen time.Time
}

func parseNodeInspector(s string) (nodeInspector, bool) {
	mode, rawPort, ok := strings.Cut(s, ":")
	port, err := strconv.Atoi(rawPort)
	if !ok || err != nil || port < 1 || port > 65535 || (mode != "open" && mode != "sigusr1") {
		return nodeInspector{}, false
	}
	return nodeInspector{open: mode == "open", port: port}, true
}

// nodeStackSampler captures one Node process's main-thread stack for epoch;
// nil with a nil error means its event loop was idle.
type nodeStackSampler func(ctx context.Context, pid int, inspector nodeInspector, epoch string) ([]byte, error)

// rememberNodeInspector records the inspector a polling collector reported.
// Entries outlive the collector's registration so a thread that stopped
// polling because it is stuck can still be reached.
func (b *profileBridge) rememberNodeInspector(pid, reported string) {
	for key, ni := range b.inspectors {
		if time.Since(ni.seen) > api.ProfileNodeInspectorRememberFor {
			delete(b.inspectors, key)
		}
	}
	ni, ok := parseNodeInspector(reported)
	if !ok {
		delete(b.inspectors, pid)
		return
	}
	if _, known := b.inspectors[pid]; known || len(b.inspectors) < api.ProfileMaxProcesses {
		ni.seen = time.Now()
		b.inspectors[pid] = ni
	}
}

// sampleNodeStacks snapshots every remembered Node process for a stacks
// capture, one at a time (processes share the default inspector port), within
// budget.
func (b *profileBridge) sampleNodeStacks(c *profileCapture, budget time.Duration) {
	b.mu.Lock()
	targets := make(map[string]nodeInspector, len(b.inspectors))
	for pid, ni := range b.inspectors {
		targets[pid] = ni
	}
	sample := b.nodeStack
	b.mu.Unlock()
	if len(targets) == 0 {
		return
	}
	if sample == nil {
		sample = captureNodeStack
	}
	pids := make([]string, 0, len(targets))
	for pid := range targets {
		pids = append(pids, pid)
	}
	sort.Strings(pids)
	ctx, cancel := context.WithTimeout(context.Background(), budget)
	defer cancel()
	for _, pid := range pids {
		n, err := strconv.Atoi(pid)
		if err != nil || ctx.Err() != nil {
			continue
		}
		profile, err := sample(ctx, n, targets[pid], c.epoch)
		if err != nil || len(profile) == 0 {
			continue
		}
		b.acceptNodeStack(ctx, c, pid, profile)
	}
}

// acceptNodeStack adds a sampled stack to the capture, counting its process:
// a stuck process never polled during the capture.
func (b *profileBridge) acceptNodeStack(ctx context.Context, c *profileCapture, pid string, profile []byte) {
	upload := profileproto.Upload{Kind: api.ProfileKindStacks, ProcessID: pid, Profile: profile,
		FromUnixNano: time.Now().UnixNano(), UntilUnixNano: time.Now().UnixNano()}
	if err := b.acceptScraped(ctx, c.epoch, upload, false); err != nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.capture == c && len(c.processes) < api.ProfileMaxProcesses {
		c.processes[pid] = true
	}
}

// Expressions evaluated in the Node process. The preload defines the hook;
// without it the snapshot carries no handles and the inspector stays open.
const (
	nodeLivenessExpr = `new Promise(r => setImmediate(() => r(1)))`
	nodeCloseExpr    = `setImmediate(() => globalThis[Symbol.for('gregale.profiling')]?.closeInspector?.())`
)

func nodeSnapshotExpr(epoch string) string {
	return `globalThis[Symbol.for('gregale.profiling')]?.pausedSnapshot?.(` + strconv.Quote(epoch) + `) ?? "[]"`
}

// errNodeOtherProcess means the inspector on the port belongs to another
// process, such as one whose inspector is still closing.
var errNodeOtherProcess = errors.New("inspector belongs to another process")

// captureNodeStack is the production nodeStackSampler.
func captureNodeStack(ctx context.Context, pid int, ni nodeInspector, epoch string) ([]byte, error) {
	if !ni.open {
		if err := syscall.Kill(pid, syscall.SIGUSR1); err != nil {
			return nil, fmt.Errorf("open inspector: %w", err)
		}
	}
	return sampleNodeInspector(ctx, "127.0.0.1:"+strconv.Itoa(ni.port), pid, !ni.open, epoch)
}

// sampleNodeInspector connects to the inspector at addr, verifies it is pid's,
// and snapshots its main thread if the event loop is stuck. closeAfter asks the
// process to close an inspector this capture opened.
func sampleNodeInspector(ctx context.Context, addr string, pid int, closeAfter bool, epoch string) ([]byte, error) {
	target, err := waitNodeInspector(ctx, addr)
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(ctx, api.ProfileNodeInspectorConnect)
	defer cancel()
	ws, resp, err := (&websocket.Dialer{HandshakeTimeout: api.ProfileNodeInspectorConnect}).DialContext(dialCtx, target, nil)
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	if err != nil {
		return nil, fmt.Errorf("connect inspector: %w", err)
	}
	cdp := newCDPConn(ws)
	defer cdp.close()
	// Cleanup calls run even when the capture's context is done.
	cleanup := context.WithoutCancel(ctx)
	var owner struct {
		Result struct {
			Value json.Number `json:"value"`
		} `json:"result"`
	}
	if err := cdp.callInto(ctx, api.ProfileNodeInspectorConnect, "Runtime.evaluate", map[string]any{"expression": "process.pid", "returnByValue": true}, &owner); err != nil {
		return nil, err
	}
	if owner.Result.Value.String() != strconv.Itoa(pid) {
		// Another process listens on the port: its inspector is its own
		// (a developer's --inspect, say) and is never closed from here.
		return nil, errNodeOtherProcess
	}
	if closeAfter {
		defer func() {
			_, _ = cdp.call(cleanup, time.Second, "Runtime.evaluate", map[string]any{"expression": nodeCloseExpr})
		}()
	}
	if _, err := cdp.call(ctx, api.ProfileNodeLivenessTimeout, "Runtime.evaluate",
		map[string]any{"expression": nodeLivenessExpr, "awaitPromise": true, "returnByValue": true}); err == nil {
		return nil, nil // the event loop runs: the collector reports what it waits on
	}
	if _, err := cdp.call(ctx, api.ProfileNodeInspectorConnect, "Debugger.enable", map[string]any{}); err != nil {
		return nil, err
	}
	defer func() { _, _ = cdp.call(cleanup, time.Second, "Debugger.disable", map[string]any{}) }()
	// Resume even when no pause was seen: one that lands after the wait must
	// not leave the application stopped. Disabling the debugger follows.
	defer func() { _, _ = cdp.call(cleanup, time.Second, "Debugger.resume", map[string]any{}) }()
	if _, err := cdp.call(ctx, api.ProfileNodeInspectorConnect, "Debugger.pause", map[string]any{}); err != nil {
		return nil, err
	}
	paused, ok := cdp.waitPaused(ctx, api.ProfileNodePauseTimeout)
	if !ok {
		// Busy, but not in JavaScript: a synchronous native call or GC.
		return encodeStackProfile([]stackSample{{frames: []stackFrame{{name: "busy outside JavaScript (native code or garbage collection)"}, {name: "<main thread>"}}, count: 1}}, time.Now()), nil
	}
	samples := []stackSample{{frames: nodeCallFrames(paused.CallFrames, cdp.scriptURLs()), count: 1}}
	if len(paused.CallFrames) > 0 {
		var snap struct {
			Result struct {
				Value string `json:"value"`
			} `json:"result"`
		}
		if err := cdp.callInto(ctx, api.ProfileNodeInspectorConnect, "Debugger.evaluateOnCallFrame",
			map[string]any{"callFrameId": paused.CallFrames[0].CallFrameID, "expression": nodeSnapshotExpr(epoch), "returnByValue": true}, &snap); err == nil {
			samples = append(samples, nodeHandleSamples(snap.Result.Value)...)
		}
	}
	return encodeStackProfile(samples, time.Now()), nil
}

// waitNodeInspector polls the inspector's target list until it answers, as
// one opened by SIGUSR1 starts listening asynchronously.
func waitNodeInspector(ctx context.Context, addr string) (string, error) {
	client := &http.Client{Timeout: 200 * time.Millisecond, Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	deadline := time.Now().Add(api.ProfileNodeInspectorConnect)
	for {
		target, err := nodeInspectorTarget(ctx, client, addr)
		if err == nil {
			return target, nil
		}
		if time.Now().After(deadline) || ctx.Err() != nil {
			return "", fmt.Errorf("inspector on %s did not answer: %w", addr, err)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func nodeInspectorTarget(ctx context.Context, client *http.Client, addr string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/json/list", nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	var targets []struct {
		URL string `json:"webSocketDebuggerUrl"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 64<<10)).Decode(&targets); err != nil {
		return "", err
	}
	for _, t := range targets {
		// Only a loopback target of this address: the list is the process's.
		if strings.HasPrefix(t.URL, "ws://"+addr+"/") {
			return t.URL, nil
		}
	}
	return "", errors.New("no inspector target")
}

type nodeCallFrame struct {
	CallFrameID  string `json:"callFrameId"`
	FunctionName string `json:"functionName"`
	// URL is deprecated in the protocol and empty in current V8; the
	// location's script id resolves through Debugger.scriptParsed instead.
	URL      string `json:"url"`
	Location struct {
		ScriptID   string `json:"scriptId"`
		LineNumber int64  `json:"lineNumber"`
	} `json:"location"`
}

// nodeCallFrames turns inspector call frames, innermost first, into a stack
// rooted at the main thread.
func nodeCallFrames(callFrames []nodeCallFrame, scripts map[string]string) []stackFrame {
	frames := make([]stackFrame, 0, len(callFrames)+1)
	for i, f := range callFrames {
		if i == api.ProfileMaxStackDepth {
			break
		}
		name := f.FunctionName
		if name == "" {
			name = "(anonymous)"
		}
		url := f.URL
		if url == "" {
			url = scripts[f.Location.ScriptID]
		}
		frames = append(frames, stackFrame{name: name, file: strings.TrimPrefix(url, "file://"), line: f.Location.LineNumber + 1})
	}
	return append(frames, stackFrame{name: "<main thread>"})
}

// nodeHandleSamples reports what the event loop waits on, as the collector
// does for an idle loop.
func nodeHandleSamples(raw string) []stackSample {
	var types []string
	if err := json.Unmarshal([]byte(raw), &types); err != nil {
		return nil
	}
	counts := map[string]int64{}
	for _, t := range types {
		if len(counts) < api.ProfileMaxStackDepth || counts[t] > 0 {
			counts[t]++
		}
	}
	keys := make([]string, 0, len(counts))
	for t := range counts {
		keys = append(keys, t)
	}
	sort.Strings(keys)
	out := make([]stackSample, 0, len(keys))
	for _, t := range keys {
		out = append(out, stackSample{frames: []stackFrame{{name: "waiting on " + t}, {name: "<event loop>"}}, count: counts[t]})
	}
	return out
}

// cdpConn is a minimal Chrome DevTools Protocol client: numbered commands,
// Debugger.paused, and the script URLs Debugger.scriptParsed announces.
type cdpConn struct {
	ws      *websocket.Conn
	writeMu sync.Mutex
	mu      sync.Mutex
	next    int
	pending map[int]chan cdpMessage
	scripts map[string]string
	paused  chan cdpPaused
	done    chan struct{}
}

// cdpMaxScripts bounds the script URLs kept from Debugger.scriptParsed.
const cdpMaxScripts = 50000

type cdpMessage struct {
	ID     int             `json:"id"`
	Method string          `json:"method"`
	Result json.RawMessage `json:"result"`
	Params json.RawMessage `json:"params"`
	Error  *struct {
		Message string `json:"message"`
	} `json:"error"`
}

type cdpPaused struct {
	CallFrames []nodeCallFrame `json:"callFrames"`
}

func newCDPConn(ws *websocket.Conn) *cdpConn {
	// Debugger.enable announces every loaded script; a paused event carries
	// every frame's scopes. Both fit well within this bound.
	ws.SetReadLimit(16 << 20)
	c := &cdpConn{ws: ws, pending: map[int]chan cdpMessage{}, scripts: map[string]string{}, paused: make(chan cdpPaused, 1), done: make(chan struct{})}
	go c.read()
	return c
}

func (c *cdpConn) read() {
	defer close(c.done)
	for {
		var m cdpMessage
		if err := c.ws.ReadJSON(&m); err != nil {
			return
		}
		if m.ID != 0 {
			c.mu.Lock()
			ch := c.pending[m.ID]
			delete(c.pending, m.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- m
			}
			continue
		}
		switch m.Method {
		case "Debugger.scriptParsed":
			var p struct {
				ScriptID string `json:"scriptId"`
				URL      string `json:"url"`
			}
			if json.Unmarshal(m.Params, &p) == nil && p.URL != "" && len(p.URL) <= api.ProfileMaxSymbolBytes {
				c.mu.Lock()
				if len(c.scripts) < cdpMaxScripts {
					c.scripts[p.ScriptID] = p.URL
				}
				c.mu.Unlock()
			}
		case "Debugger.paused":
			var p cdpPaused
			if json.Unmarshal(m.Params, &p) == nil {
				select {
				case c.paused <- p:
				default:
				}
			}
		}
	}
}

func (c *cdpConn) call(ctx context.Context, timeout time.Duration, method string, params any) (json.RawMessage, error) {
	ch := make(chan cdpMessage, 1)
	c.mu.Lock()
	c.next++
	id := c.next
	c.pending[id] = ch
	c.mu.Unlock()
	forget := func() { c.mu.Lock(); delete(c.pending, id); c.mu.Unlock() }
	c.writeMu.Lock()
	_ = c.ws.SetWriteDeadline(time.Now().Add(timeout))
	err := c.ws.WriteJSON(map[string]any{"id": id, "method": method, "params": params})
	c.writeMu.Unlock()
	if err != nil {
		forget()
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case m := <-ch:
		if m.Error != nil {
			return nil, fmt.Errorf("%s: %s", method, m.Error.Message)
		}
		return m.Result, nil
	case <-t.C:
	case <-ctx.Done():
	case <-c.done:
	}
	forget()
	return nil, fmt.Errorf("%s: no answer", method)
}

func (c *cdpConn) callInto(ctx context.Context, timeout time.Duration, method string, params, out any) error {
	raw, err := c.call(ctx, timeout, method, params)
	if err != nil {
		return err
	}
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.UseNumber()
	return dec.Decode(out)
}

func (c *cdpConn) waitPaused(ctx context.Context, timeout time.Duration) (cdpPaused, bool) {
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case p := <-c.paused:
		return p, true
	case <-t.C:
	case <-ctx.Done():
	case <-c.done:
	}
	return cdpPaused{}, false
}

// scriptURLs copies the script URLs announced so far.
func (c *cdpConn) scriptURLs() map[string]string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string]string, len(c.scripts))
	for id, url := range c.scripts {
		out[id] = url
	}
	return out
}

func (c *cdpConn) close() {
	_ = c.ws.Close()
	<-c.done
}

type stackFrame struct {
	name, file string
	line       int64
}

// stackSample is one stack, innermost frame first, and how many threads or
// handles sit on it.
type stackSample struct {
	frames []stackFrame
	count  int64
}

// encodeStackProfile writes an uncompressed pprof profile with the
// threads/count sample type of stacks captures. guest-init only encodes;
// apid parses and normalizes.
func encodeStackProfile(samples []stackSample, at time.Time) []byte {
	strs := []string{""}
	index := map[string]int64{"": 0}
	str := func(s string) int64 {
		if i, ok := index[s]; ok {
			return i
		}
		index[s] = int64(len(strs))
		strs = append(strs, s)
		return index[s]
	}
	type funcKey struct{ name, file string }
	type locKey struct {
		fn   uint64
		line int64
	}
	funcs := map[funcKey]uint64{}
	locs := map[locKey]uint64{}
	var funcMsgs, locMsgs, sampleMsgs [][]byte
	for _, s := range samples {
		var ids []uint64
		for _, f := range s.frames {
			fk := funcKey{f.name, f.file}
			fid, ok := funcs[fk]
			if !ok {
				fid = uint64(len(funcs) + 1)
				funcs[fk] = fid
				var m []byte
				m = protowire.AppendTag(m, 1, protowire.VarintType)
				m = protowire.AppendVarint(m, fid)
				m = protowire.AppendTag(m, 2, protowire.VarintType)
				m = protowire.AppendVarint(m, uint64(str(f.name)))
				m = protowire.AppendTag(m, 3, protowire.VarintType)
				m = protowire.AppendVarint(m, uint64(str(f.name)))
				m = protowire.AppendTag(m, 4, protowire.VarintType)
				m = protowire.AppendVarint(m, uint64(str(f.file)))
				funcMsgs = append(funcMsgs, m)
			}
			lk := locKey{fid, f.line}
			lid, ok := locs[lk]
			if !ok {
				lid = uint64(len(locs) + 1)
				locs[lk] = lid
				var line []byte
				line = protowire.AppendTag(line, 1, protowire.VarintType)
				line = protowire.AppendVarint(line, fid)
				line = protowire.AppendTag(line, 2, protowire.VarintType)
				line = protowire.AppendVarint(line, uint64(f.line))
				var m []byte
				m = protowire.AppendTag(m, 1, protowire.VarintType)
				m = protowire.AppendVarint(m, lid)
				m = protowire.AppendTag(m, 4, protowire.BytesType)
				m = protowire.AppendBytes(m, line)
				locMsgs = append(locMsgs, m)
			}
			ids = append(ids, lid)
		}
		var packed []byte
		for _, id := range ids {
			packed = protowire.AppendVarint(packed, id)
		}
		var m []byte
		m = protowire.AppendTag(m, 1, protowire.BytesType)
		m = protowire.AppendBytes(m, packed)
		m = protowire.AppendTag(m, 2, protowire.BytesType)
		m = protowire.AppendBytes(m, protowire.AppendVarint(nil, uint64(s.count)))
		sampleMsgs = append(sampleMsgs, m)
	}
	valueType := func() []byte {
		var m []byte
		m = protowire.AppendTag(m, 1, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(str("threads")))
		m = protowire.AppendTag(m, 2, protowire.VarintType)
		m = protowire.AppendVarint(m, uint64(str("count")))
		return m
	}
	sampleType, periodType := valueType(), valueType()
	var out []byte
	out = protowire.AppendTag(out, 1, protowire.BytesType)
	out = protowire.AppendBytes(out, sampleType)
	for _, m := range sampleMsgs {
		out = protowire.AppendTag(out, 2, protowire.BytesType)
		out = protowire.AppendBytes(out, m)
	}
	for _, m := range locMsgs {
		out = protowire.AppendTag(out, 4, protowire.BytesType)
		out = protowire.AppendBytes(out, m)
	}
	for _, m := range funcMsgs {
		out = protowire.AppendTag(out, 5, protowire.BytesType)
		out = protowire.AppendBytes(out, m)
	}
	for _, s := range strs {
		out = protowire.AppendTag(out, 6, protowire.BytesType)
		out = protowire.AppendString(out, s)
	}
	out = protowire.AppendTag(out, 9, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(at.UnixNano()))
	out = protowire.AppendTag(out, 11, protowire.BytesType)
	out = protowire.AppendBytes(out, periodType)
	out = protowire.AppendTag(out, 12, protowire.VarintType)
	out = protowire.AppendVarint(out, 1)
	return out
}
