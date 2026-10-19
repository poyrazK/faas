//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

// pprofScraper collects profiles from a Go application's net/http/pprof
// endpoint (profiling.pprof_port, ADR-1155). It follows the bridge's control
// the way the Go SDK's collector does, but fetches each profile over loopback
// HTTP, so the application needs no Gregale code.
type pprofScraper struct {
	bridge *profileBridge
	base   string
	port   int
	pid    string
	client *http.Client
	// poll paces control reads; tests shorten it.
	poll, dormantPoll, probeEvery time.Duration
}

// pprofWindow is one collection window run by its own goroutine. Cancelling
// ctx discards it; otherwise it uploads when its CPU profile returns.
type pprofWindow struct {
	epoch             string
	capture           bool
	cpu, heap, stacks bool
	seconds           int
	checkpoint        time.Duration
	cancel            context.CancelFunc
	done              chan struct{}
	from              time.Time
}

func (w *pprofWindow) finished() bool {
	select {
	case <-w.done:
		return true
	default:
		return false
	}
}

// runPprofScraper serves one run of the workload: pid labels its profiles and
// ctx ends with the process. A port guest-init owns is refused even though
// apid validates it, because it would profile guest-init itself.
func runPprofScraper(ctx context.Context, b *profileBridge, port, pid int, log *slog.Logger) {
	if b == nil || port < 1 || port > 65535 || slices.Contains(api.ProfilePprofReservedPorts(), port) {
		return
	}
	s := newPprofScraper(b, port, pid)
	if !s.waitForEndpoint(ctx) {
		return
	}
	log.Info("profiling: collecting from the application's pprof endpoint", "port", port)
	s.run(ctx)
}

func newPprofScraper(b *profileBridge, port, pid int) *pprofScraper {
	transport := &http.Transport{Proxy: nil, DisableKeepAlives: true, MaxResponseHeaderBytes: 16 << 10}
	return &pprofScraper{bridge: b, base: "http://127.0.0.1:" + strconv.Itoa(port), port: port, pid: strconv.Itoa(pid),
		client: &http.Client{Transport: transport}, poll: api.ProfileControlPollInterval, dormantPoll: time.Second, probeEvery: api.ProfilePprofProbeInterval}
}

// waitForEndpoint probes the pprof index until it answers. Until then the
// scraper registers no collector: a capture reports why it found nothing,
// and memory pressure is not reported for an endpoint that may never exist.
func (s *pprofScraper) waitForEndpoint(ctx context.Context) bool {
	for {
		if _, err := s.fetch(ctx, "/debug/pprof/", api.ProfilePprofFetchTimeout, api.ProfileMaxCompressedBytes); err == nil {
			return true
		}
		s.bridge.noteScrapeProbe(fmt.Sprintf("nothing answered at 127.0.0.1:%d/debug/pprof/ (profiling.pprof_port)", s.port))
		select {
		case <-ctx.Done():
			return false
		case <-time.After(s.probeEvery):
		}
	}
}

func (s *pprofScraper) run(ctx context.Context) {
	var active *pprofWindow
	// finished is the capture epoch whose single window already ended.
	var finished string
	defer func() {
		if active != nil {
			active.cancel()
			<-active.done
		}
	}()
	delay := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		cfg, ok := s.bridge.pprofControl(s.pid)
		if active != nil {
			switch {
			case !ok || cfg.Suspended || cfg.Epoch != active.epoch:
				// A snapshot or restore discards the window rather than
				// relabelling pre-snapshot samples with a new epoch.
				active.cancel()
				<-active.done
				active = nil
			case active.finished():
				if active.capture {
					finished = active.epoch
				}
				active = nil
			}
		}
		delay = s.poll
		switch {
		case !ok:
			delay = s.dormantPoll
		case cfg.Suspended:
			if active == nil {
				s.bridge.pprofAcknowledge(cfg.Epoch)
			}
		case cfg.Enabled && active == nil && cfg.Epoch != "" && !(cfg.Capture && cfg.Epoch == finished):
			active = s.start(ctx, cfg)
		case !cfg.Enabled && active == nil:
			delay = s.dormantPoll
		}
	}
}

