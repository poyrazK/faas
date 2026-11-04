// Package profiling sends a Go app's CPU, heap, goroutine, block and mutex
// profiles to Gregale's continuous profiling and on-demand captures
// (ADR-819, ADR-1155) without depending on the platform module.
//
//	func main() {
//		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
//		defer stop()
//		profiling.Start(ctx)
//		...
//		http.ListenAndServe(":"+os.Getenv("PORT"), profiling.ServeMux(mux))
//	}
//
// Collection follows the deployment: guest-init sets FAAS_PROFILING_ENABLED=1
// and the endpoint of its local bridge when the app has continuous profiling
// or on-demand captures. Everywhere else (local runs, tests) Start does
// nothing. While no window is open the collector only polls the bridge once a
// second.
package profiling

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"runtime/pprof"
	"slices"
	"strconv"
	"sync/atomic"
	"time"
)

// The bridge contract. These mirror the platform's limits (pkg/api) and must
// change with them; guest-init accepts what an older SDK sends.
const (
	defaultEndpoint      = "http://127.0.0.1:9191"
	controlPollInterval  = 100 * time.Millisecond
	dormantPollInterval  = time.Second
	transportTimeout     = 2 * time.Second
	controlMaxBytes      = 4096
	minWindowSeconds     = 1
	maxWindowSeconds     = 60
	captureGrace         = 3 * time.Second
	routeReportMaxBytes  = 16 << 10
	routeRequestHeader   = "X-Gregale-Route-Requests"
	kindCPU              = "cpu"
	kindHeap             = "heap"
	kindStacks           = "stacks"
	kindBlock            = "block"
	kindMutex            = "mutex"
	blockRateNanos       = 10_000
	mutexProfileFraction = 10
)

type control struct {
	Enabled       bool     `json:"enabled"`
	Suspended     bool     `json:"suspended"`
	Epoch         string   `json:"epoch"`
	WindowSeconds int      `json:"window_seconds"`
	Kinds         []string `json:"kinds,omitempty"`
	Capture       bool     `json:"capture,omitempty"`
	// CheckpointSeconds asks a capture for interim heap profiles.
	CheckpointSeconds int `json:"checkpoint_seconds,omitempty"`
}

func (c control) wants(kind string) bool {
	if len(c.Kinds) == 0 {
		return kind == kindCPU
	}
	return slices.Contains(c.Kinds, kind)
}

var started atomic.Bool

// Start returns immediately. Collection is opt-in through the deployment's
// profiling configuration or an on-demand capture. It shares Go's
// process-wide profiler, so callers must not also run
// runtime/pprof.StartCPUProfile. Cancel ctx before exiting.
func Start(ctx context.Context) {
	if os.Getenv("FAAS_PROFILING_ENABLED") != "1" || !started.CompareAndSwap(false, true) {
		return
	}
	endpoint := os.Getenv("FAAS_PROFILING_ENDPOINT")
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	go func() { defer started.Store(false); run(ctx, endpoint) }()
}

// window is one collection interval. Heap profiles are read at its end;
// Go's heap profile is always sampled, so it reports the live heap.
type window struct {
	epoch      string
	from       time.Time
	span       time.Duration
	capture    bool
	cpu        bool
	heap       bool
	checkpoint time.Time
	// contention holds a capture's block and mutex baselines.
	contention []*contention
}

