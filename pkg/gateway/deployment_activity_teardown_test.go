// adr: 696
package gateway

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/gateway/activity"
	"google.golang.org/grpc"
)

func TestDeploymentActivityBridgePanicWaitsForBodyPump(t *testing.T) {
	for _, raw := range []bool{false, true} {
		t.Run(map[bool]string{false: "HTTP recovered panic", true: "raw propagated panic"}[raw], func(t *testing.T) {
			tracker, err := activity.New(uuid.NewString())
			if err != nil {
				t.Fatal(err)
			}
			body := &activityHeldBody{entered: make(chan struct{}), cancelled: make(chan struct{}), release: make(chan struct{})}
			t.Cleanup(body.releaseRead)
			lookup := singleClientLookup{cli: &activityPanicClient{body: body}}
			log := slog.New(slog.NewTextHandler(io.Discard, nil))
			factory := ForwardingReverseProxyWithEvents(lookup, log, nil)
			if raw {
				factory = ForwardingRawReverseProxyWithEventsAndDrain(lookup, log, nil, nil, nil)
			}
			target := Target{AppID: uuid.NewString(), DeploymentID: uuid.NewString(), NodeID: uuid.NewString(), InstanceID: uuid.NewString()}
			writer := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPost, "/", body)
			finished := make(chan any, 1)
			go func() {
				defer func() { finished <- recover() }()
				WithDeploymentActivity(factory, tracker)(target).ServeHTTP(writer, request)
			}()
			select {
			case <-body.cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("panic did not cancel the body reader")
			}
			if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 1 {
				t.Errorf("panic returned while the body pump was still active: %+v", got)
			}
			body.releaseRead()
			select {
			case recovered := <-finished:
				if (recovered != nil) != raw {
					t.Fatalf("panic propagation changed: raw=%v recovered=%v", raw, recovered)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("forwarder did not return after body pump exit")
			}
			if !raw && writer.Code != http.StatusInternalServerError {
				t.Fatalf("recovered panic response changed: %d", writer.Code)
			}
			if got := tracker.Observe(target.AppID, target.DeploymentID); !got.CoverageKnown || got.ActiveForwards != 0 || got.ActivityVersion != 3 {
				t.Fatalf("finished panic retained activity: %+v", got)
			}
		})
	}
}

// Close acknowledges cancellation before an asynchronous reader has unwound.
// Holding Read here makes a false zero deterministic rather than timing based.
type activityHeldBody struct {
	entered, cancelled, release       chan struct{}
	enterOnce, closeOnce, releaseOnce sync.Once
}

func (b *activityHeldBody) Read([]byte) (int, error) {
	b.enterOnce.Do(func() { close(b.entered) })
	<-b.release
	return 0, io.EOF
}
func (b *activityHeldBody) Close() error {
	b.closeOnce.Do(func() { close(b.cancelled) })
	return nil
}
func (b *activityHeldBody) releaseRead() { b.releaseOnce.Do(func() { close(b.release) }) }

type activityPanicClient struct {
	stubVmmdClient
	body *activityHeldBody
}

func (c *activityPanicClient) ForwardHTTPStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardHTTPStreamRequest, vmmdpb.ForwardHTTPStreamResponse], error) {
	return &activityPanicHTTPStream{body: c.body}, nil
}
func (c *activityPanicClient) ForwardRawStream(context.Context, ...grpc.CallOption) (grpc.BidiStreamingClient[vmmdpb.ForwardRawRequest, vmmdpb.ForwardRawResponse], error) {
	return &activityPanicRawStream{body: c.body}, nil
}

type activityPanicHTTPStream struct {
	grpc.ClientStream
	body *activityHeldBody
}

func (*activityPanicHTTPStream) Send(*vmmdpb.ForwardHTTPStreamRequest) error { return nil }
func (*activityPanicHTTPStream) CloseSend() error                            { return nil }
func (s *activityPanicHTTPStream) Recv() (*vmmdpb.ForwardHTTPStreamResponse, error) {
	<-s.body.entered
	panic("receiver failure")
}

type activityPanicRawStream struct {
	grpc.ClientStream
	body *activityHeldBody
}

func (*activityPanicRawStream) Send(*vmmdpb.ForwardRawRequest) error { return nil }
func (*activityPanicRawStream) CloseSend() error                     { return nil }
func (s *activityPanicRawStream) Recv() (*vmmdpb.ForwardRawResponse, error) {
	<-s.body.entered
	panic("receiver failure")
}
