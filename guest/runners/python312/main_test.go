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

const fakePyScript = `#!/usr/bin/env python3
import sys, json, base64
env = json.loads(sys.stdin.read())
out = {
    "status": 200,
    "headers": {"X-Echo-Method": env["method"], "X-Echo-Path": env["path"]},
    "body_b64": base64.b64encode(("echo:" + env["path"]).encode()).decode(),
}
sys.stdout.write(json.dumps(out))
`

func TestHandle_RoundTrip(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not on PATH; skipping runtime round-trip")
	}
	dir := t.TempDir()
	script := dir + "/handler.py"
	if err := os.WriteFile(script, []byte(fakePyScript), 0o755); err != nil {
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
