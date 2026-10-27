package tracing

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// queueSize bounds spans waiting for export; a full queue drops spans
	// rather than blocking the app.
	queueSize = 2048
	// batchSize spans, or flushInterval after the first queued span, trigger
	// an export. One export a second per instance stays well inside the
	// per-account debugger rate (ADR-127).
	batchSize     = 256
	flushInterval = time.Second
	exportTimeout = 2 * time.Second
)

// exporter posts finished spans to the guest bridge as OTLP/HTTP JSON.
type exporter struct {
	endpoint string
	resource []attribute
	client   *http.Client
	queue    chan *Span
	stop     chan struct{}
	done     chan struct{}
	stopOnce sync.Once
	dropped  atomic.Int64
}

func newExporter(endpoint string, resource []attribute) *exporter {
	return &exporter{
		endpoint: endpoint,
		resource: resource,
		// The bridge is on loopback; a proxy from the app's environment
		// must not intercept it.
		client: &http.Client{Timeout: exportTimeout, Transport: &http.Transport{Proxy: nil}},
		queue:  make(chan *Span, queueSize),
		stop:   make(chan struct{}),
		done:   make(chan struct{}),
	}
}

func (e *exporter) enqueue(s *Span) {
	select {
	case e.queue <- s:
	default:
		e.dropped.Add(1)
	}
}

func (e *exporter) run() {
	defer close(e.done)
	var batch []*Span
	timer := time.NewTimer(flushInterval)
	timer.Stop()
	flush := func() {
		if len(batch) > 0 {
			e.export(batch)
			batch = nil
		}
		timer.Stop()
	}
	for {
		select {
		case s := <-e.queue:
			batch = append(batch, s)
			if len(batch) == 1 {
				timer.Reset(flushInterval)
			}
			if len(batch) >= batchSize {
				flush()
			}
		case <-timer.C:
			flush()
		case <-e.stop:
			for {
				select {
				case s := <-e.queue:
					batch = append(batch, s)
					if len(batch) >= batchSize {
						flush()
					}
				default:
					flush()
					return
				}
			}
		}
	}
}

// shutdown exports the queued spans and stops the exporter.
func (e *exporter) shutdown(ctx context.Context) error {
	e.close()
	select {
	case <-e.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (e *exporter) close() { e.stopOnce.Do(func() { close(e.stop) }) }

func (e *exporter) export(batch []*Span) {
	body, err := json.Marshal(otlpRequest(e.resource, batch))
	if err != nil {
		e.dropped.Add(int64(len(batch)))
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), exportTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.endpoint, bytes.NewReader(body))
	if err != nil {
		e.dropped.Add(int64(len(batch)))
		return
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := e.client.Do(req)
	if err != nil {
		e.dropped.Add(int64(len(batch)))
		return
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		e.dropped.Add(int64(len(batch)))
	}
}

// The OTLP/HTTP JSON shape: hex IDs, numeric enums and int64 values as
// strings (https://opentelemetry.io/docs/specs/otlp/#json-protobuf-encoding).
type otlpExport struct {
	ResourceSpans []otlpResourceSpans `json:"resourceSpans"`
}

type otlpResourceSpans struct {
	Resource   otlpResource     `json:"resource"`
	ScopeSpans []otlpScopeSpans `json:"scopeSpans"`
}

type otlpResource struct {
	Attributes []otlpKeyValue `json:"attributes"`
}

type otlpScopeSpans struct {
	Scope otlpScope  `json:"scope"`
	Spans []otlpSpan `json:"spans"`
}

type otlpScope struct {
	Name string `json:"name"`
}

type otlpSpan struct {
	TraceID           string         `json:"traceId"`
	SpanID            string         `json:"spanId"`
	ParentSpanID      string         `json:"parentSpanId,omitempty"`
	Name              string         `json:"name"`
	Kind              int            `json:"kind"`
	StartTimeUnixNano string         `json:"startTimeUnixNano"`
	EndTimeUnixNano   string         `json:"endTimeUnixNano"`
	Attributes        []otlpKeyValue `json:"attributes,omitempty"`
	Status            *otlpStatus    `json:"status,omitempty"`
}

type otlpStatus struct {
	Code    int    `json:"code"`
	Message string `json:"message,omitempty"`
}

type otlpKeyValue struct {
	Key   string    `json:"key"`
	Value otlpValue `json:"value"`
}

type otlpValue struct {
	StringValue *string `json:"stringValue,omitempty"`
	IntValue    *string `json:"intValue,omitempty"`
}

// otlpStatusError is STATUS_CODE_ERROR.
const otlpStatusError = 2

func otlpRequest(resource []attribute, spans []*Span) otlpExport {
	out := make([]otlpSpan, 0, len(spans))
	for _, s := range spans {
		s.mu.Lock()
		span := otlpSpan{
			TraceID:           hex.EncodeToString(s.ctx.trace[:]),
			SpanID:            hex.EncodeToString(s.ctx.span[:]),
			Name:              s.name,
			Kind:              int(s.kind),
			StartTimeUnixNano: strconv.FormatInt(s.start.UnixNano(), 10),
			EndTimeUnixNano:   strconv.FormatInt(s.end.UnixNano(), 10),
			Attributes:        otlpAttributes(s.attrs),
		}
		if s.parent != (spanID{}) {
			span.ParentSpanID = hex.EncodeToString(s.parent[:])
		}
		if s.errored {
			span.Status = &otlpStatus{Code: otlpStatusError, Message: s.statusMsg}
		}
		s.mu.Unlock()
		out = append(out, span)
	}
	return otlpExport{ResourceSpans: []otlpResourceSpans{{
		Resource:   otlpResource{Attributes: otlpAttributes(resource)},
		ScopeSpans: []otlpScopeSpans{{Scope: otlpScope{Name: scopeName}, Spans: out}},
	}}}
}

func otlpAttributes(attrs []attribute) []otlpKeyValue {
	out := make([]otlpKeyValue, 0, len(attrs))
	for _, a := range attrs {
		kv := otlpKeyValue{Key: a.key}
		if a.isNum {
			v := strconv.FormatInt(a.num, 10)
			kv.Value.IntValue = &v
		} else {
			v := a.str
			kv.Value.StringValue = &v
		}
		out = append(out, kv)
	}
	return out
}
