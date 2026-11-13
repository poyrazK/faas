package main

import (
	"io"
	"net/http"
)

// NewHTTPDebugTransport isolates a recorder so external transport tests can
// exercise concurrent requests without changing invocation-global wiring.
func NewHTTPDebugTransport(base http.RoundTripper, out io.Writer) (http.RoundTripper, func()) {
	recorder := &httpDebugRecorder{out: out}
	return cliTransport{base: base, debug: recorder}, recorder.close
}

func ExtractHTTPDebugPrefix(args []string) ([]string, bool, error) {
	previous := httpDebugEnabled
	defer func() { httpDebugEnabled = previous }()
	httpDebugEnabled = false
	rest, err := extractAutomationFlag(args)
	return rest, httpDebugEnabled, err
}
