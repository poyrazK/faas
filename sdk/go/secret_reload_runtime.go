package faas

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// SecretSnapshot is one immutable guest envelope. Secrets returns a defensive
// copy; consumers cannot change another consumer's inputs or the ACK revision.
type SecretSnapshot struct {
	revision string
	secrets  map[string]string
}

func (s SecretSnapshot) Revision() string           { return s.revision }
func (s SecretSnapshot) Secrets() map[string]string { return maps.Clone(s.secrets) }
func (SecretSnapshot) String() string               { return "SecretSnapshot{redacted}" }
func (SecretSnapshot) GoString() string             { return "SecretSnapshot{redacted}" }
func (SecretSnapshot) MarshalJSON() ([]byte, error) { return []byte(`"SecretSnapshot{redacted}"`), nil }

// SecretConsumer must synchronously/idempotently invalidate new acquisition and
// fence late publication. Apply must honor ctx before publishing resources.
// Applications own acquired/retired clients and register every secret user.
type SecretConsumer struct {
	Apply      func(context.Context, SecretSnapshot) error
	Invalidate func()
}

// SecretReloadIO adapters must honor context deadlines; nil functions use the
// local guest file contract and a dedicated HTTP client without API wrappers.
type SecretReloadIO struct {
	Read  func(context.Context, string) ([]byte, error)
	Ready func(context.Context, string) error
	Post  func(context.Context, string, []byte) (int, error)
	Sleep func(context.Context, time.Duration) error
}

type SecretReloadOptions struct {
	Consumers      []SecretConsumer
	Environment    map[string]string // nil captures the four required process settings
	StartupTimeout time.Duration     // zero defaults to 30s; only bounds waiting for an initial version
	OnError        func(error)       // signal-triggered failures receive sanitized errors
	IO             SecretReloadIO
}

type secretReloadRequest struct {
	ctx    context.Context
	result chan secretReloadResult
}

type secretReloadResult struct {
	revision string
	err      error
}

// SecretReloadCoordinator installs SIGHUP before readiness and serializes all
// application callbacks. Start's context owns the workload lifetime. Cancelling
// an admitted Reload is terminal; Stop disables consumers without waiting for
// arbitrary callbacks. The application owns graceful process/server shutdown.
type SecretReloadCoordinator struct {
	mu                 sync.Mutex
	started, stopped   bool
	ctx                context.Context
	cancel             context.CancelFunc
	stopSignals        func()
	stopOnce           sync.Once
	initialDone        chan struct{}
	initialResult      secretReloadResult
	workerDone         chan struct{}
	requests           chan secretReloadRequest
	consumers          []SecretConsumer
	snapshot, ready    string
	endpoint, identity string
	startupTimeout     time.Duration
	onError            func(error)
	io                 SecretReloadIO
}

func secretReloadError(message string) error { return errors.New("faas: " + message) }

var secretRevisionPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
var secretGenerationPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

func NewSecretReloadCoordinator(options SecretReloadOptions) (*SecretReloadCoordinator, error) {
	if len(options.Consumers) == 0 {
		return nil, secretReloadError("secret consumers are unavailable")
	}
	for _, consumer := range options.Consumers {
		if consumer.Apply == nil || consumer.Invalidate == nil {
			return nil, secretReloadError("secret consumers are unavailable")
		}
	}
	get := os.Getenv
	if options.Environment != nil {
		get = func(key string) string { return options.Environment[key] }
	}
	snapshot, ready := get("FAAS_SECRETS_SNAPSHOT_FILE"), get("FAAS_SECRETS_RELOAD_READY_FILE")
	for _, path := range []string{snapshot, ready} {
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\x00\r\n") {
			return nil, secretReloadError("atomic secret reload paths are unavailable")
		}
	}
	endpoint, identity := get("FAAS_SECRETS_RELOAD_ACK_ENDPOINT"), get("FAAS_SECRETS_RELOAD_GENERATION")
	u, err := url.Parse(endpoint)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" || strings.ContainsAny(endpoint, "\x00\r\n") {
		return nil, secretReloadError("secret acknowledgement endpoint is unavailable")
	}
	if !secretGenerationPattern.MatchString(identity) {
		return nil, secretReloadError("secret execution identity is unavailable")
	}
	timeout := options.StartupTimeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	if timeout < 0 {
		return nil, secretReloadError("initial secret wait deadline is invalid")
	}
	return &SecretReloadCoordinator{consumers: append([]SecretConsumer(nil), options.Consumers...),
		snapshot: snapshot, ready: ready, endpoint: endpoint, identity: identity, startupTimeout: timeout,
		onError: options.OnError, io: completeSecretReloadIO(options.IO), initialDone: make(chan struct{}),
		workerDone: make(chan struct{}), requests: make(chan secretReloadRequest)}, nil
}

