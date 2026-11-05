package preview

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type leasedStorageClient struct {
	key    string
	closed atomic.Int32
	mu     sync.Mutex
	body   io.ReadCloser
}

func (c *leasedStorageClient) close() error {
	c.closed.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.body != nil {
		return c.body.Close()
	}
	return nil
}

func leaseBinding(t *testing.T, retire func(*leasedStorageClient) error) *ObjectStorageBinding[*leasedStorageClient] {
	t.Helper()
	b, err := NewObjectStorageBinding("ASSETS", func(_ context.Context, config ObjectStorageClientConfig) (*leasedStorageClient, error) {
		return &leasedStorageClient{key: config.SecretAccessKey}, nil
	}, ObjectStorageBindingOptions[*leasedStorageClient]{Retire: retire})
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func acquireStorage(t *testing.T, b *ObjectStorageBinding[*leasedStorageClient]) (*ObjectStorageLease[*leasedStorageClient], *leasedStorageClient) {
	t.Helper()
	l, err := b.Acquire()
	if err != nil {
		t.Fatal(err)
	}
	c, bucket, err := l.Current()
	if err != nil || bucket != "assets" {
		t.Fatalf("lease pair: %s %v", bucket, err)
	}
	return l, c
}

func drainStorage(t *testing.T, b *ObjectStorageBinding[*leasedStorageClient]) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := b.WaitForRetirement(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestObjectStorageLeaseRotationAndConcurrentCopiedRelease(t *testing.T) {
	b := leaseBinding(t, (*leasedStorageClient).close)
	if _, err := b.Acquire(); err == nil {
		t.Fatal("uninitialized acquisition")
	}
	if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := b.Current(); err == nil {
		t.Fatal("untracked managed acquisition allowed")
	}
	first, old := acquireStorage(t, b)
	second, same := acquireStorage(t, b)
	if old != same {
		t.Fatal("leases split active client")
	}
	raw, _ := json.Marshal(first)
	if strings.Contains(string(raw)+fmt.Sprintf("%v %#v", first, first), "private-") {
		t.Fatal("lease logging exposed client")
	}
	values := runtimeStorageValues()
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-rotated"
	if err := b.Reload(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	current, next := acquireStorage(t, b)
	if next == old || next.key != "private-rotated" {
		t.Fatal("wrong rotation")
	}
	first.Release()
	if old.closed.Load() != 0 {
		t.Fatal("retired before last borrower")
	}
	copied := *second
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() { defer wg.Done(); copied.Release(); second.Release() }()
	}
	wg.Wait()
	drainStorage(t, b) // ignores the still-active new client
	if old.closed.Load() != 1 || next.closed.Load() != 0 {
		t.Fatal("wrong retirement count")
	}
	if _, _, err := second.Current(); err == nil {
		t.Fatal("released lease reused")
	}
	b.Invalidate()
	if _, err := b.Acquire(); err == nil {
		t.Fatal("revoked binding acquired")
	}
	if next.closed.Load() != 0 {
		t.Fatal("revocation closed borrowed client")
	}
	current.Release()
	drainStorage(t, b)
	if next.closed.Load() != 1 {
		t.Fatal("shutdown did not retire client once")
	}
	var empty ObjectStorageLease[*leasedStorageClient]
	empty.Release()
	if _, _, err := empty.Current(); err == nil {
		t.Fatal("empty lease ready")
	}
}

func TestObjectStorageLeaseDrainCancellationAndSlowRetirement(t *testing.T) {
	entered, finish := make(chan struct{}), make(chan struct{})
	var once sync.Once
	defer once.Do(func() { close(finish) })
	b := leaseBinding(t, func(c *leasedStorageClient) error {
		if c.key == "private-secret" {
			close(entered)
			<-finish
		}
		return c.close()
	})
	if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	lease, old := acquireStorage(t, b)
	b.Invalidate()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := b.WaitForRetirement(ctx); err == nil {
		t.Fatal("cancelled drain succeeded")
	}
	if old.closed.Load() != 0 {
		t.Fatal("drain cancellation destroyed lease")
	}
	lease.Release()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("cleanup never began")
	}
	values := runtimeStorageValues()
	values["ASSETS_SECRET_ACCESS_KEY"] = "private-new"
	if err := b.Reload(context.Background(), values); err != nil {
		t.Fatal(err)
	}
	next, _ := acquireStorage(t, b)
	next.Release()
	b.Invalidate() // a held callback must not hold the state lock
	ctx, cancel = context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if err := b.WaitForRetirement(ctx); err == nil {
		t.Fatal("drain ignored callback")
	}
	once.Do(func() { close(finish) })
	drainStorage(t, b)
	if old.closed.Load() != 1 {
		t.Fatal("held cleanup did not finish")
	}
}

func TestObjectStorageLeaseLateCandidateAndFactoryErrorCleanup(t *testing.T) {
	for _, failure := range []string{"invalidate", "cancel", "factory-error"} {
		t.Run(failure, func(t *testing.T) {
			entered, finish := make(chan struct{}), make(chan struct{})
			var once sync.Once
			defer once.Do(func() { close(finish) })
			candidate := &leasedStorageClient{key: "private-candidate"}
			var calls atomic.Int32
			b, _ := NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (*leasedStorageClient, error) {
				calls.Add(1)
				close(entered)
				<-finish
				if failure == "factory-error" {
					return candidate, errors.New("private-driver")
				}
				return candidate, nil
			}, ObjectStorageBindingOptions[*leasedStorageClient]{Retire: (*leasedStorageClient).close})
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			result := make(chan error, 1)
			go func() { result <- b.Reload(ctx, runtimeStorageValues()) }()
			select {
			case <-entered:
			case <-time.After(3 * time.Second):
				t.Fatal("factory never started")
			}
			if failure == "cancel" {
				cancel()
			} else if failure == "invalidate" {
				b.Invalidate()
			}
			waitCtx, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
			if err := b.WaitForRetirement(waitCtx); err == nil {
				t.Fatal("drain ignored admitted factory")
			}
			stop()
			once.Do(func() { close(finish) })
			select {
			case err := <-result:
				if err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatal("unsafe candidate result")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("reload never completed")
			}
			drainStorage(t, b)
			if calls.Load() != 1 || candidate.closed.Load() != 1 {
				t.Fatal("rejected candidate leaked or double closed")
			}
			if _, err := b.Acquire(); err == nil {
				t.Fatal("unpublished candidate acquired")
			}
		})
	}
}