func (s *pprofScraper) start(ctx context.Context, cfg profileControl) *pprofWindow {
	wants := func(kind string) bool {
		if len(cfg.Kinds) == 0 {
			return kind == api.ProfileKindCPU
		}
		return slices.Contains(cfg.Kinds, kind)
	}
	w := &pprofWindow{epoch: cfg.Epoch, capture: cfg.Capture, cpu: wants(api.ProfileKindCPU), heap: wants(api.ProfileKindHeap),
		stacks: cfg.Capture && wants(api.ProfileKindStacks), seconds: max(cfg.WindowSeconds, api.ProfileMinWindowSeconds),
		done: make(chan struct{}), from: time.Now()}
	if cfg.Capture {
		w.checkpoint = time.Duration(cfg.CheckpointSeconds) * time.Second
	}
	var wctx context.Context
	wctx, w.cancel = context.WithCancel(ctx)
	go s.collect(wctx, w)
	return w
}

// collect runs one window: an instant goroutine snapshot first (captures
// only), then the CPU profile, whose request lasts the window, with interim
// heap checkpoints alongside, then the heap profile at the window end.
func (s *pprofScraper) collect(ctx context.Context, w *pprofWindow) {
	defer close(w.done)
	if w.stacks {
		s.collectKind(ctx, w, api.ProfileKindStacks, "/debug/pprof/goroutine", api.ProfilePprofFetchTimeout, false)
	}
	if !w.cpu && !w.heap {
		return
	}
	span := time.Duration(w.seconds) * time.Second
	var cpuBody []byte
	var cpuErr error
	windowDone := make(chan struct{})
	go func() {
		defer close(windowDone)
		if w.cpu {
			cpuBody, cpuErr = s.fetch(ctx, "/debug/pprof/profile?seconds="+strconv.Itoa(w.seconds), span+api.ProfilePprofFetchTimeout, api.ProfileMaxCompressedBytes)
			return
		}
		select {
		case <-time.After(span):
		case <-ctx.Done():
		}
	}()
	var tick <-chan time.Time
	if w.heap && w.checkpoint > 0 {
		t := time.NewTicker(w.checkpoint)
		defer t.Stop()
		tick = t.C
	}
	for waiting := true; waiting; {
		select {
		case <-ctx.Done():
			<-windowDone
			return
		case <-windowDone:
			waiting = false
		case <-tick:
			s.collectKind(ctx, w, api.ProfileKindHeap, "/debug/pprof/heap", api.ProfilePprofFetchTimeout, true)
		}
	}
	if ctx.Err() != nil {
		return
	}
	if w.cpu {
		if cpuErr != nil {
			s.noteFailure(w, api.ProfileKindCPU, cpuErr)
		} else {
			s.upload(ctx, w, api.ProfileKindCPU, cpuBody, time.Now(), false)
		}
	}
	if w.heap {
		s.collectKind(ctx, w, api.ProfileKindHeap, "/debug/pprof/heap", api.ProfilePprofFetchTimeout, false)
	}
}

func (s *pprofScraper) collectKind(ctx context.Context, w *pprofWindow, kind, path string, timeout time.Duration, checkpoint bool) {
	body, err := s.fetch(ctx, path, timeout, api.ProfileMaxCompressedBytes)
	if err != nil {
		if ctx.Err() == nil && !checkpoint {
			s.noteFailure(w, kind, err)
		}
		return
	}
	s.upload(ctx, w, kind, body, time.Now(), checkpoint)
}

