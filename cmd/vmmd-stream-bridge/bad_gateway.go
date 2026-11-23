package main

import (
	"log/slog"
	"net/http"
)

// guestBadGatewayBody is what a client sees when the bridge cannot get a
// response from the app. The error itself names the inner network
// (`read tcp 10.0.0.1:60204->10.0.0.2:3000`); that went to public clients
// verbatim (prod hunt r30), so it is logged instead.
const guestBadGatewayBody = "Bad gateway: the app closed the connection or did not send a response"

func guestBadGateway(w http.ResponseWriter, op string, err error) {
	slog.Warn("vmmd-stream-bridge: guest request failed", "op", op, "err", err)
	http.Error(w, guestBadGatewayBody, http.StatusBadGateway)
}
