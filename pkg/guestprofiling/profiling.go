// Package guestprofiling enables CPU profiling in Go applications running on
// Gregale. Call Start once during application startup (ADR-792).
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
	"strconv"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/profileproto"
)

type control struct {
	Enabled       bool   `json:"enabled"`
	Suspended     bool   `json:"suspended"`
	Epoch         string `json:"epoch"`
	WindowSeconds int    `json:"window_seconds"`
}

var started atomic.Bool

// Start returns immediately. Collection is opt-in through the deployment's
// profiling configuration. It shares Go's process-wide profiler, so callers
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

func run(ctx context.Context, endpoint string) {
	client := &http.Client{Timeout: api.ProfileTransportTimeout}
	pid := strconv.Itoa(os.Getpid())
	var buffer bytes.Buffer
	var epoch string
	var from time.Time
	running := false
	window := time.Duration(api.ProfileDefaultWindowSeconds) * time.Second
	defer func() {
		if running {
			stopRouteRequestWindow()
			pprof.StopCPUProfile()
		}
	}()
	ticker := time.NewTicker(api.ProfileControlPollInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		cfg, err := readControl(ctx, client, endpoint, pid)
		if err != nil {
			if running && time.Since(from) >= window {
				stopRouteRequestWindow()
				pprof.StopCPUProfile()
				running = false
				buffer.Reset()
			}
			continue
		}
		if running && (cfg.Suspended || !cfg.Enabled || cfg.Epoch != epoch || time.Since(from) >= time.Duration(cfg.WindowSeconds)*time.Second) {
			report := stopRouteRequestWindow()
			pprof.StopCPUProfile()
			running = false
			// A restored process discards its pre-snapshot buffer rather than
			// relabelling old samples with the new deployment/instance epoch.
			if cfg.Epoch == epoch {
				_ = upload(ctx, client, endpoint, epoch, from, time.Now(), buffer.Bytes(), report)
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
		} else if cfg.Enabled && !running && cfg.Epoch != "" {
			buffer.Reset()
			window = time.Duration(cfg.WindowSeconds) * time.Second
			from = time.Now()
			epoch = cfg.Epoch
			if err := pprof.StartCPUProfile(&buffer); err == nil {
				running = true
				startRouteRequestWindow()
			}
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

func upload(ctx context.Context, client *http.Client, endpoint, epoch string, from, until time.Time, body []byte, reports ...*profileproto.RouteRequestReport) error {
	query := url.Values{"name": {"gregale{gregale_epoch=" + strconv.Quote(epoch) + ",gregale_process=" + strconv.Itoa(os.Getpid()) + "}"}, "from": {strconv.FormatInt(from.Unix(), 10)}, "until": {strconv.FormatInt(until.Unix(), 10)}}
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
