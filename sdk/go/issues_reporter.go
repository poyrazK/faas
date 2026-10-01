package faas

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	issueReporterDefaultQueue = 100
	issueReporterMaxQueue     = 1000
	issueReporterMaxFrames    = 32
	issueReporterFrameBytes   = 512
	issueReporterTypeBytes    = 256
	issueReporterMessageBytes = 2048
	issueReporterStackBytes   = 16 << 10
	issueReporterMaxPayload   = 64 << 10
)

// IssueContext contains the bounded correlation fields accepted by the issue
// ingestion endpoint. It deliberately has no arbitrary tags or customer
// identity fields.
type IssueContext struct {
	TraceID             string
	SpanID              string
	RequestID           string
	InvocationID        string
	Route               string
	SourceKind          string
	HTTPStatus          int
	FingerprintOverride string
}

// IssueReporterOptions configures a deployment-bound issue reporter. Token
// must be a short-lived g_issue_ token created for the deployment running this
// code, not an account API key.
type IssueReporterOptions struct {
	BaseURL    string
	App        string
	Token      string
	MaxQueue   int
	Timeout    time.Duration
	RetryDelay time.Duration
	HTTPClient *http.Client
}

// IssueReporterStats reports the current in-memory queue and settled delivery
// outcomes. The queue is best-effort and is lost if the process crashes.
type IssueReporterStats struct {
	Queued   int
	Accepted int64
	Dropped  int64
}

// ErrIssueReporterStopped indicates that Flush was called after the reporter's
// delivery worker stopped with events still queued.
var ErrIssueReporterStopped = errors.New("gregale issue reporter stopped with events queued")

type issueReport struct {
	body []byte
}

type issueDelivery int

const (
	issueDeliveryRetry issueDelivery = iota
	issueDeliveryAccepted
	issueDeliveryDropped
)

// IssueReporter sends bounded exception evidence to Gregale independently of
// trace sampling. Capture methods never return application errors; an empty
// event ID means the event was not queued.
type IssueReporter struct {
	endpoint   string
	token      string
	client     *http.Client
	timeout    time.Duration
	retryDelay time.Duration
	maxQueue   int

	ctx        context.Context
	cancel     context.CancelFunc
	workerDone chan struct{}

	mu       sync.Mutex
	queue    []*issueReport
	changed  chan struct{}
	closing  bool
	accepted int64
	dropped  int64
}

// NewIssueReporter creates a reporter for one Gregale app. Delivery uses a
// bounded in-memory queue, retries network errors/429/5xx with the same event
// payload, and rejects redirects so the ingest token is never forwarded to a
// different host.
func NewIssueReporter(options IssueReporterOptions) (*IssueReporter, error) {
	base, err := url.Parse(options.BaseURL)
	if err != nil || base == nil || (base.Scheme != "https" && base.Scheme != "http") || base.Host == "" || base.User != nil || base.RawQuery != "" || base.Fragment != "" || (base.Path != "" && base.Path != "/") {
		return nil, errors.New("issue base URL must be an HTTP(S) origin without credentials")
	}
	if strings.TrimSpace(options.App) == "" {
		return nil, errors.New("issue app slug is required")
	}
	if !strings.HasPrefix(options.Token, "g_issue_") {
		return nil, errors.New("a Gregale issue ingest token is required")
	}

	maxQueue := options.MaxQueue
	if maxQueue == 0 {
		maxQueue = issueReporterDefaultQueue
	}
	if maxQueue < 1 {
		maxQueue = 1
	}
	if maxQueue > issueReporterMaxQueue {
		maxQueue = issueReporterMaxQueue
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	if timeout < 100*time.Millisecond {
		timeout = 100 * time.Millisecond
	}
	if timeout > 10*time.Second {
		timeout = 10 * time.Second
	}
	retryDelay := options.RetryDelay
	if retryDelay <= 0 {
		retryDelay = time.Second
	}
	if retryDelay < 10*time.Millisecond {
		retryDelay = 10 * time.Millisecond
	}
	if retryDelay > 30*time.Second {
		retryDelay = 30 * time.Second
	}

	client := options.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	// Copy the client so the reporter does not mutate caller-owned transport
	// configuration. Ingest requests must not follow redirects with a bearer
	// credential, regardless of the caller's general redirect policy.
	clientCopy := *client
	clientCopy.Jar = nil
	clientCopy.CheckRedirect = func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}

	ctx, cancel := context.WithCancel(context.Background())
	reporter := &IssueReporter{
		endpoint:   base.Scheme + "://" + base.Host + "/v1/apps/" + url.PathEscape(options.App) + "/issue-events",
		token:      options.Token,
		client:     &clientCopy,
		timeout:    timeout,
		retryDelay: retryDelay,
		maxQueue:   maxQueue,
		ctx:        ctx,
		cancel:     cancel,
		workerDone: make(chan struct{}),
		changed:    make(chan struct{}),
	}
	go reporter.run()
	return reporter, nil
}