func (c *SecretReloadCoordinator) Start(ctx context.Context) (string, error) {
	c.mu.Lock()
	if c.stopped || ctx.Err() != nil {
		c.mu.Unlock()
		c.Stop()
		return "", secretReloadError("secret reload cancelled")
	}
	if !c.started {
		hangups, stop, err := registerSecretHangups()
		if err != nil {
			c.mu.Unlock()
			c.Stop()
			return "", err
		}
		c.ctx, c.cancel = context.WithCancel(ctx)
		c.started, c.stopSignals = true, stop
		go c.run(hangups)
		go func() { <-c.ctx.Done(); c.Stop() }()
	}
	running := c.ctx
	c.mu.Unlock()
	select {
	case <-c.initialDone:
		if running.Err() != nil || ctx.Err() != nil {
			c.Stop()
			return "", secretReloadError("secret reload cancelled")
		}
		return c.initialResult.revision, c.initialResult.err
	case <-running.Done():
		c.Stop()
		return "", secretReloadError("secret reload cancelled")
	case <-ctx.Done():
		c.Stop()
		return "", secretReloadError("secret reload cancelled")
	}
}

func (c *SecretReloadCoordinator) Reload(ctx context.Context) (string, error) {
	c.mu.Lock()
	if !c.started || c.stopped {
		c.mu.Unlock()
		return "", secretReloadError("secret reload is not running")
	}
	running := c.ctx
	c.mu.Unlock()
	if ctx.Err() != nil {
		return "", secretReloadError("secret reload cancelled")
	}
	operation, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(running, cancel)
	defer func() { stop(); cancel() }()
	request := secretReloadRequest{ctx: operation, result: make(chan secretReloadResult, 1)}
	select {
	case c.requests <- request:
	case <-operation.Done():
		return "", secretReloadError("secret reload cancelled")
	}
	select {
	case result := <-request.result:
		if operation.Err() == nil && running.Err() == nil {
			return result.revision, result.err
		}
	case <-operation.Done():
	}
	c.Stop() // An admitted cancellation cannot leave partial or late adoption active.
	return "", secretReloadError("secret reload cancelled")
}

func (c *SecretReloadCoordinator) Stop() {
	c.stopOnce.Do(func() {
		c.mu.Lock()
		c.stopped = true
		cancel, stop := c.cancel, c.stopSignals
		c.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		if stop != nil {
			stop()
		}
		c.disable()
	})
}

func (c *SecretReloadCoordinator) run(hangups <-chan os.Signal) {
	defer close(c.workerDone)
	initial := c.refresh(c.ctx, true)
	if initial.err != nil {
		c.Stop()
	}
	c.initialResult = initial
	close(c.initialDone)
	for {
		select {
		case <-c.ctx.Done():
			return
		case request := <-c.requests:
			if c.ctx.Err() != nil {
				request.result <- secretReloadResult{err: secretReloadError("secret reload cancelled")}
				return
			}
			request.result <- c.refresh(request.ctx, false)
		case <-hangups:
			result := c.refresh(c.ctx, false)
			if result.err != nil && c.ctx.Err() == nil && c.onError != nil {
				func() {
					defer func() { _ = recover() }()
					c.onError(secretReloadError("secret reload could not be completed"))
				}()
			}
		}
	}
}

