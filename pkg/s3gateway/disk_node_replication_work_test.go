package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/objectnode"
)

type blockedReplicationResponse struct {
	header  http.Header
	started chan struct{}
	release <-chan struct{}
	once    sync.Once
	fail    bool
}

func (w *blockedReplicationResponse) Header() http.Header { return w.header }
func (*blockedReplicationResponse) WriteHeader(int)       {}
func (w *blockedReplicationResponse) Write(p []byte) (int, error) {
	w.once.Do(func() { close(w.started) })
	<-w.release
	if w.fail {
		return 0, io.ErrClosedPipe
	}
	return len(p), nil
}

func TestDiskReplicationTransfersAreBoundedAndReleaseOnWriteFailure(t *testing.T) {
	store, h, _, _, source := replicaDiskFixture(t)
	o, err := store.Put(t.Context(), "test-bucket", "key", strings.NewReader("data"), 4, objectnode.PutOptions{})
	if err != nil {
		t.Fatal(err)
	}
	head, err := source.Head(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	query := url.Values{objectnode.ReplicationQuery: {"blob"}, "stream": {head.StreamID}, "sha256": {o.SHA256}, "offset": {"0"}, "length": {"4"}}
	path := "/?" + query.Encode()
	release := make(chan struct{})
	var released sync.Once
	resume := func() { released.Do(func() { close(release) }) }
	defer resume()
	var workers sync.WaitGroup
	for range api.ObjectNodeReplicationMaxTransfers {
		response := &blockedReplicationResponse{header: make(http.Header), started: make(chan struct{}), release: release, fail: true}
		request := signedInventoryRequest(t, http.MethodGet, path)
		workers.Go(func() { h.ServeHTTP(response, request) })
		select {
		case <-response.started:
		case <-time.After(5 * time.Second):
			t.Fatal("authenticated transfer never started")
		}
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, signedInventoryRequest(t, http.MethodGet, path))
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("excess bulk work was admitted", w.Code, w.Body.String())
	}
	// Status/confirmation traffic retains its independent route while payload
	// responses are blocked, so a saturated bulk pool cannot block all progress.
	if _, err = source.Head(t.Context()); err != nil {
		t.Fatal(err)
	}
	resume()
	workers.Wait()
	if len(h.metrics.replication.transfers) != 0 {
		t.Fatal("failed payload writes leaked transfer slots")
	}
	body, err := source.Blob(t.Context(), head.StreamID, o.SHA256, 0, 4)
	if err != nil {
		t.Fatal("transfer pool did not recover", err)
	}
	data, err := io.ReadAll(body)
	_ = body.Close()
	if err != nil || string(data) != "data" {
		t.Fatal(string(data), err)
	}
	metrics := httptest.NewRecorder()
	h.MetricsHandler().ServeHTTP(metrics, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	for _, name := range []string{"faas_object_node_replication_transfers 0", "faas_object_node_replication_transfer_rejections_total 1"} {
		if !strings.Contains(metrics.Body.String(), name) {
			t.Fatal("missing transfer observation", name)
		}
	}
	if _, err = source.Blob(t.Context(), head.StreamID, o.SHA256, 99, 1); !errors.Is(err, objectnode.ErrInvalid) {
		t.Fatal("bad range escaped validation", err)
	}
}
