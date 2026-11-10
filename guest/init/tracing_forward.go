package main

// In-VM OTLP forwarding (ADR-20261108100000): the trace bridge hands every
// export it receives to a traceForwarder, which posts the same bytes to the
// customer's own OTLP/HTTP endpoint. Forwarding never delays or fails the
// app's export: it is asynchronous, bounded by queued bytes, and drops what
// does not fit. An SDK retrying an export (because Gregale answered 429 or
// 503) sends identical bytes, so a recently forwarded body is not sent again.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
)

// forwardExport is one export waiting to be forwarded.
type forwardExport struct {
	url             string
	contentType     string
	contentEncoding string
	body            []byte
}

type traceForwarder struct {
	cfg     api.TracingForward
	client  *http.Client
	headers func() (http.Header, error)
	// observe receives each export's final outcome (diagnostics only):
	// delivered, dropped_queue_full, duplicate, failed.
	observe func(outcome string, err error)
	now     func() time.Time
	sleep   func(context.Context, time.Duration) bool

	queue chan forwardExport

	mu          sync.Mutex
	queuedBytes int
	maxBytes    int
	seen        map[[sha256.Size]byte]time.Time
}

const (
	traceForwardQueueSlots = 64
	traceForwardMaxSeen    = 512
)

func newTraceForwarder(cfg api.TracingForward, headers func() (http.Header, error)) *traceForwarder {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	// A connection opened before a snapshot is dead after restore; keep idle
	// connections short so the first export after a wake opens a new one.
	transport.IdleConnTimeout = 15 * time.Second
	transport.MaxIdleConnsPerHost = 2
	return &traceForwarder{
		cfg:      cfg,
		client:   &http.Client{Timeout: api.TraceForwardTimeout, Transport: transport},
		headers:  headers,
		now:      time.Now,
		sleep:    forwardSleep,
		queue:    make(chan forwardExport, traceForwardQueueSlots),
		maxBytes: api.TraceForwardMaxQueuedBytes,
		seen:     make(map[[sha256.Size]byte]time.Time),
	}
}

func forwardSleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// enqueue queues one export for forwarding; signalPath is
// api.TraceLocalTracesPath or api.TraceLocalLogsPath. It never blocks.
func (f *traceForwarder) enqueue(signalPath, contentType, contentEncoding string, body []byte) bool {
	if f == nil || len(body) == 0 {
		return false
	}
	key := sha256.Sum256(append([]byte(signalPath+"\x00"), body...))
	f.mu.Lock()
	now := f.now()
	if at, ok := f.seen[key]; ok && now.Sub(at) < api.TraceForwardDedupWindow {
		f.mu.Unlock()
		f.report("duplicate", nil)
		return false
	}
	if f.queuedBytes+len(body) > f.maxBytes {
		f.mu.Unlock()
		f.report("dropped_queue_full", nil)
		return false
	}
	item := forwardExport{url: f.cfg.SignalURL(signalPath), contentType: contentType, contentEncoding: contentEncoding, body: body}
	select {
	case f.queue <- item:
	default:
		f.mu.Unlock()
		f.report("dropped_queue_full", nil)
		return false
	}
	f.queuedBytes += len(body)
	f.rememberLocked(key, now)
	f.mu.Unlock()
	return true
}

// rememberLocked records a forwarded body, pruning expired entries and, past
// the bound, forgetting everything (a missed dedup only costs a duplicate).
func (f *traceForwarder) rememberLocked(key [sha256.Size]byte, now time.Time) {
	if len(f.seen) >= traceForwardMaxSeen {
		for k, at := range f.seen {
			if now.Sub(at) >= api.TraceForwardDedupWindow {
				delete(f.seen, k)
			}
		}
		if len(f.seen) >= traceForwardMaxSeen {
			clear(f.seen)
		}
	}
	f.seen[key] = now
}

// run delivers queued exports until ctx ends, with workers in parallel.
func (f *traceForwarder) run(ctx context.Context, workers int) {
	var wg sync.WaitGroup
	for range max(workers, 1) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item := <-f.queue:
					err := f.deliver(ctx, item)
					f.mu.Lock()
					f.queuedBytes -= len(item.body)
					f.mu.Unlock()
					if err != nil {
						f.report("failed", err)
					} else {
						f.report("delivered", nil)
					}
				}
			}
		}()
	}
	wg.Wait()
}

// errForwardPermanent marks a refusal retrying cannot fix.
var errForwardPermanent = errors.New("permanent")

// deliver posts one export, retrying transport errors, 429 and 5xx with
// backoff, as OTLP/HTTP exporters do.
func (f *traceForwarder) deliver(ctx context.Context, item forwardExport) error {
	var last error
	backoff := 500 * time.Millisecond
	for attempt := 1; attempt <= api.TraceForwardMaxAttempts; attempt++ {
		if attempt > 1 && !f.sleep(ctx, backoff) {
			return ctx.Err()
		}
		backoff *= 2
		last = f.post(ctx, item)
		if last == nil || errors.Is(last, errForwardPermanent) {
			return last
		}
	}
	return last
}