func (c *SecretReloadCoordinator) disable() bool {
	valid := true
	for _, consumer := range c.consumers {
		func() {
			defer func() {
				if recover() != nil {
					valid = false
				}
			}()
			consumer.Invalidate()
		}()
	}
	return valid
}

func (c *SecretReloadCoordinator) refresh(ctx context.Context, initial bool) (result secretReloadResult) {
	defer func() {
		if recover() != nil {
			result = secretReloadResult{err: secretReloadError("secret reload could not be completed")}
		}
		if ctx.Err() != nil && result.err == nil {
			result.err = secretReloadError("secret reload cancelled")
		}
		if result.err != nil {
			c.disable()
			result.err = secretReloadError("secret reload could not be completed")
		}
	}()
	if ctx.Err() != nil {
		return secretReloadResult{err: secretReloadError("secret reload cancelled")}
	}
	if !c.disable() {
		return secretReloadResult{err: secretReloadError("secret consumers could not be disabled")}
	}
	if initial {
		if err := c.io.Ready(ctx, c.ready); err != nil {
			return secretReloadResult{err: err}
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		if !c.disable() {
			return secretReloadResult{err: secretReloadError("secret consumers could not be disabled")}
		}
		snapshot, err := c.readSnapshot(ctx, initial && attempt == 0)
		if err != nil {
			return secretReloadResult{err: err}
		}
		status := "applied"
		for _, consumer := range c.consumers {
			if err := applySecretConsumer(ctx, consumer, snapshot); err != nil {
				c.disable()
				status = "failed"
				break
			}
		}
		if ctx.Err() != nil {
			return secretReloadResult{err: secretReloadError("secret reload cancelled")}
		}
		stale, err := c.ack(ctx, snapshot.revision, status)
		if err != nil || (!stale && status == "failed") {
			return secretReloadResult{err: secretReloadError("secret snapshot could not be applied")}
		}
		if !stale {
			return secretReloadResult{revision: snapshot.revision}
		}
	}
	return secretReloadResult{err: secretReloadError("secret projection changed too many times")}
}

func applySecretConsumer(ctx context.Context, consumer SecretConsumer, snapshot SecretSnapshot) (err error) {
	defer func() {
		if recover() != nil {
			err = secretReloadError("secret snapshot could not be applied")
		}
	}()
	if ctx.Err() != nil {
		return secretReloadError("secret reload cancelled")
	}
	err = consumer.Apply(ctx, snapshot)
	if ctx.Err() != nil {
		return secretReloadError("secret reload cancelled")
	}
	return err
}

func (c *SecretReloadCoordinator) readSnapshot(ctx context.Context, initial bool) (SecretSnapshot, error) {
	if initial {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, c.startupTimeout)
		defer cancel()
	}
	backoff := 100 * time.Millisecond
	for {
		snapshot, err := c.readEnvelope(ctx, initial)
		if err != nil || snapshot.revision != "" {
			return snapshot, err
		}
		if err := c.io.Sleep(ctx, backoff); err != nil {
			return SecretSnapshot{}, err
		}
		backoff = min(2*time.Second, backoff*2)
	}
}

func (c *SecretReloadCoordinator) readEnvelope(ctx context.Context, pending bool) (SecretSnapshot, error) {
	for attempt := 0; attempt < 5; attempt++ {
		if ctx.Err() != nil {
			return SecretSnapshot{}, secretReloadError("secret reload cancelled")
		}
		raw, err := c.io.Read(ctx, c.snapshot)
		if ctx.Err() != nil {
			return SecretSnapshot{}, secretReloadError("secret reload cancelled")
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) && attempt < 4 {
				continue
			}
			return SecretSnapshot{}, secretReloadError("secret snapshot could not be read")
		}
		return parseSecretEnvelope(raw, pending)
	}
	return SecretSnapshot{}, secretReloadError("secret snapshot could not be read")
}

