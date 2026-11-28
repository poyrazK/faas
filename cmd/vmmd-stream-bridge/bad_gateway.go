package main

import (
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"syscall"
)

// The bridge's own 502 bodies. The transport error names the inner network
// (`read tcp 10.0.0.1:60204->10.0.0.2:3000`) and went to public clients
// verbatim (prod hunt r30); each body keeps only the category a customer can
// act on.
const (
	guestBadGatewayBody = "Bad gateway: the app closed the connection or did not send a response"
	// The bridge cannot tell a crash or OOM from a wrong port (prod hunt r78:
	// an app that exited 3 was told to fix its port), so it names both.
	guestNotListeningBody    = "Bad gateway: the app is not accepting connections on its port. If it crashed or ran out of memory, `gregale logs` shows why; otherwise check that it listens on 0.0.0.0 and $PORT"
	guestForwardFailedBody   = "Bad gateway: Gregale could not forward the request to the app"
	guestBuildRequestFailure = "build outbound request"
)

func guestBadGateway(w http.ResponseWriter, r *http.Request, op string, err error) {
	body := guestBadGatewayBody
	switch {
	case op == guestBuildRequestFailure:
		body = guestForwardFailedBody
	case errors.Is(err, syscall.ECONNREFUSED):
		body = guestNotListeningBody
	}
	// A request its client gave up on is not a fault worth a line; the rest
	// log at debug (vmmd records guest transport failures), without the URL
	// a request-build error carries (its query may hold a token).
	if r != nil && r.Context().Err() == nil {
		var urlErr *url.Error
		if errors.As(err, &urlErr) {
			err = urlErr.Err
		}
		slog.Debug("vmmd-stream-bridge: guest request failed", "op", op, "err", err)
	}
	http.Error(w, body, http.StatusBadGateway)
}