func (f *traceForwarder) post(ctx context.Context, item forwardExport) error {
	header, err := f.headers()
	if err != nil {
		return fmt.Errorf("forward headers: %w: %w", errForwardPermanent, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, item.url, bytes.NewReader(item.body))
	if err != nil {
		return fmt.Errorf("%w: %w", errForwardPermanent, err)
	}
	for k, v := range header {
		req.Header[k] = v
	}
	req.Header.Set("Content-Type", item.contentType)
	if item.contentEncoding != "" {
		req.Header.Set("Content-Encoding", item.contentEncoding)
	}
	req.Header.Set("User-Agent", "gregale-otlp-forward/1")
	resp, err := f.client.Do(req)
	if err != nil {
		return err
	}
	_, _ = io.CopyN(io.Discard, resp.Body, 4096)
	_ = resp.Body.Close()
	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusBadGateway ||
		resp.StatusCode == http.StatusServiceUnavailable || resp.StatusCode == http.StatusGatewayTimeout:
		return fmt.Errorf("forward endpoint answered %d", resp.StatusCode)
	default:
		return fmt.Errorf("forward endpoint answered %d: %w", resp.StatusCode, errForwardPermanent)
	}
}

func (f *traceForwarder) report(outcome string, err error) {
	if f.observe != nil {
		f.observe(outcome, err)
	}
}

// newForwardOutcomeLogger logs the first forwarded export once and any
// forwarding problem at most every 30 seconds. Duplicates (an SDK retry of a
// body already forwarded) are expected and not logged.
func newForwardOutcomeLogger(log *slog.Logger) func(string, error) {
	var mu sync.Mutex
	var delivered bool
	var lastProblem time.Time
	return func(outcome string, err error) {
		mu.Lock()
		defer mu.Unlock()
		switch outcome {
		case "duplicate":
			return
		case "delivered":
			if !delivered {
				delivered = true
				log.Info("trace forwarding delivered first export")
			}
			return
		}
		if time.Since(lastProblem) < 30*time.Second {
			return
		}
		lastProblem = time.Now()
		log.Warn("trace forwarding export not delivered", "outcome", outcome, "err", err)
	}
}

// forwardHeaderSource reads the headers secret from the secrets file at send
// time, re-parsing only when the file changes: vmmd rewrites it on every
// wake, so a rotated secret applies after the next restore without the
// bridge keeping a stale copy from before the snapshot.
type forwardHeaderSource struct {
	path string
	name string

	mu      sync.Mutex
	modTime time.Time
	size    int64
	header  http.Header
	err     error
	loaded  bool
}

func (s *forwardHeaderSource) get() (http.Header, error) {
	if s.name == "" {
		return http.Header{}, nil
	}
	info, statErr := os.Stat(s.path)
	s.mu.Lock()
	defer s.mu.Unlock()
	if statErr == nil && s.loaded && info.ModTime().Equal(s.modTime) && info.Size() == s.size {
		return s.header, s.err
	}
	s.loaded = statErr == nil
	if statErr == nil {
		s.modTime, s.size = info.ModTime(), info.Size()
	}
	s.header, s.err = s.load()
	return s.header, s.err
}

func (s *forwardHeaderSource) load() (http.Header, error) {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return nil, fmt.Errorf("secret %s is unavailable", s.name)
	}
	secrets := map[string]string{}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &secrets); err != nil {
			return nil, fmt.Errorf("secret %s is unavailable", s.name)
		}
	}
	raw, ok := secrets[s.name]
	if !ok {
		return nil, fmt.Errorf("secret %s is not set", s.name)
	}
	return parseOTLPHeaders(raw)
}

// parseOTLPHeaders parses the OTEL_EXPORTER_OTLP_HEADERS form: comma-separated
// key=value pairs with percent-encoded values. Errors never include values.
func parseOTLPHeaders(raw string) (http.Header, error) {
	h := http.Header{}
	for _, pair := range strings.Split(raw, ",") {
		if strings.TrimSpace(pair) == "" {
			continue
		}
		key, value, ok := strings.Cut(pair, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, errors.New("headers must be key=value pairs separated by commas")
		}
		decoded, err := url.PathUnescape(strings.TrimSpace(value))
		if err != nil {
			return nil, fmt.Errorf("header %q has an invalid percent-encoded value", key)
		}
		if strings.ContainsAny(key, " \t\r\n:") || strings.ContainsAny(decoded, "\r\n\x00") {
			return nil, fmt.Errorf("header %q is invalid", key)
		}
		switch http.CanonicalHeaderKey(key) {
		case "Host", "Content-Length", "Content-Type", "Content-Encoding", "Transfer-Encoding", "Connection":
			return nil, fmt.Errorf("header %q cannot be set", key)
		}
		h.Add(key, decoded)
	}
	return h, nil
}
