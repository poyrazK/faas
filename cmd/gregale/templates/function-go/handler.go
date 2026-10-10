// Function template for gregale (go124 runtime contract; built with Go 1.26.9).
//
// The go124 runner is a static binary that lives at
// /usr/local/bin/gregale-runner in the layer. It listens on :8080 and talks
// to this binary (at /app/handler) over stdin/stdout with newline-framed
// §4.9 envelopes: one request envelope per line in, one response envelope
// per line out.
//
// This handler speaks the persistent protocol: the runner starts it once,
// before the init snapshot, and sends every request to the same process.
// Anything you set up in main() before the loop (clients, pools, caches) is
// reused across requests. Older runners that start the handler once per
// request still work: they send one envelope and close stdin.
//
// Once the Go SDK is published, github.com/poyrazK/faas/sdk/go/function.Serve
// runs a standard net/http handler instead of envelopes.
//
// No go.mod is shipped on purpose: Go's //go:embed refuses to descend
// into a directory that contains one, and a local go.mod would make a later
// zero-config deploy classify this project as an app. The CLI adds a generated
// go.mod to the upload archive without changing the customer's directory.

package main

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// persistentProtocol tells the runner this binary keeps serving after the
// first request. The runner finds the marker in the compiled binary, so it
// must stay referenced from code that runs.
const persistentProtocol = "FAAS_PERSISTENT_PROTOCOL_V1"

// Envelope matches the §4.9 request contract.
type Envelope struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
	Query   string            `json:"query"`
	BodyB64 string            `json:"body_b64"`
}

// Response matches the §4.9 response contract.
type Response struct {
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers"`
	BodyB64 string            `json:"body_b64"`
}

// reply is the starter's response body.
type reply struct {
	OK     bool   `json:"ok"`
	Path   string `json:"path"`
	Method string `json:"method"`
}

// handle is your function. It runs once per request.
func handle(env Envelope) Response {
	// Encode with encoding/json: request fields can contain quotes and
	// backslashes, and splicing them into a JSON string by hand produces
	// invalid (or field-injected) output.
	body, err := json.Marshal(reply{OK: true, Path: env.Path, Method: env.Method})
	if err != nil {
		return Response{Status: 500}
	}
	return Response{
		Status:  200,
		Headers: map[string]string{"content-type": "application/json"},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}
}

func main() {
	// stdout carries response envelopes. Send ordinary prints to stderr,
	// which reaches `gregale logs`.
	protocol := json.NewEncoder(os.Stdout)
	os.Stdout = os.Stderr

	if os.Getenv("FAAS_PERSISTENT_WORKER") == "1" {
		if err := protocol.Encode(map[string]any{"__faas_ready": true, "protocol": persistentProtocol}); err != nil {
			panic("function-go: write ready handshake: " + err.Error())
		}
	}

	in := bufio.NewReader(os.Stdin)
	for {
		line, readErr := in.ReadBytes('\n')
		if len(bytes.TrimSpace(line)) > 0 {
			var env Envelope
			resp := Response{Status: 500}
			if err := json.Unmarshal(line, &env); err != nil {
				fmt.Fprintln(os.Stderr, "function-go: decode envelope:", err)
			} else {
				resp = handle(env)
			}
			if err := protocol.Encode(resp); err != nil {
				panic("function-go: write response: " + err.Error())
			}
		}
		if readErr == io.EOF {
			return
		}
		if readErr != nil {
			panic("function-go: read request: " + readErr.Error())
		}
	}
}