// CaptureException queues an exception with its current goroutine stack and
// optional correlation context. Standard Go errors do not carry creation-time
// stacks, so frames identify the reporting call site. Pass at most one context.
func (r *IssueReporter) CaptureException(err error, issueContext ...IssueContext) (eventID string) {
	if err == nil || r == nil {
		return ""
	}
	defer r.recoverCapturePanic(&eventID)
	ctx := firstIssueContext(issueContext)
	return r.capture(reflect.TypeOf(err).String(), err.Error(), ctx, 4)
}

// CapturePanic queues a panic value. Errors keep their concrete Go type;
// other values are reported with exception_type "panic".
func (r *IssueReporter) CapturePanic(value any, issueContext ...IssueContext) (eventID string) {
	if value == nil || r == nil {
		return ""
	}
	defer r.recoverCapturePanic(&eventID)
	ctx := firstIssueContext(issueContext)
	if err, ok := value.(error); ok {
		return r.capture(reflect.TypeOf(err).String(), err.Error(), ctx, 4)
	}
	return r.capture("panic", fmt.Sprint(value), ctx, 4)
}

// RecoverAndRepanic can be used directly in a defer. It captures the panic
// best-effort and then re-panics with the original value, preserving normal
// Go panic behavior.
//
//	defer issues.RecoverAndRepanic(faas.IssueContext{RequestID: requestID})
func (r *IssueReporter) RecoverAndRepanic(issueContext ...IssueContext) {
	value := recover()
	if value == nil {
		return
	}
	func() {
		defer func() { _ = recover() }()
		if r == nil {
			return
		}
		ctx := firstIssueContext(issueContext)
		if err, ok := value.(error); ok {
			r.capture(reflect.TypeOf(err).String(), err.Error(), ctx, 5)
		} else {
			r.capture("panic", fmt.Sprint(value), ctx, 5)
		}
	}()
	panic(value)
}

