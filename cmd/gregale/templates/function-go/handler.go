// Function template for gregale (Go 1.24 runtime).
//
// The go124 runner is a static binary that lives at
// /usr/local/bin/gregale-runner in the layer. It listens on :8080, reads
// the §4.9 envelope for each incoming request, and execs this binary
// (at /app/handler) per request. The runner pipes the envelope JSON
// into stdin; your handler writes the response envelope to stdout.
//
// This file is the minimal contract:
//
//   - package main
//   - main() reads the envelope from stdin, builds a response, writes
//     the response envelope to stdout
//   - the runner encodes/decodes the JSON; you only need to do the
//     work in between.
//
// No go.mod is shipped on purpose: Go's //go:embed refuses to descend
// into a directory that contains one, and a local go.mod would make a later
// zero-config deploy classify this project as an app. The CLI adds a generated
// go.mod to the upload archive without changing the customer's directory.

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"os"
)

// Envelope matches the §4.9 request contract. The runner emits it on
// stdin; you decode it, do the work, and write a Response on stdout.
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

func main() {
	// 1. Read the envelope from stdin. The runner writes exactly one
	// JSON object and closes stdin.
	in := bufio.NewReader(os.Stdin)
	var env Envelope
	if err := json.NewDecoder(in).Decode(&env); err != nil {
		// A decode failure is the customer's bug (malformed stdin is
		// not the runner's contract). We exit non-zero so the runner
		// surfaces a 500 to the gateway with stderr attached.
		panic("function-go: decode envelope: " + err.Error())
	}

	// 2. Do the work. For the starter template, echo the path back.
	body := []byte(`{"ok":true,"path":"` + env.Path + `","method":"` + env.Method + `"}`)
	resp := Response{
		Status: 200,
		Headers: map[string]string{
			"content-type": "application/json",
		},
		BodyB64: base64.StdEncoding.EncodeToString(body),
	}

	// 3. Write the response envelope on stdout. The runner decodes
	// the first JSON object it sees and returns the response to
	// the gateway.
	if err := json.NewEncoder(os.Stdout).Encode(resp); err != nil {
		panic("function-go: encode response: " + err.Error())
	}
}
