package devbridge

import (
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/onebox-faas/faas/pkg/redact"
)

// RequestRecord deliberately excludes query strings, headers and bodies.
// Inspection lives in bounded laptop memory and disappears when the CLI exits.
type RequestRecord struct {
	ID            uint64    `json:"id"`
	StartedAt     time.Time `json:"started_at"`
	Method        string    `json:"method"`
	Path          string    `json:"path"`
	Status        int       `json:"status"`
	DurationMS    int64     `json:"duration_ms"`
	ResponseBytes int64     `json:"response_bytes"`
	Complete      bool      `json:"complete"`
	Error         string    `json:"error,omitempty"`
}

type Inspector struct {
	mu       sync.Mutex
	capacity int
	sequence uint64
	records  []RequestRecord
	redactor *redact.Redactor
}

func NewInspector(capacity, pathBytes int) *Inspector {
	if capacity < 1 {
		capacity = 1
	}
	return &Inspector{capacity: capacity, records: make([]RequestRecord, 0, capacity), redactor: redact.New(pathBytes)}
}

func (i *Inspector) Snapshot() []RequestRecord {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]RequestRecord{}, i.records...)
}

func (i *Inspector) begin(r *http.Request) uint64 {
	path, _ := i.redactor.Apply(r.URL.EscapedPath())
	i.mu.Lock()
	defer i.mu.Unlock()
	i.sequence++
	if len(i.records) == i.capacity {
		copy(i.records, i.records[1:])
		i.records = i.records[:len(i.records)-1]
	}
	method, _ := i.redactor.Apply(r.Method)
	i.records = append(i.records, RequestRecord{ID: i.sequence, StartedAt: time.Now().UTC(), Method: method, Path: path})
	return i.sequence
}

func (i *Inspector) finish(id uint64, status int, bytes int64, failed bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.records {
		if i.records[n].ID != id {
			continue
		}
		r := &i.records[n]
		r.Status, r.ResponseBytes, r.Complete = status, bytes, true
		r.DurationMS = time.Since(r.StartedAt).Milliseconds()
		if failed {
			r.Error = "local_transport_failed"
		}
		return
	}
}

func (i *Inspector) Transport(base http.RoundTripper) http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		id := i.begin(r)
		response, err := base.RoundTrip(r)
		if err != nil {
			i.finish(id, 0, 0, true)
			return nil, err
		}
		response.Body = &inspectionBody{ReadCloser: response.Body, finish: func(bytes int64, failed bool) { i.finish(id, response.StatusCode, bytes, failed) }}
		return response, nil
	})
}

type inspectionBody struct {
	io.ReadCloser
	bytes  atomic.Int64
	failed atomic.Bool
	once   sync.Once
	finish func(int64, bool)
}

func (b *inspectionBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	b.bytes.Add(int64(n))
	if err != nil && err != io.EOF {
		b.failed.Store(true)
	}
	return n, err
}
func (b *inspectionBody) Close() error {
	err := b.ReadCloser.Close()
	b.once.Do(func() { b.finish(b.bytes.Load(), b.failed.Load() || err != nil) })
	return err
}