// Flush waits until all queued events have either been accepted or dropped by
// the server. It returns ctx.Err when delivery cannot settle before ctx ends.
func (r *IssueReporter) Flush(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			r.mu.Unlock()
			return nil
		}
		changed := r.changed
		r.mu.Unlock()

		select {
		case <-changed:
		case <-r.workerDone:
			return ErrIssueReporterStopped
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// Close stops accepting events, flushes the queue, and shuts down the worker.
// A deadline or cancellation stops retries; inspect Stats to see any events
// left queued or dropped. Repeated calls are safe.
func (r *IssueReporter) Close(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	r.closing = true
	r.signalLocked()
	r.mu.Unlock()

	err := r.Flush(ctx)
	r.cancel()
	if err == nil {
		<-r.workerDone
		return nil
	}
	select {
	case <-r.workerDone:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Stats returns a concurrency-safe snapshot of reporter delivery counters.
func (r *IssueReporter) Stats() IssueReporterStats {
	if r == nil {
		return IssueReporterStats{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return IssueReporterStats{Queued: len(r.queue), Accepted: r.accepted, Dropped: r.dropped}
}

func firstIssueContext(contexts []IssueContext) IssueContext {
	if len(contexts) == 0 {
		return IssueContext{}
	}
	return contexts[0]
}

func (r *IssueReporter) capture(exceptionType, message string, ctx IssueContext, stackSkip int) (eventID string) {
	defer func() {
		if recover() != nil {
			r.recordDrop()
			eventID = ""
		}
	}()

	r.mu.Lock()
	if r.closing || len(r.queue) >= r.maxQueue {
		r.dropped++
		r.mu.Unlock()
		return ""
	}
	r.mu.Unlock()

	sourceKind := ctx.SourceKind
	if sourceKind == "" {
		sourceKind = "exception"
	}
	if sourceKind != "exception" && sourceKind != "worker" {
		r.recordDrop()
		return ""
	}
	id, err := issueEventID()
	if err != nil {
		r.recordDrop()
		return ""
	}
	frames, stack := issueStack(stackSkip)
	event := IssueEvent{
		EventID:             id,
		OccurredAt:          time.Now().UTC(),
		ExceptionType:       boundedIssueString(exceptionType, issueReporterTypeBytes),
		Message:             boundedIssueString(message, issueReporterMessageBytes),
		StackTrace:          boundedIssueString(stack, issueReporterStackBytes),
		Frames:              frames,
		FingerprintOverride: boundedIssueString(ctx.FingerprintOverride, issueReporterTypeBytes),
		TraceID:             boundedIssueString(ctx.TraceID, 32),
		SpanID:              boundedIssueString(ctx.SpanID, 16),
		RequestID:           boundedIssueString(ctx.RequestID, issueReporterTypeBytes),
		InvocationID:        boundedIssueString(ctx.InvocationID, 36),
		Route:               boundedIssueString(ctx.Route, issueReporterTypeBytes),
		HTTPStatus:          ctx.HTTPStatus,
		SourceKind:          sourceKind,
	}
	body, err := json.Marshal(event)
	if err != nil || len(body) > issueReporterMaxPayload {
		r.recordDrop()
		return ""
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closing || len(r.queue) >= r.maxQueue {
		r.dropped++
		return ""
	}
	r.queue = append(r.queue, &issueReport{body: body})
	r.signalLocked()
	return id
}

func (r *IssueReporter) recoverCapturePanic(eventID *string) {
	if recover() != nil {
		r.recordDrop()
		*eventID = ""
	}
}

func (r *IssueReporter) run() {
	defer close(r.workerDone)
	for {
		r.mu.Lock()
		if len(r.queue) == 0 {
			if r.closing {
				r.mu.Unlock()
				return
			}
			changed := r.changed
			r.mu.Unlock()
			select {
			case <-changed:
			case <-r.ctx.Done():
				return
			}
			continue
		}
		item := r.queue[0]
		r.mu.Unlock()

		outcome := r.deliver(item)
		if outcome == issueDeliveryRetry {
			timer := time.NewTimer(r.retryDelay)
			select {
			case <-timer.C:
			case <-r.ctx.Done():
				timer.Stop()
				return
			}
			continue
		}

		r.mu.Lock()
		if len(r.queue) > 0 && r.queue[0] == item {
			r.queue = r.queue[1:]
			if outcome == issueDeliveryAccepted {
				r.accepted++
			} else {
				r.dropped++
			}
			r.signalLocked()
		}
		r.mu.Unlock()
	}
}

func (r *IssueReporter) deliver(item *issueReport) (outcome issueDelivery) {
	defer func() {
		if recover() != nil {
			// A caller-supplied RoundTripper is outside the reporter's control.
			// Keep its panic from taking down the application's process.
			outcome = issueDeliveryDropped
		}
	}()
	ctx, cancel := context.WithTimeout(r.ctx, r.timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.endpoint, bytes.NewReader(item.body))
	if err != nil {
		return issueDeliveryDropped
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+r.token)
	resp, err := r.client.Do(req)
	if err != nil {
		return issueDeliveryRetry
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
	_ = resp.Body.Close()
	if resp.StatusCode == http.StatusAccepted {
		return issueDeliveryAccepted
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		return issueDeliveryRetry
	}
	return issueDeliveryDropped
}

func (r *IssueReporter) signalLocked() {
	close(r.changed)
	r.changed = make(chan struct{})
}

func (r *IssueReporter) recordDrop() {
	r.mu.Lock()
	r.dropped++
	r.mu.Unlock()
}

func issueEventID() (string, error) {
	var value [16]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	value[6] = (value[6] & 0x0f) | 0x40
	value[8] = (value[8] & 0x3f) | 0x80
	encoded := hex.EncodeToString(value[:])
	return encoded[:8] + "-" + encoded[8:12] + "-" + encoded[12:16] + "-" + encoded[16:20] + "-" + encoded[20:], nil
}

func issueStack(skip int) ([]IssueFrame, string) {
	var pcs [64]uintptr
	// skip counts runtime.Callers itself as frame zero. The capture methods
	// supply a value that begins with their application caller.
	n := runtime.Callers(skip, pcs[:])
	frames := make([]IssueFrame, 0, issueReporterMaxFrames)
	var stack strings.Builder
	iterator := runtime.CallersFrames(pcs[:n])
	for len(frames) < issueReporterMaxFrames {
		frame, more := iterator.Next()
		if frame.Function != "" && frame.File != "" {
			file := boundedIssueString(filepath.ToSlash(frame.File), issueReporterFrameBytes)
			function := boundedIssueString(frame.Function, issueReporterFrameBytes)
			inApp := !strings.HasPrefix(file, filepath.ToSlash(runtime.GOROOT())+"/")
			frames = append(frames, IssueFrame{File: file, Function: function, Line: frame.Line, InApp: inApp})
			if stack.Len() > 0 {
				stack.WriteByte('\n')
			}
			stack.WriteString(function)
			stack.WriteString("\n\t")
			stack.WriteString(file)
			stack.WriteByte(':')
			stack.WriteString(fmt.Sprint(frame.Line))
		}
		if !more {
			break
		}
	}
	return frames, stack.String()
}

func boundedIssueString(value string, limit int) string {
	if limit <= 0 || len(value) <= limit {
		return value
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
