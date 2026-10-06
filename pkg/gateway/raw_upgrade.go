package gateway

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"

	vmmdpb "github.com/onebox-faas/faas/api/proto/onebox/faas/vmmd/v1"
	"github.com/onebox-faas/faas/pkg/api"
)

// A 101 leaves net/http's body framing. The hijacked connection owns both
// directions, including client bytes already buffered with the request head.
func beginRawUpgrade(w http.ResponseWriter) (net.Conn, io.ReadCloser, error) {
	writer := w
	for {
		if _, ok := writer.(http.Hijacker); ok {
			break
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			return nil, nil, http.ErrNotSupported
		}
		writer = unwrapper.Unwrap()
	}
	w.WriteHeader(http.StatusSwitchingProtocols)
	conn, buffered, err := http.NewResponseController(w).Hijack()
	if err != nil {
		return nil, nil, err
	}
	reader := struct {
		io.Reader
		io.Closer
	}{buffered.Reader, conn}
	return conn, reader, nil
}

type rawUpgradeOutput struct {
	net.Conn
	response http.ResponseWriter
}

type rawRequestSendError struct{ err error }

func (e *rawRequestSendError) Error() string { return e.err.Error() }
func (e *rawRequestSendError) Unwrap() error { return e.err }

func (o *rawUpgradeOutput) Write(data []byte) (int, error) {
	n, err := o.Conn.Write(data)
	for writer := o.response; writer != nil; {
		if recorder, ok := writer.(*statusRecorder); ok && n > 0 {
			recorder.Bytes += int64(n)
			recorder.mirrorSourceCapture.write(data[:n])
		}
		unwrapper, ok := writer.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		writer = unwrapper.Unwrap()
	}
	return n, err
}

// Send any HTTP request body before the guest's response, then keep the RPC
// open until that response chooses a socket reader or an ordinary rejection.
func rawRequestBodyLoop(ctx context.Context, body io.ReadCloser,
	stream interface {
		Send(*vmmdpb.ForwardRawRequest) error
		CloseSend() error
	}, upgrade <-chan io.ReadCloser, touch func(), cancel context.CancelFunc,
	metrics *Metrics, plan api.Plan,
) <-chan error {
	result := make(chan error, 1)
	go func() {
		err := sendRawRequestBytes(ctx, body, stream, touch, metrics, plan)
		if err == nil {
			select {
			case reader := <-upgrade:
				if reader != nil {
					err = sendRawRequestBytes(ctx, reader, stream, touch, metrics, plan)
					// A disconnected upgraded client must cancel the receive side
					// too; request.Context no longer owns a hijacked socket.
					cancel()
				}
			case <-ctx.Done():
				err = ctx.Err()
			}
		} else {
			cancel()
		}
		_ = stream.CloseSend()
		result <- err
	}()
	return result
}

func sendRawRequestBytes(ctx context.Context, reader io.Reader,
	stream interface {
		Send(*vmmdpb.ForwardRawRequest) error
	}, touch func(),
	metrics *Metrics, plan api.Plan,
) error {
	cr, stop := newCtxReader(ctx, reader)
	defer stop()
	buffer := make([]byte, 8*1024)
	for {
		n, err := cr.Read(buffer)
		if n > 0 {
			touch()
			if sendErr := stream.Send(&vmmdpb.ForwardRawRequest{
				Frame: &vmmdpb.ForwardRawRequest_BodyChunk{BodyChunk: append([]byte(nil), buffer[:n]...)},
			}); sendErr != nil {
				return &rawRequestSendError{err: sendErr}
			}
			if metrics != nil {
				metrics.AddWSSessionBytes(string(plan), WSDirectionTx, int64(n))
			}
		}
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
	}
}
