package e2e_test

import (
	"context"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/e2etest"
)

// TestE2E_NormalPath_CancelledUploadDoesNotOpenBridge proves that incomplete
// uploads remain in the upload-admission phase. A client disconnect must stop
// the request without acquiring VM capacity or opening ForwardHTTPStream.
func TestE2E_NormalPath_CancelledUploadDoesNotOpenBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-cancel-upload")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "cancel-upload")
	f.vmmd.SetVersion(instance.ID, "cancel-upload")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:cancel-upload\n", 10*time.Second)
	probe := f.vmmd.InstallCancellationProbe(instance.ID, false, false)

	requestCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	bodyReader, bodyWriter := io.Pipe()
	defer func() { _ = bodyWriter.Close() }()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, f.h.EdgeURL()+"/cancel-upload", bodyReader)
	if err != nil {
		t.Fatalf("new cancel upload request: %v", err)
	}
	req.Host = f.host
	req.Header.Set("Content-Type", "application/octet-stream")

	client := *f.h.HTTPClient()
	requestDone := make(chan error, 1)
	go func() {
		resp, err := client.Do(req)
		if resp != nil {
			_ = resp.Body.Close()
		}
		requestDone <- err
	}()

	writeDone := make(chan error, 1)
	go func() {
		_, writeErr := bodyWriter.Write([]byte(strings.Repeat("x", 16*1024)))
		writeDone <- writeErr
	}()
	select {
	case writeErr := <-writeDone:
		if writeErr != nil {
			t.Fatalf("write partial upload: %v", writeErr)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("client upload did not deliver the first body chunk")
	}

	cancel()
	_ = bodyWriter.CloseWithError(context.Canceled)
	select {
	case requestErr := <-requestDone:
		if requestErr == nil {
			t.Fatal("cancelled upload unexpectedly returned an HTTP response")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancelled upload did not terminate the client request")
	}
	select {
	case <-probe.InitSeen():
		t.Fatal("cancelled incomplete upload opened the VMMD bridge")
	case <-time.After(250 * time.Millisecond):
	}
	for _, capture := range f.vmmd.Requests() {
		if capture.Init.GetRequestUri() == "/cancel-upload" {
			t.Fatal("cancelled incomplete upload reached VMMD")
		}
	}
}

// TestE2E_NormalPath_CancelledResponseClosesBridge catches a cleanup
// regression where a client disconnect after response headers leaves the
// gateway receiver or VMMD stream blocked on a slow response.
func TestE2E_NormalPath_CancelledResponseClosesBridge(t *testing.T) {
	f := newNormalPathFixture(t, "normal-cancel-response")
	if f == nil {
		return
	}
	_, instance := createNormalPathLiveDeployment(t, f, f.app.ID, "cancel-response")
	f.vmmd.SetVersion(instance.ID, "cancel-response")
	waitForNormalPathResponse(t, f.h, f.host, "normal-path:cancel-response\n", 10*time.Second)
	probe := f.vmmd.InstallCancellationProbe(instance.ID, false, true)
	f.vmmd.SetResponse(instance.ID, e2etest.FakeResponse{
		Status:  http.StatusOK,
		Headers: []*vmmdpb.Header{{Name: "Content-Type", Value: "text/plain"}},
		Body:    []byte(strings.Repeat("y", 512*1024)),
	})

	requestCtx, cancel := context.WithCancel(f.ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(requestCtx, http.MethodGet, f.h.EdgeURL()+"/cancel-response", nil)
	if err != nil {
		t.Fatalf("new cancel response request: %v", err)
	}
	req.Host = f.host
	client := *f.h.HTTPClient()
	responseRelease := make(chan struct{})
	var releaseOnce sync.Once
	releaseResponse := func() { releaseOnce.Do(func() { close(responseRelease) }) }
	defer releaseResponse()
	type responseResult struct {
		resp *http.Response
		err  error
	}
	responseDone := make(chan responseResult, 1)
	go func() {
		resp, requestErr := client.Do(req)
		responseDone <- responseResult{resp: resp, err: requestErr}
		if resp != nil {
			<-responseRelease
			_ = resp.Body.Close()
		}
	}()

	waitNormalPathProbe(t, probe.HeadersSent(), "bridge response headers")
	waitNormalPathProbe(t, probe.FirstResponseBody(), "first response body chunk")
	var result responseResult
	select {
	case result = <-responseDone:
	case <-time.After(5 * time.Second):
		t.Fatal("response headers did not reach the client")
	}
	if result.err != nil {
		t.Fatalf("response request failed before cancellation: %v", result.err)
	}
	if result.resp == nil || result.resp.StatusCode != http.StatusOK {
		t.Fatalf("response before cancellation=%v, want HTTP 200", result.resp)
	}

	cancel()
	releaseResponse()
	waitNormalPathProbe(t, probe.Canceled(), "bridge cancellation")
}

func waitNormalPathProbe(t *testing.T, event <-chan struct{}, name string) {
	t.Helper()
	select {
	case <-event:
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for %s", name)
	}
}
