package apidserver

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/statusmirror"
)

type recordingMirror struct {
	mu   sync.Mutex
	puts map[string][]byte
	fail bool
	sent chan struct{}
}

func (m *recordingMirror) Get(context.Context, string) (statusmirror.Object, error) {
	return statusmirror.Object{}, statusmirror.ErrNotFound
}

func (m *recordingMirror) Put(_ context.Context, key string, body []byte, _, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	defer func() {
		select {
		case m.sent <- struct{}{}:
		default:
		}
	}()
	if m.fail {
		return errors.New("r2 unavailable")
	}
	m.puts[key] = append([]byte(nil), body...)
	return nil
}

// The mirror must publish the same document /v1/status serves, or the
// off-platform page and the API could disagree during an incident.
func TestStatusMirrorPublishesTheV1StatusDocument(t *testing.T) {
	prom := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Query().Get("query"), "ALERTS") {
			_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"status":"success","data":{"resultType":"vector","result":[{"metric":{},"value":[1700000000,"99.9"]}]}}`))
	}))
	t.Cleanup(prom.Close)
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache(prom.URL, "")
	mirror := &recordingMirror{puts: map[string][]byte{}, sent: make(chan struct{}, 1)}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { srv.runStatusMirror(ctx, mirror, "apid-test", time.Hour); close(done) }()
	select {
	case <-mirror.sent:
	case <-time.After(10 * time.Second):
		t.Fatal("mirror did not publish on start")
	}
	cancel()
	<-done

	var doc statusmirror.StatusDocument
	if err := json.Unmarshal(mirror.puts[statusmirror.StatusKey], &doc); err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	srv.publicStatusOverviewHandler(rec, httptest.NewRequest(http.MethodGet, "/v1/status", nil))
	served, err := json.Marshal(json.RawMessage(rec.Body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	mirrored, err := json.Marshal(doc.Status)
	if err != nil {
		t.Fatal(err)
	}
	if string(served) != string(mirrored) {
		t.Fatalf("mirror document differs from /v1/status\nserved:   %s\nmirrored: %s", served, mirrored)
	}
	if doc.Publisher != "apid-test" || doc.Schema != statusmirror.SchemaVersion || doc.PublishedAt.IsZero() {
		t.Fatalf("envelope = %+v", doc)
	}
}

func TestStatusMirrorFailureDoesNotStopTheLoop(t *testing.T) {
	srv := newServer(state.NewMemStore(), slog.Default(), "gregale.dev", nil).WithStatusCache("", "")
	mirror := &recordingMirror{puts: map[string][]byte{}, fail: true, sent: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { srv.runStatusMirror(ctx, mirror, "apid-test", 10*time.Millisecond); close(done) }()
	for i := 0; i < 3; i++ {
		select {
		case <-mirror.sent:
		case <-time.After(10 * time.Second):
			t.Fatalf("push %d never attempted after a failure", i+1)
		}
	}
	cancel()
	<-done
}