func parseSecretEnvelope(raw []byte, pending bool) (SecretSnapshot, error) {
	var envelope map[string]json.RawMessage
	var revision string
	var entries map[string]json.RawMessage
	invalid := secretReloadError("secret snapshot is invalid")
	if json.Unmarshal(raw, &envelope) != nil || envelope == nil || bytes.Equal(bytes.TrimSpace(envelope["revision"]), []byte("null")) || json.Unmarshal(envelope["revision"], &revision) != nil ||
		(!(pending && revision == "") && !secretRevisionPattern.MatchString(revision)) ||
		json.Unmarshal(envelope["secrets"], &entries) != nil || entries == nil {
		return SecretSnapshot{}, invalid
	}
	secrets := make(map[string]string, len(entries))
	for key, rawValue := range entries {
		var value string
		if key == "" || bytes.Equal(bytes.TrimSpace(rawValue), []byte("null")) || json.Unmarshal(rawValue, &value) != nil {
			return SecretSnapshot{}, invalid
		}
		secrets[key] = value
	}
	return SecretSnapshot{revision: revision, secrets: secrets}, nil
}

func (c *SecretReloadCoordinator) ack(ctx context.Context, revision, status string) (bool, error) {
	body, _ := json.Marshal(struct {
		Revision   string `json:"revision"`
		Status     string `json:"status"`
		Generation string `json:"generation"`
	}{revision, status, c.identity})
	for attempt := 0; attempt < 10; attempt++ {
		if ctx.Err() != nil {
			break
		}
		request, cancel := context.WithTimeout(ctx, 5*time.Second)
		code, err := c.io.Post(request, c.endpoint, bytes.Clone(body))
		if request.Err() != nil {
			err = request.Err()
		}
		cancel()
		if ctx.Err() != nil {
			break
		}
		if err == nil {
			switch code {
			case http.StatusAccepted:
				return false, nil
			case http.StatusConflict:
				return true, nil
			case http.StatusServiceUnavailable:
			default:
				return false, secretReloadError("secret acknowledgement could not be confirmed")
			}
		}
		if attempt == 9 {
			break
		}
		if err := c.io.Sleep(ctx, min(2*time.Second, 100*time.Millisecond*time.Duration(1<<attempt))); err != nil {
			break
		}
	}
	return false, secretReloadError("secret acknowledgement could not be confirmed")
}

func completeSecretReloadIO(io SecretReloadIO) SecretReloadIO {
	if io.Read == nil {
		io.Read = func(ctx context.Context, path string) ([]byte, error) {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			return os.ReadFile(path)
		}
	}
	if io.Ready == nil {
		io.Ready = func(ctx context.Context, path string) error {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return os.WriteFile(path, []byte("ready\n"), 0o600)
		}
	}
	if io.Sleep == nil {
		io.Sleep = func(ctx context.Context, wait time.Duration) error {
			timer := time.NewTimer(wait)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-timer.C:
				return nil
			}
		}
	}
	if io.Post == nil {
		client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
			DialContext: (&net.Dialer{Timeout: 5 * time.Second}).DialContext, DisableKeepAlives: true, TLSHandshakeTimeout: 5 * time.Second,
		}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		io.Post = func(ctx context.Context, endpoint string, body []byte) (int, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
			if err != nil {
				return 0, err
			}
			request.Header.Set("Content-Type", "application/json")
			response, err := client.Do(request)
			if err != nil {
				return 0, err
			}
			defer response.Body.Close()
			return response.StatusCode, nil
		}
	}
	return io
}
