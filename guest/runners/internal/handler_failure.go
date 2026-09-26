package internal

import (
	"encoding/json"
	"errors"
	"net/http"
	"os/exec"
	"strings"

	"github.com/onebox-faas/faas/pkg/api"
)

// WriteHandlerFailure answers a request whose handler invocation failed.
//
// A failure of the customer's own code — the handler process exited
// non-zero (an uncaught exception or panic), or it wrote a response the
// runner cannot decode — is answered with the same bounded envelope the
// persistent adapters emit ({"error":"handler_error", ...}, HTTP 500).
// gatewayd-internal classifies that envelope as a terminal application
// failure, as the runtime docs promise. Answering these with a plain-text
// 500 made them look like infrastructure failures, which the durable
// invocation contract retries: the failing handler — and its side effects —
// ran again until the retry budget was spent.
//
// Everything else keeps the plain retryable 500: timeouts, a cancelled
// request, a handler killed by a signal (the OOM killer, the runner's own
// deadline), a persistent worker's broken pipe, a failed exec.
func WriteHandlerFailure(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil || !isCustomerCodeFailure(err) {
		http.Error(w, "handler error", http.StatusInternalServerError)
		return
	}
	message := "handler process failed"
	if isProtocolFailure(err) {
		message = "handler returned an invalid response"
	}
	body, _ := json.Marshal(map[string]string{
		"error":         "handler_error",
		"message":       message,
		"invocation_id": r.Header.Get(api.InvocationIDHeader),
	})
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(http.StatusInternalServerError)
	_, _ = w.Write(body)
}

func isCustomerCodeFailure(err error) bool {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		// ExitCode is -1 when a signal ended the process.
		return exitErr.ExitCode() > 0
	}
	return isProtocolFailure(err)
}

// isProtocolFailure matches the one-shot runners' own "decode response"
// wrap. The persistent pool's "workerpool: decode response" is excluded:
// the adapter owns that stream, so a bad frame there is not the handler's.
func isProtocolFailure(err error) bool {
	return err != nil && strings.HasPrefix(err.Error(), "decode response")
}
