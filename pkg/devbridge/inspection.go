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
// Inspection lives in bounded process memory and disappears on exit/restart.
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
	// Upgrade is "websocket" for an upgraded connection (ADR-742). Its
	// RequestBytes/ResponseBytes count stream bytes in each direction and
	// DurationMS covers the whole connection; frames are never recorded.
	Upgrade      string `json:"upgrade,omitempty"`
	RequestBytes int64  `json:"request_bytes,omitempty"`
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

func (i *Inspector) beginUpgrade(r *http.Request) uint64 {
	id := i.begin(r)
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.records {
		if i.records[n].ID == id {
			i.records[n].Upgrade = upgradeWebSocket
		}
	}
	return id
}

func (i *Inspector) finishUpgrade(id uint64, status int, sent, received int64, failed bool) {
	i.finish(id, status, received, failed)
	i.mu.Lock()
	defer i.mu.Unlock()
	for n := range i.records {
		if i.records[n].ID == id {
			i.records[n].RequestBytes = sent
		}
	}
}

func (i *Inspector) Transport(base http.RoundTripper) http.RoundTripper {
	return i.transport(base, func(r *http.Request) *http.Request { return r })
}

func (i *Inspector) transport(base http.RoundTripper, view func(*http.Request) *http.Request) http.RoundTripper {
	return roundTripperFunc(func(r *http.Request) (*http.Response, error) {
		upgrade := IsWebSocketUpgrade(r)
		var id uint64
		if upgrade {
			id = i.beginUpgrade(view(r))
		} else {
			id = i.begin(view(r))
		}
		response, err := base.RoundTrip(r)
		if err != nil {
			i.finish(id, 0, 0, true)
			return nil, err
		}
		if body, ok := response.Body.(io.ReadWriteCloser); ok && response.StatusCode == http.StatusSwitchingProtocols {
			// httputil.ReverseProxy needs a writable body to switch protocols.
			response.Body = &inspectionUpgrade{ReadWriteCloser: body, finish: func(sent, received int64, failed bool) {
				i.finishUpgrade(id, http.StatusSwitchingProtocols, sent, received, failed)
			}}
			return response, nil
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

type inspectionUpgrade struct {
	io.ReadWriteCloser
	read, wrote atomic.Int64
	once        sync.Once
	finish      func(sent, received int64, failed bool)
}

func (u *inspectionUpgrade) Read(p []byte) (int, error) {
	n, err := u.ReadWriteCloser.Read(p)
	u.read.Add(int64(n))
	return n, err
}

func (u *inspectionUpgrade) Write(p []byte) (int, error) {
	n, err := u.ReadWriteCloser.Write(p)
	u.wrote.Add(int64(n))
	return n, err
}

func (u *inspectionUpgrade) Close() error {
	err := u.ReadWriteCloser.Close()
	u.once.Do(func() { u.finish(u.wrote.Load(), u.read.Load(), false) })
	return err
}
