package gateway

import (
	"errors"
	"io"
	"net/http"

	"github.com/onebox-faas/faas/pkg/api"
	"github.com/onebox-faas/faas/pkg/grpcerr"
)

// Node refusal happens before guest execution. It is a bounded overflow
// outcome, not evidence that the placement is stale or an app replay signal.
func writeNodeAdmissionRefusal(w http.ResponseWriter, err error) bool {
	p, ok := grpcerr.FromStatus(err)
	if !ok || p == nil || (p.Code != api.CodeConcurrencyThrottled && p.Code != api.CodeHTTPAdmissionUnavailable) {
		return false
	}
	if p.Code == api.CodeConcurrencyThrottled {
		p.Title = "App busy"
		p.Detail = "This app is handling its current request limit. Retry shortly."
	} else {
		p.Title = "Request admission unavailable"
		p.Detail = "Gregale could not verify this app's request capacity. Retry shortly."
	}
	p = p.WithHeader("Retry-After", "1")
	api.WriteProblem(w, p)
	return true
}

// gRPC reports server termination as Send EOF; Recv carries the terminal
// status. Resolve it before classifying an uncommitted forwarding failure.
// Buffered frames are discarded because the request send has already failed.
// The receive remains on the original bounded RPC context. adr: 531
func forwardingSendStatus[Response any](sendErr error, stream interface{ Recv() (*Response, error) }) error {
	if !errors.Is(sendErr, io.EOF) {
		return sendErr
	}
	for {
		_, err := stream.Recv()
		if err == nil {
			continue
		}
		if errors.Is(err, io.EOF) {
			return sendErr
		}
		return err
	}
}