func run(ctx context.Context, endpoint string) {
	client := &http.Client{Timeout: transportTimeout}
	pid := strconv.Itoa(os.Getpid())
	var buffer bytes.Buffer
	var active *window
	// finished is the capture epoch whose single window already ended.
	var finished string
	// stacksEpoch is the capture epoch whose stack snapshot was sent.
	var stacksEpoch string
	stop := func(upload bool) {
		w := active
		active = nil
		if w == nil {
			return
		}
		var report *routeRequestReport
		if w.cpu {
			report = stopRouteRequestWindow()
			pprof.StopCPUProfile()
		}
		until := time.Now()
		for _, c := range w.contention {
			if body, err := c.finish(upload); err == nil && len(body) > 0 {
				_ = uploadKind(ctx, client, endpoint, c.kind, w.epoch, w.from, until, body, false)
			}
		}
		if !upload {
			buffer.Reset()
			return
		}
		if w.cpu {
			_ = uploadKind(ctx, client, endpoint, kindCPU, w.epoch, w.from, until, buffer.Bytes(), false, report)
		}
		if w.heap {
			var heap bytes.Buffer
			if err := pprof.Lookup("heap").WriteTo(&heap, 0); err == nil {
				_ = uploadKind(ctx, client, endpoint, kindHeap, w.epoch, w.from, until, heap.Bytes(), false)
			}
		}
		buffer.Reset()
	}
	defer stop(false)
	delay := controlPollInterval
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		cfg, err := readControl(ctx, client, endpoint, pid)
		if err != nil {
			if w := active; w != nil && time.Since(w.from) >= w.span {
				// A capture's window that just ended is still sent: the person
				// waiting on it would otherwise get nothing for one failed
				// poll. Past the capture grace nobody takes it, and a window
				// frozen across a snapshot must not be relabelled, so it and
				// a continuous window are discarded.
				ended := w.capture && time.Since(w.from) <= w.span+captureGrace
				if ended {
					finished = w.epoch
				}
				stop(ended)
			}
			delay = controlPollInterval
			continue
		}
		if active != nil && (cfg.Suspended || !cfg.Enabled || cfg.Epoch != active.epoch || time.Since(active.from) >= active.span) {
			if cfg.Capture && cfg.Epoch == active.epoch {
				finished = cfg.Epoch
			}
			// A capture starting over a continuous window ends it, and its
			// samples go out under their own epoch, which the bridge accepts
			// during the capture. Any other new epoch is a checkpoint or
			// restore: the pre-snapshot buffer is discarded, not relabelled.
			stop(cfg.Epoch == active.epoch || cfg.Capture && !cfg.Suspended && !active.capture)
		}
		if !cfg.Suspended && cfg.Enabled && cfg.Capture && cfg.Epoch != stacksEpoch && cfg.wants(kindStacks) {
			// An instant snapshot, taken as the capture starts: closest to
			// whatever triggered it.
			stacksEpoch = cfg.Epoch
			var stacks bytes.Buffer
			if err := pprof.Lookup("goroutine").WriteTo(&stacks, 0); err == nil {
				now := time.Now()
				_ = uploadKind(ctx, client, endpoint, kindStacks, cfg.Epoch, now, now, stacks.Bytes(), false)
			}
		}
		if cfg.Suspended {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/control/ack?pid="+pid+"&epoch="+url.QueryEscape(cfg.Epoch), nil)
			if err == nil {
				resp, err := client.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
			}
		} else if cfg.Enabled && active == nil && cfg.Epoch != "" && (!cfg.Capture || cfg.Epoch != finished) {
			w := &window{epoch: cfg.Epoch, from: time.Now(), span: time.Duration(cfg.WindowSeconds) * time.Second, capture: cfg.Capture, heap: cfg.wants(kindHeap)}
			w.checkpoint = w.from
			buffer.Reset()
			if cfg.wants(kindCPU) {
				if err := pprof.StartCPUProfile(&buffer); err == nil {
					w.cpu = true
					startRouteRequestWindow()
				}
			}
			if cfg.Capture {
				for _, kind := range []string{kindBlock, kindMutex} {
					if cfg.wants(kind) {
						if c := startContention(kind); c != nil {
							w.contention = append(w.contention, c)
						}
					}
				}
			}
			if w.cpu || w.heap || len(w.contention) > 0 {
				active = w
			}
		}
		if w := active; w != nil && w.heap && cfg.Capture && cfg.Epoch == w.epoch && cfg.CheckpointSeconds > 0 &&
			time.Since(w.checkpoint) >= time.Duration(cfg.CheckpointSeconds)*time.Second {
			// Interim heap for an OOM kill that would end the window early.
			w.checkpoint = time.Now()
			var heap bytes.Buffer
			if err := pprof.Lookup("heap").WriteTo(&heap, 0); err == nil {
				_ = uploadKind(ctx, client, endpoint, kindHeap, w.epoch, w.from, w.checkpoint, heap.Bytes(), true)
			}
		}
		delay = controlPollInterval
		if !cfg.Enabled && active == nil {
			delay = dormantPollInterval
		}
	}
}

func readControl(ctx context.Context, client *http.Client, endpoint, pid string) (control, error) {
	var cfg control
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/control?pid="+pid, nil)
	if err != nil {
		return cfg, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return cfg, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return cfg, fmt.Errorf("profile control unavailable")
	}
	err = json.NewDecoder(io.LimitReader(resp.Body, controlMaxBytes)).Decode(&cfg)
	if cfg.WindowSeconds < minWindowSeconds || cfg.WindowSeconds > maxWindowSeconds {
		return cfg, fmt.Errorf("invalid profile window")
	}
	return cfg, err
}

func uploadKind(ctx context.Context, client *http.Client, endpoint, kind, epoch string, from, until time.Time, body []byte, checkpoint bool, reports ...*routeRequestReport) error {
	query := url.Values{"name": {"gregale{gregale_epoch=" + strconv.Quote(epoch) + ",gregale_process=" + strconv.Itoa(os.Getpid()) + "}"}, "from": {strconv.FormatInt(from.Unix(), 10)}, "until": {strconv.FormatInt(until.Unix(), 10)}}
	if kind != kindCPU {
		query.Set("kind", kind)
	}
	if checkpoint {
		query.Set("checkpoint", "1")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/ingest?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if len(reports) == 1 && reports[0] != nil {
		if data, err := json.Marshal(reports[0]); err == nil && len(data) <= routeReportMaxBytes {
			req.Header.Set(routeRequestHeader, base64.RawURLEncoding.EncodeToString(data))
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("profile upload unavailable")
	}
	return nil
}