func TestObjectStorageLeaseRetirementFailureIsSanitizedAndSticky(t *testing.T) {
	for _, panicHook := range []bool{false, true} {
		t.Run(fmt.Sprint(panicHook), func(t *testing.T) {
			observed := make(chan error, 1)
			var count atomic.Int32
			b, _ := NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (*leasedStorageClient, error) {
				return &leasedStorageClient{}, nil
			}, ObjectStorageBindingOptions[*leasedStorageClient]{Retire: func(*leasedStorageClient) error {
				count.Add(1)
				if panicHook {
					panic("private-driver")
				}
				return errors.New("private-driver")
			}, OnRetireError: func(err error) { observed <- err; panic("private-observer") }})
			if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
				t.Fatal(err)
			}
			b.Invalidate()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			for range 2 {
				if err := b.WaitForRetirement(ctx); err == nil || strings.Contains(err.Error(), "private-") {
					t.Fatal("unsafe/missing retirement failure")
				}
			}
			select {
			case err := <-observed:
				if strings.Contains(err.Error(), "private-") {
					t.Fatal("unsafe observer")
				}
			case <-ctx.Done():
				t.Fatal("observer never called")
			}
			if count.Load() != 1 {
				t.Fatal("uncertain cleanup retried")
			}
		})
	}
}

func TestObjectStorageLeaseRejectsTrackedClientAlias(t *testing.T) {
	client := &leasedStorageClient{}
	b, _ := NewObjectStorageBinding("ASSETS", func(context.Context, ObjectStorageClientConfig) (*leasedStorageClient, error) { return client, nil },
		ObjectStorageBindingOptions[*leasedStorageClient]{Retire: (*leasedStorageClient).close})
	if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	l, _ := acquireStorage(t, b)
	if err := b.Reload(context.Background(), runtimeStorageValues()); err == nil {
		t.Fatal("shared factory client accepted")
	}
	if client.closed.Load() != 0 {
		t.Fatal("rejected alias closed outstanding lease")
	}
	l.Release()
	drainStorage(t, b)
	if client.closed.Load() != 1 {
		t.Fatal("alias retired twice")
	}
}

func TestObjectStorageLeaseHTTPBodySurvivesRotationAndRevocation(t *testing.T) {
	finish := make(chan struct{})
	var once sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "first-")
		w.(http.Flusher).Flush()
		select {
		case <-finish:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "last")
	}))
	defer server.Close()
	defer once.Do(func() { close(finish) })
	b := leaseBinding(t, (*leasedStorageClient).close)
	if err := b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	l, client := acquireStorage(t, b)
	defer l.Release()
	httpClient := &http.Client{Timeout: 3 * time.Second}
	response, err := httpClient.Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	client.mu.Lock()
	client.body = response.Body
	client.mu.Unlock()
	prefix := make([]byte, 6)
	if _, err = io.ReadFull(response.Body, prefix); err != nil || string(prefix) != "first-" {
		t.Fatal("stream did not begin", err)
	}
	if err = b.Reload(context.Background(), runtimeStorageValues()); err != nil {
		t.Fatal(err)
	}
	b.Invalidate()
	if client.closed.Load() != 0 {
		t.Fatal("live HTTP stream closed on rotation")
	}
	once.Do(func() { close(finish) })
	tail, err := io.ReadAll(response.Body)
	if err != nil || string(tail) != "last" {
		t.Fatal("stream interrupted", err)
	}
	if err = response.Body.Close(); err != nil {
		t.Fatal(err)
	}
	l.Release()
	drainStorage(t, b)
	if client.closed.Load() != 1 {
		t.Fatal("stream client leaked")
	}
}
