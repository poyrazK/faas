package fnadapter

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Prod hunt r32, on rc.375:
//   - {"body": {...}} without a content-type was served as
//     application/octet-stream, so a browser downloaded the JSON;
//   - {"status": "ok"} failed with "invalid literal for int()" (Python);
//   - {"status": 201, "headers": {...}} sent itself, headers included, as
//     the body.
//
// The adapter labels the bodies it encodes and keeps the handler's own
// header; raw bytes keep the runner's binary default. A dict is the envelope
// when it has a body or holds only an integer status and headers, so data
// with a numeric status field stays data.
func TestAdapterResponseShapes(t *testing.T) {
	type want struct {
		status      int
		contentType string
		body        string
	}
	cases := []struct {
		path string
		want want
	}{
		{"/json", want{200, "application/json; charset=utf-8", `{"hello":"world"}`}},
		{"/text", want{200, "text/plain; charset=utf-8", "plain"}},
		{"/bytes", want{200, "", "\x00\xff"}},
		{"/own", want{200, "text/html", `{"a":1}`}},
		{"/nobody", want{201, "", ""}},
		{"/data", want{200, "application/json; charset=utf-8", `{"status":"ok","db":"up"}`}},
		{"/order", want{200, "application/json; charset=utf-8", `{"id":7,"status":2}`}},
		{"/strcode", want{202, "text/plain; charset=utf-8", "queued"}},
		// Code review: an error status without a body still sets the status.
		{"/error", want{503, "application/json; charset=utf-8", `{"status":503,"error":"dbdown"}`}},
	}
	for _, tc := range []struct {
		lang, interpreter, adapterName, adapter, handlerName, handler string
	}{
		{"node", "node", "node22.js", Node, "handler.js", `export async function handler(event) {
  switch (event.path) {
    case "/json": return { status: 200, body: { hello: "world" } };
    case "/text": return { status: 200, body: "plain" };
    case "/bytes": return { status: 200, body: Buffer.from([0, 255]) };
    case "/own": return { status: 200, headers: { "Content-Type": "text/html" }, body: { a: 1 } };
    case "/nobody": return { status: 201, headers: { "x-a": "1" } };
    case "/order": return { id: 7, status: 2 };
    case "/strcode": return { statusCode: "202", body: "queued" };
    case "/error": return { status: 503, error: "dbdown" };
    default: return { status: "ok", db: "up" };
  }
}
`},
		{"python", "python3", "handler.py", Python, ".faas-handler.py", `def handler(event, ctx):
    p = event["path"]
    if p == "/json": return {"status": 200, "body": {"hello": "world"}}
    if p == "/text": return {"status": 200, "body": "plain"}
    if p == "/bytes": return {"status": 200, "body": b"\x00\xff"}
    if p == "/own": return {"status": 200, "headers": {"Content-Type": "text/html"}, "body": {"a": 1}}
    if p == "/nobody": return {"status": 201, "headers": {"x-a": "1"}}
    if p == "/order": return {"id": 7, "status": 2}
    if p == "/strcode": return {"statusCode": "202", "body": "queued"}
    if p == "/error": return {"status": 503, "error": "dbdown"}
    return {"status": "ok", "db": "up"}
`},
	} {
		t.Run(tc.lang, func(t *testing.T) {
			interpreter, err := exec.LookPath(tc.interpreter)
			if err != nil {
				t.Skipf("%s is not installed", tc.interpreter)
			}
			dir := t.TempDir()
			files := map[string]string{tc.adapterName: tc.adapter, tc.handlerName: tc.handler, "package.json": `{"type":"module"}`}
			for name, body := range files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			var in strings.Builder
			for _, c := range cases {
				in.WriteString(`{"method":"GET","path":"` + c.path + `","headers":{},"query":"","body_b64":""}` + "\n")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, interpreter, filepath.Join(dir, tc.adapterName))
			cmd.Stdin = strings.NewReader(in.String())
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("adapter: %v\nstderr:\n%s", err, stderr.String())
			}
			var got []want
			scanner := bufio.NewScanner(&stdout)
			for scanner.Scan() {
				var frame struct {
					Status  int               `json:"status"`
					Headers map[string]string `json:"headers"`
					BodyB64 string            `json:"body_b64"`
				}
				if err := json.Unmarshal(scanner.Bytes(), &frame); err != nil {
					t.Fatalf("frame %q: %v", scanner.Text(), err)
				}
				body, err := base64.StdEncoding.DecodeString(frame.BodyB64)
				if err != nil {
					t.Fatalf("body_b64 %q: %v", frame.BodyB64, err)
				}
				w := want{status: frame.Status, body: strings.ReplaceAll(string(body), " ", "")}
				for k, v := range frame.Headers {
					if strings.EqualFold(k, "content-type") {
						w.contentType = v
					}
				}
				got = append(got, w)
			}
			if len(got) != len(cases) {
				t.Fatalf("got %d frames, want %d:\n%s\nstderr:\n%s", len(got), len(cases), stdout.String(), stderr.String())
			}
			for i, c := range cases {
				if got[i] != c.want {
					t.Errorf("%s = %+v, want %+v", c.path, got[i], c.want)
				}
			}
		})
	}
}