func (s *pprofScraper) upload(ctx context.Context, w *pprofWindow, kind string, body []byte, until time.Time, checkpoint bool) {
	upload := profileproto.Upload{Profile: body, ProcessID: s.pid, FromUnixNano: w.from.Unix() * int64(time.Second), UntilUnixNano: until.Unix() * int64(time.Second)}
	if kind != api.ProfileKindCPU {
		upload.Kind = kind
	}
	uctx, cancel := context.WithTimeout(ctx, api.ProfileTransportTimeout)
	defer cancel()
	_ = s.bridge.acceptScraped(uctx, w.epoch, upload, checkpoint)
}

func (s *pprofScraper) noteFailure(w *pprofWindow, kind string, err error) {
	if w.capture {
		s.bridge.noteScrapeFailure(w.epoch, fmt.Sprintf("the %s profile from 127.0.0.1:%d (profiling.pprof_port) was not collected: %v", kind, s.port, err))
	}
}

// errPprofTooLarge marks a profile over the upload bound; it is dropped
// rather than truncated into an unparsable profile.
var errPprofTooLarge = errors.New("the profile exceeds the 1 MiB upload bound")

// fetch reads one endpoint. Errors are phrased for the capture reason: they
// carry the status code, never the response body, which is the
// application's.
func (s *pprofScraper) fetch(ctx context.Context, path string, timeout time.Duration, limit int64) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.base+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return nil, errors.New("the endpoint did not answer in time")
		}
		return nil, errors.New("the endpoint could not be reached")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
		return nil, fmt.Errorf("the endpoint answered HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	switch {
	case err != nil:
		return nil, errors.New("the response ended early")
	case int64(len(body)) > limit:
		return nil, errPprofTooLarge
	case len(body) == 0:
		return nil, errors.New("the endpoint returned an empty profile")
	}
	return body, nil
}

// pprofControl is the scraper's control poll. It registers the scraped
// process like a collector's poll does, unless a collector of the
// application's own polled recently: an application running the Gregale SDK
// or a managed preload is profiled by that collector, and a second one would
// contend for Go's single CPU profiler and double-count the heap.
func (b *profileBridge) pprofControl(pid string) (profileControl, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, at := range b.processes {
		if time.Since(at) < api.ProfileProcessStaleAfter {
			return b.control, false
		}
	}
	b.pprofSeen = time.Now()
	if b.capture != nil && b.control.Epoch == b.capture.epoch && len(b.capture.processes) < api.ProfileMaxProcesses {
		b.capture.processes[pid] = true
	}
	return b.control, true
}

// pprofAcknowledge records that the scraper stopped for a checkpoint.
func (b *profileBridge) pprofAcknowledge(epoch string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if epoch == b.control.Epoch {
		b.pprofAck = epoch
	}
}

// acceptScraped takes a scraped profile under the rules /ingest applies to a
// collector's upload: it must belong to the current epoch.
func (b *profileBridge) acceptScraped(ctx context.Context, epoch string, upload profileproto.Upload, checkpoint bool) error {
	b.mu.Lock()
	current := b.control.Epoch
	b.mu.Unlock()
	if epoch == "" || epoch != current {
		return fmt.Errorf("profile belongs to an earlier collection epoch")
	}
	if checkpoint {
		if !b.acceptCheckpoint(epoch, upload) {
			return fmt.Errorf("no capture is collecting checkpoints")
		}
		return nil
	}
	return b.accept(ctx, epoch, upload)
}

// noteScrapeFailure keeps the first scrape failure of the capture in epoch
// for its reason.
func (b *profileBridge) noteScrapeFailure(epoch, note string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.capture; c != nil && c.epoch == epoch && c.scrapeNote == "" {
		c.scrapeNote = note
	}
}

// noteScrapeProbe explains, for a running capture, that the configured pprof
// endpoint has not answered; it is the reason only when nothing registered.
func (b *profileBridge) noteScrapeProbe(note string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if c := b.capture; c != nil && c.probeNote == "" {
		c.probeNote = note
	}
}
