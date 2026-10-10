// Package guestprofiling enables CPU and heap profiling in Go applications
// running on Gregale. Call Start once during application startup (ADR-819,
// ADR-967). Collection is driven by the deployment's continuous profiling
// configuration and by on-demand captures; otherwise the collector is dormant.
package guestprofiling

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

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

type control struct {
	Enabled       bool     `json:"enabled"`
	Suspended     bool     `json:"suspended"`
	Epoch         string   `json:"epoch"`
	WindowSeconds int      `json:"window_seconds"`
	Kinds         []string `json:"kinds,omitempty"`
	Capture       bool     `json:"capture,omitempty"`
}

func (c control) wants(kind string) bool {
	if len(c.Kinds) == 0 {
		return kind == api.ProfileKindCPU
	}
	return slices.Contains(c.Kinds, kind)
}

// dormantPollInterval paces control polls while nothing is collected. An
// on-demand capture therefore starts within one interval.
const dormantPollInterval = time.Second

var started atomic.Bool

// Start returns immediately. Collection is opt-in through the deployment's
// profiling configuration or an on-demand capture. It shares Go's process-wide profiler, so callers
// must not also run runtime/pprof.StartCPUProfile. Cancel ctx before exiting.
func Start(ctx context.Context) {
	if os.Getenv("FAAS_PROFILING_ENABLED") != "1" || !started.CompareAndSwap(false, true) {
		return
	}
	endpoint := os.Getenv("FAAS_PROFILING_ENDPOINT")
	if endpoint == "" {
		endpoint = api.ProfileLocalEndpoint
	}
	go func() { defer started.Store(false); run(ctx, endpoint) }()
}

// window is one collection interval. Heap profiles are read at its end;
// Go's heap profile is always sampled, so it reports the live heap.
type window struct {
	epoch string
	from  time.Time
	span  time.Duration
	cpu   bool
	heap  bool
}

func run(ctx context.Context, endpoint string) {
	client := &http.Client{Timeout: api.ProfileTransportTimeout}
	pid := strconv.Itoa(os.Getpid())
	var buffer bytes.Buffer
	var active *window
	// finished is the capture epoch whose single window already ended.
	var finished string
	stop := func(upload bool) {
		w := active
		active = nil
		if w == nil {
			return
		}
		var report *profileproto.RouteRequestReport
		if w.cpu {
			report = stopRouteRequestWindow()
			pprof.StopCPUProfile()
		}
		if !upload {
			buffer.Reset()
			return
		}
		until := time.Now()
		if w.cpu {
			_ = uploadKind(ctx, client, endpoint, api.ProfileKindCPU, w.epoch, w.from, until, buffer.Bytes(), report)
		}
		if w.heap {
			var heap bytes.Buffer
			if err := pprof.Lookup("heap").WriteTo(&heap, 0); err == nil {
				_ = uploadKind(ctx, client, endpoint, api.ProfileKindHeap, w.epoch, w.from, until, heap.Bytes())
			}
		}
		buffer.Reset()
	}
	defer stop(false)
	delay := api.ProfileControlPollInterval
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(delay):
		}
		cfg, err := readControl(ctx, client, endpoint, pid)
		if err != nil {
			if active != nil && time.Since(active.from) >= active.span {
				stop(false)
			}
			delay = api.ProfileControlPollInterval
			continue
		}
		if active != nil && (cfg.Suspended || !cfg.Enabled || cfg.Epoch != active.epoch || time.Since(active.from) >= active.span) {
			// A restored process discards its pre-snapshot buffer rather than
			// relabelling old samples with the new deployment/instance epoch.
			if cfg.Capture && cfg.Epoch == active.epoch {
				finished = cfg.Epoch
			}
			stop(cfg.Epoch == active.epoch)
		}
		if cfg.Suspended {
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/control/ack?pid="+pid+"&epoch="+url.QueryEscape(cfg.Epoch), nil)
			if err == nil {
				resp, err := client.Do(req)
				if err == nil {
					_ = resp.Body.Close()
				}
			}
		} else if cfg.Enabled && active == nil && cfg.Epoch != "" && !(cfg.Capture && cfg.Epoch == finished) {
			w := &window{epoch: cfg.Epoch, from: time.Now(), span: time.Duration(cfg.WindowSeconds) * time.Second, heap: cfg.wants(api.ProfileKindHeap)}
			buffer.Reset()
			if cfg.wants(api.ProfileKindCPU) {
				if err := pprof.StartCPUProfile(&buffer); err == nil {
					w.cpu = true
					startRouteRequestWindow()
				}
			}
			if w.cpu || w.heap {
				active = w
			}
		}
		delay = api.ProfileControlPollInterval
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
	err = json.NewDecoder(io.LimitReader(resp.Body, api.ProfileControlMaxBytes)).Decode(&cfg)
	if cfg.WindowSeconds < api.ProfileMinWindowSeconds || cfg.WindowSeconds > api.ProfileMaxWindowSeconds {
		return cfg, fmt.Errorf("invalid profile window")
	}
	return cfg, err
}

func uploadKind(ctx context.Context, client *http.Client, endpoint, kind, epoch string, from, until time.Time, body []byte, reports ...*profileproto.RouteRequestReport) error {
	query := url.Values{"name": {"gregale{gregale_epoch=" + strconv.Quote(epoch) + ",gregale_process=" + strconv.Itoa(os.Getpid()) + "}"}, "from": {strconv.FormatInt(from.Unix(), 10)}, "until": {strconv.FormatInt(until.Unix(), 10)}}
	if kind != api.ProfileKindCPU {
		query.Set("kind", kind)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/ingest?"+query.Encode(), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	if len(reports) == 1 && reports[0] != nil {
		if data, err := json.Marshal(reports[0]); err == nil && len(data) <= api.ProfileRouteRequestReportMaxBytes {
			req.Header.Set(profileproto.RouteRequestHeader, base64.RawURLEncoding.EncodeToString(data))
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
