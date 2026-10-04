package s3gateway

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/onebox-faas/faas/pkg/state"
	"github.com/onebox-faas/faas/pkg/trace"
	"github.com/onebox-faas/faas/pkg/wire"
)

// adr: 553
func TestTransferSpoolReservationsIncludeUnwrittenSpace(t *testing.T) {
	h, _, _ := newGatewayTestHandler(t, state.ObjectBucketPermissionWrite, nil)
	h.maxSpoolBytes, h.minSpoolFree = 100, 20
	h.spoolAvailable = func() (uint64, error) { return 100, nil }
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if h.reserveSpool(30) {
				accepted.Add(1)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 2 {
		t.Fatalf("accepted %d reservations with only 80 free bytes", accepted.Load())
	}
	for range 2 {
		h.releaseSpool(30)
	}
	if !h.reserveSpool(30) {
		t.Fatal("released reservation did not restore capacity")
	}
	h.releaseSpool(30)
}

func TestTransferSocketDeadlineReleasesStalledSpool(t *testing.T) {
	var providerCalls atomic.Int32
	h, _, _ := newGatewayReceiptHandler(t, func(*http.Request) (*http.Response, error) {
		providerCalls.Add(1)
		t.Error("incomplete upload reached provider")
		return nil, nil
	})
	h.transferTimeout = time.Second
	server := httptest.NewServer(trace.HTTPHandler("transfer-test", wire.HTTPMetricsHandler(nil, "transfer", h)))
	defer server.Close()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	r := signedGatewayRequest(t, http.MethodPut, server.URL+"/assets/key", []byte("0123456789"), "UNSIGNED-PAYLOAD")
	r.RequestURI = ""
	r.Body = reader
	type reply struct {
		status int
		body   string
	}
	response := make(chan reply, 1)
	fail := make(chan error, 1)
	client := *server.Client()
	client.Timeout = 4 * time.Second
	started := time.Now()
	go func() {
		out, err := client.Do(r)
		if err != nil {
			fail <- err
			return
		}
		body, readErr := io.ReadAll(out.Body)
		closeErr := out.Body.Close()
		if err = errors.Join(readErr, closeErr); err != nil {
			fail <- err
			return
		}
		response <- reply{status: out.StatusCode, body: string(body)}
	}()
	if _, err := writer.Write([]byte("0")); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-fail:
		t.Fatal(err)
	case out := <-response:
		if out.status != http.StatusBadRequest || !strings.Contains(out.body, "IncompleteBody") {
			t.Fatal(out.status, out.body)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("socket deadline did not interrupt staging")
	}
	if time.Since(started) > 3*time.Second || providerCalls.Load() != 0 {
		t.Fatal("stalled request escaped transfer bound")
	}
	if len(h.putSlots) != 0 || h.spoolReserved != 0 {
		t.Fatal("stalled upload leaked reservation")
	}
	files, err := os.ReadDir(h.spoolDir)
	if err != nil || len(files) != 0 {
		t.Fatal("stalled upload leaked staged file", files, err)
	}
}

type delayedTransferBody struct {
	io.Reader
	waited bool
}

func (b *delayedTransferBody) Read(p []byte) (int, error) {
	if !b.waited {
		b.waited = true
		time.Sleep(600 * time.Millisecond)
	}
	return b.Reader.Read(p)
}
func (*delayedTransferBody) Close() error { return nil }

func TestTransferStagingAndForwardingShareDeadline(t *testing.T) {
	var remaining time.Duration
	h, st, _ := newGatewayReceiptHandler(t, func(r *http.Request) (*http.Response, error) {
		deadline, ok := r.Context().Deadline()
		if !ok {
			t.Fatal("provider request has no deadline")
		}
		remaining = time.Until(deadline)
		<-r.Context().Done()
		return nil, r.Context().Err()
	})
	h.transferTimeout = time.Second
	r := signedGatewayRequest(t, http.MethodPut, "https://s3.gregale.dev/assets/key", []byte("x"), "UNSIGNED-PAYLOAD")
	r.Body = &delayedTransferBody{Reader: strings.NewReader("x")}
	w := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(w, r)
	if w.Code != 503 || remaining > 600*time.Millisecond || time.Since(started) > 1400*time.Millisecond {
		t.Fatal("staging reset the transfer budget", w.Code, remaining, time.Since(started))
	}
	row, err := st.GetObjectWriteReceipt(t.Context(), st.bucket.AccountID, st.bucket.AppID, st.bucket.ID, w.Header().Get("X-Gregale-Upload-ID"))
	if err != nil || row.Status != "pending" {
		t.Fatal("timeout lost dispatched receipt", row, err)
	}
}
