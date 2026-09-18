package runtimeconfig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// DefaultLiveEndpoint is the platform-owned metadata endpoint advertised
	// by guest-init. Applications opt into this helper; it never mutates the
	// process environment automatically.
	DefaultLiveEndpoint = "http://169.254.169.254/v1/metadata/env"

	DefaultLivePollInterval = 5 * time.Second
	defaultLiveTimeout      = 4 * time.Second
	maxLiveResponseBytes    = 32 << 10
)

// ErrLiveUnavailable indicates that the metadata endpoint could not provide
// a usable snapshot. Callers may keep their last-known configuration when
// this occurs.
var ErrLiveUnavailable = errors.New("live runtime config unavailable")

// LiveSnapshot is the non-sensitive, default-scope configuration returned by
// the in-guest metadata endpoint. Values are copied before they are exposed
// to callers so one callback cannot mutate a later snapshot.
type LiveSnapshot struct {
	Env      map[string]string `json:"env,omitempty"`
	Revision string            `json:"revision,omitempty"`
}

// LiveClient fetches opt-in configuration from a guest metadata endpoint.
// Endpoint may be overridden for tests or a platform-specific deployment;
// NewLiveClientFromEnv reads FAAS_METADATA_ENV_ENDPOINT when present.
type LiveClient struct {
	Endpoint   string
	HTTPClient *http.Client
	MaxBytes   int64
}

// NewLiveClient constructs a bounded client for endpoint. An empty endpoint
// uses the platform metadata address.
func NewLiveClient(endpoint string) *LiveClient {
	if strings.TrimSpace(endpoint) == "" {
		endpoint = DefaultLiveEndpoint
	}
	return &LiveClient{
		Endpoint: endpoint,
		HTTPClient: &http.Client{
			Timeout: defaultLiveTimeout,
		},
		MaxBytes: maxLiveResponseBytes,
	}
}

// NewLiveClientFromEnv constructs a client from the platform-advertised
// endpoint, falling back to DefaultLiveEndpoint when the variable is absent.
func NewLiveClientFromEnv() *LiveClient {
	return NewLiveClient(os.Getenv("FAAS_METADATA_ENV_ENDPOINT"))
}

// Fetch performs one bounded GET. It treats the endpoint's structured error
// response as ErrLiveUnavailable and never returns a partial environment.
func (c *LiveClient) Fetch(ctx context.Context) (LiveSnapshot, error) {
	if c == nil {
		return LiveSnapshot{}, fmt.Errorf("%w: client is nil", ErrLiveUnavailable)
	}
	if ctx == nil {
		return LiveSnapshot{}, fmt.Errorf("%w: context is required", ErrLiveUnavailable)
	}
	endpoint := strings.TrimSpace(c.Endpoint)
	if endpoint == "" {
		endpoint = DefaultLiveEndpoint
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return LiveSnapshot{}, fmt.Errorf("%w: build request: %w", ErrLiveUnavailable, err)
	}
	client := c.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultLiveTimeout}
	}
	response, err := client.Do(request)
	if err != nil {
		return LiveSnapshot{}, fmt.Errorf("%w: request: %w", ErrLiveUnavailable, err)
	}
	defer func() { _ = response.Body.Close() }()
	maxBytes := c.MaxBytes
	if maxBytes <= 0 {
		maxBytes = maxLiveResponseBytes
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxBytes+1))
	if err != nil {
		return LiveSnapshot{}, fmt.Errorf("%w: read response: %w", ErrLiveUnavailable, err)
	}
	if int64(len(body)) > maxBytes {
		return LiveSnapshot{}, fmt.Errorf("%w: response exceeds %d bytes", ErrLiveUnavailable, maxBytes)
	}
	var wire struct {
		Env      map[string]string `json:"env"`
		Revision string            `json:"revision"`
		Error    string            `json:"error"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return LiveSnapshot{}, fmt.Errorf("%w: decode response: %w", ErrLiveUnavailable, err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices || wire.Error != "" {
		if wire.Error == "" {
			wire.Error = response.Status
		}
		return LiveSnapshot{}, fmt.Errorf("%w: %s", ErrLiveUnavailable, wire.Error)
	}
	if wire.Env == nil {
		wire.Env = map[string]string{}
	}
	return LiveSnapshot{Env: cloneLiveEnv(wire.Env), Revision: wire.Revision}, nil
}

// LivePoller applies a changed snapshot to an opt-in application callback.
// It polls for convergence because a guest cannot subscribe directly to the
// host's Postgres notification channel. A failed fetch or Apply leaves the
// last successful revision intact and is retried on the next tick.
type LivePoller struct {
	Client   *LiveClient
	Interval time.Duration
	Apply    func(context.Context, LiveSnapshot) error
	OnError  func(error)

	mu       sync.Mutex
	seen     bool
	revision string
}

// PollOnce fetches and applies one snapshot if its revision changed. It
// returns changed=false for an already-applied revision.
func (p *LivePoller) PollOnce(ctx context.Context) (changed bool, err error) {
	if p == nil || p.Client == nil {
		return false, fmt.Errorf("%w: poller is not configured", ErrLiveUnavailable)
	}
	snapshot, err := p.Client.Fetch(ctx)
	if err != nil {
		return false, err
	}
	p.mu.Lock()
	seen := p.seen
	previous := p.revision
	p.mu.Unlock()
	if seen && snapshot.Revision == previous {
		return false, nil
	}
	if p.Apply != nil {
		if err := p.Apply(ctx, LiveSnapshot{Env: cloneLiveEnv(snapshot.Env), Revision: snapshot.Revision}); err != nil {
			return true, err
		}
	}
	p.mu.Lock()
	p.seen = true
	p.revision = snapshot.Revision
	p.mu.Unlock()
	return true, nil
}

// Run polls until ctx is cancelled. Endpoint or callback failures are
// reported through OnError and do not terminate the opt-in watcher.
func (p *LivePoller) Run(ctx context.Context) error {
	if p == nil || p.Client == nil {
		return fmt.Errorf("%w: poller is not configured", ErrLiveUnavailable)
	}
	if ctx == nil {
		return errors.New("live runtime config: context is required")
	}
	interval := p.Interval
	if interval <= 0 {
		interval = DefaultLivePollInterval
	}
	p.pollAndReport(ctx)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			p.pollAndReport(ctx)
		}
	}
}

func (p *LivePoller) pollAndReport(ctx context.Context) {
	_, err := p.PollOnce(ctx)
	if err != nil && p.OnError != nil {
		p.OnError(err)
	}
}

func cloneLiveEnv(env map[string]string) map[string]string {
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}
