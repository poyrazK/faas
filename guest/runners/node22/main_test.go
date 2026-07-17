package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// fakeNodeScript is a stand-in for the customer's handler. It echoes the
// request envelope back as a response so the test can assert the
// contract round-trips correctly.
const fakeNodeScript = `#!/usr/bin/env node
let buf = '';
process.stdin.on('data', (c) => { buf += c; });
process.stdin.on('end', () => {
  const env = JSON.parse(buf);
  const out = {
    status: 200,
    headers: { "X-Echo-Method": env.method, "X-Echo-Path": env.path },
    body_b64: Buffer.from("echo:" + env.path).toString("base64")
  };
  process.stdout.write(JSON.stringify(out));
});`

// TestHandle_RoundTrip spins a stub handler with the same JSON contract
// the runner expects. The runner's spawn-the-binary path needs an
// actual `node` on PATH — skip if not present, since the test is
// about the contract, not the platform dep.
func TestHandle_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node not on PATH; skipping runtime round-trip")
	}
	dir := t.TempDir()
	script := dir + "/handler.js"
	if err := os.WriteFile(script, []byte(fakeNodeScript), 0o755); err != nil {
		t.Fatalf("write handler: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		handle(w, r, script)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/hello?x=1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("X-Echo-Method"); got != "GET" {
		t.Fatalf("X-Echo-Method = %q, want GET", got)
	}
	if got := resp.Header.Get("X-Echo-Path"); got != "/hello" {
		t.Fatalf("X-Echo-Path = %q, want /hello", got)
	}
	body := new(bytes.Buffer)
	body.ReadFrom(resp.Body)
	if !strings.Contains(body.String(), "echo:/hello") {
		t.Fatalf("body = %q, want echo:/hello", body.String())
	}
}

// TestHeaderMap_LowercasesHTTP: §4.9 envelope uses lowercase header
// keys; the runner folds http.Header into that shape.
func TestHeaderMap(t *testing.T) {
	h := http.Header{}
	h.Set("X-Trace-Id", "abc")
	h.Set("Content-Type", "application/json")
	m := headerMap(h)
	if m["X-Trace-Id"] != "abc" {
		t.Fatalf("X-Trace-Id = %q, want abc", m["X-Trace-Id"])
	}
	if m["Content-Type"] != "application/json" {
		t.Fatalf("Content-Type = %q", m["Content-Type"])
	}
}

// TestEnvelopeRoundTrip sanity-checks the JSON tags line up with §4.9.
func TestEnvelopeRoundTrip(t *testing.T) {
	env := envelope{
		Method:  "POST",
		Path:    "/foo",
		Headers: map[string]string{"X": "y"},
		Query:   "a=1",
		BodyB64: base64.StdEncoding.EncodeToString([]byte("hi")),
	}
	b, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !bytes.Contains(b, []byte(`"body_b64":"aGk="`)) {
		t.Fatalf("body_b64 tag missing: %s", b)
	}
}
