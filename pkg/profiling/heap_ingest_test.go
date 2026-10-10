package profiling

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/pprof/profile"
	"github.com/onebox-faas/faas/pkg/api"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type heapCaptureBackend struct {
	captureBackend
	heapPushes int
	heap       *profile.Profile
}

func (b *heapCaptureBackend) PushHeap(_ context.Context, _ Principal, p *profile.Profile) error {
	b.heapPushes++
	b.heap = p
	return nil
}

func (*heapCaptureBackend) QueryHeap(context.Context, string, string, string, api.ProfileQuery) (*profile.Profile, error) {
	return nil, nil
}

func TestHeapProfileIngestion(t *testing.T) {
	now := time.Now().Add(-time.Second)
	raw := heapProfile(t, "inuse_space", "grow", 4096)
	from := now.Add(-500 * time.Millisecond)
	e := Envelope{Principal: principalFixture(now.Add(-time.Second)), Upload: Upload{Kind: "heap", ProcessID: "7", Profile: raw, FromUnixNano: from.UnixNano(), UntilUnixNano: now.UnixNano()}}

	backend := &heapCaptureBackend{}
	if err := NewService(backend, nil).Push(context.Background(), e); err != nil {
		t.Fatal(err)
	}
	if backend.heapPushes != 1 || backend.pushes != 0 {
		t.Fatalf("heap routed wrongly: heap=%d cpu=%d", backend.heapPushes, backend.pushes)
	}
	if backend.heap.TimeNanos != from.UnixNano() || backend.heap.SampleType[0].Type != "inuse_space" || len(backend.heap.Sample[0].Label) != 0 {
		t.Fatalf("heap snapshot not normalized: %+v", backend.heap.SampleType)
	}
	if status.Code(NewService(&captureBackend{}, nil).Push(context.Background(), e)) != codes.Unavailable {
		t.Fatal("heap accepted by a CPU-only backend")
	}
	e.Upload.Kind = "wall"
	if status.Code(NewService(backend, nil).Push(context.Background(), e)) != codes.InvalidArgument {
		t.Fatal("unknown kind accepted")
	}
	e.Upload.Kind = ""
	if status.Code(NewService(backend, nil).Push(context.Background(), e)) != codes.InvalidArgument {
		t.Fatal("heap profile accepted as CPU")
	}
}

func TestPyroscopeHeapSeries(t *testing.T) {
	var mu sync.Mutex
	var bodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, body)
		mu.Unlock()
	}))
	defer server.Close()
	backend, err := NewPyroscope(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	p, err := Parse(heapProfile(t, "space", "grow", 1024))
	if err != nil {
		t.Fatal(err)
	}
	if err := backend.PushHeap(context.Background(), principalFixture(time.Now()), p); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 1 || !bytes.Contains(bodies[0], []byte("memory")) || bytes.Contains(bodies[0], []byte("gregale_profile_coverage")) {
		t.Fatal("heap push must be one memory series without CPU coverage metadata")
	}
}
