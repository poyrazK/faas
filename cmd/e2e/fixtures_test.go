//go:build metal

// fixtures_test.go — minimal customer source tarballs for the M6 §14
// orchestrator e2e (issue #57). Built at test runtime so the repo doesn't
// carry checked-in binary blobs; the contents are the smallest realistic
// examples for each framework the build VM supports (ADR-004):
//
//   NodeFixture      — package.json + index.js, no native deps. Railpack
//                      auto-detects from package.json.
//   PythonFixture    — requirements.txt + app.py with Flask. Railpack
//                      auto-detects from requirements.txt.
//   DockerfileFixture — single-stage Dockerfile FROM busybox (no egress
//                      needed). buildctl --frontend dockerfile.
//
// Why runtime-built, not //go:embed: the tarballs are tiny (a few hundred
// bytes each) but committing them creates a maintenance trap — any change
// to apid's tarball validator (cmd/apid/deploy_inputs.go::validateTarballShape)
// would silently invalidate the checked-in blob, and the test would only
// fail on the next CI run. Constructing from archive/tar at test time makes
// the fixture self-documenting and the round-trip explicit.
//
// Build tag: metal. The fixtures themselves don't need /dev/kvm — they're
// pure Go archive/tar — but they live next to the test that consumes them
// (cmd/e2e/build_metal_test.go) which IS metal-gated.

package e2e_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"testing"
	"time"
)

// NodeFixture returns the bytes of a minimal Node 22 source tarball.
// Railpack auto-detects Node from package.json (spec §4.5); the contents
// are the smallest app that survives `railpack build` without native deps.
func NodeFixture(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-node",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');
http.createServer((req, res) => {
  if (req.url === '/healthz') {
    res.writeHead(200, {'content-type': 'text/plain'});
    res.end('ready\n');
    return;
  }
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end('hello from faas (node fixture)\n');
}).listen(3000, () => console.log('node fixture listening on :3000'));
`
	files := map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// FunctionNodeFixture returns the bytes of a minimal function-tier
// source tarball (issue #737 / ADR-083): a single `handler.js` at
// the project root, no app markers, no package.json. This is the
// exact shape a customer gets when they run `gregale deploy` in a
// directory containing only a hand-written handler.js — the CLI's
// auto-detect path picks shapeFunction and emits the function wire
// shape (`runtime=node22` + `handler=handler.handler`). The fixture
// exists so the metal 9th subtest can drive the apid function path
// at the wire boundary without spinning up a cold build (the unit
// test in cmd/apid/deploy_inputs_test.go covers the function
// deploy → live path). The body is a no-op handler; the test stops
// at the 202 response + `apps.type=function` row check, so the body
// content is intentionally minimal — its only purpose is to exist
// inside a valid tarball.
func FunctionNodeFixture(t *testing.T) []byte {
	t.Helper()
	const handlerJS = `// Issue #737 / ADR-083 function-tier fixture.
// Wire handler is "handler.handler" (literal) per
// cmd/gregale/templates/function-node/handler.js convention;
// imaged's function-layer manifest rewrites this to /app/handler.js
// in the guest microVM.
exports.handler = async () => ({ statusCode: 200, body: 'function ok' });
`
	files := map[string]string{
		"handler.js": handlerJS,
	}
	return buildTarGz(t, files)
}

// NodeFixturePort returns the bytes of a minimal Node 22 source tarball
// whose index.js reads the `PORT` env var (issue #460 / ADR-053, PR-C).
// The platform contract is that guest-init always stamps PORT
// (EffectivePort() resolves to 8080 when the override is unset, and
// to the override value otherwise), so the fixture mirrors that by
// reading the env var with no source-level fallback — if guest-init
// ever drops the stamp, the runner would bind :0 and the
// TestDeployOverridePortMetal content assertion would catch the
// regression. Callers wanting a hardcoded :3000 bind should keep
// using NodeFixture (the legacy fixture that pre-dates PR-C).
func NodeFixturePort(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-node-port",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');
const port = process.env.PORT;
http.createServer((req, res) => {
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end('hello from faas (override-port:' + port + ')\n');
}).listen(parseInt(port, 10), () => console.log('node fixture listening on :' + port));
`
	files := map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// NodeFixtureHealthcheck returns the bytes of a minimal Node 22 source
// tarball whose index.js binds `:8080` (the host's stable readiness
// probe target per ADR-009 + portnorm ladder) and registers a /healthz
// route returning 200 (issue #460 / ADR-053 / ADR-057, PR-D). The
// healthcheck body includes "override-healthz:<readyFlag>" so the
// test can confirm readiness on the /healthz path independently of
// the main / response. Binds :8080 directly — portnorm re-exposes the
// customer bind on :8080 inside the guest so the host's
// `GET <HostIP>:8080/healthz` reaches the fixture.
//
// Why explicit bind on :8080 (no PORT env): waitReady's probe target
// is hard-wired to :8080 in fcvm/manager.go. The override-port PR-C
// fixture reads PORT because EffectivePort() can stamp 9090, but
// PR-D's probe port doesn't change with override-port. If a future
// ADR widens waitReady to probe the override port directly (rejected
// in ADR-057 §Decision 2), NodeFixturePort would be the right
// starting point — for now, hardcoded :8080 is the contract.
func NodeFixtureHealthcheck(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-node-healthz",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');
let ready = false;
http.createServer((req, res) => {
  if (req.url === '/healthz') {
    res.writeHead(ready ? 200 : 503, {'content-type': 'text/plain'});
    res.end('override-healthz:' + (ready ? 'ready' : 'not-ready') + '\n');
    return;
  }
  // Mark ready on first non-/healthz request — exercises the
  // PR-D "retry-until-2xx" loop on the first wake, since waitReady
  // may probe before the runner has handled /healthz.
  if (!ready) ready = true;
  res.writeHead(200, {'content-type': 'text/plain'});
  res.end('hello from faas (override-healthz)\n');
}).listen(8080, () => console.log('healthz fixture listening on :8080'));
`
	files := map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// NodeFixtureStreaming returns the bytes of a minimal Node 22 source
// tarball whose index.js emits a Server-Sent-Events stream with
// controlled chunk+interval pacing (issue #471 / ADR-047 PR-D).
// The fixture is the load-bearing source for the metal-driven
// streaming acceptance tests in cmd/e2e/streaming_metal_test.go:
//
//   - GET /sse?chunks=N&size=B&interval=ms  → emit N event-stream
//     chunks of B bytes each at the given interval. Each chunk is
//     flushed immediately; the response carries Transfer-Encoding:
//     chunked implicitly via the streaming HTTP/1.1 socket.
//   - GET /payload?bytes=N  → emit exactly N bytes of plain text
//     in a single 200 response (used by the plan-matrix AC #3 test
//     to assert Free returns 413 streaming_not_available before
//     exhausting the 100 MB cap; Hobby+ actually streams the bytes).
//   - GET /healthz  → 200 + "stream-ready" (matches the
//     NodeFixtureHealthcheck readiness probe so waitReady accepts
//     the fixture).
//
// The Node `setInterval` timer is bounded by the runtime's
// setTimeout clamp (≈24.8 days as Int32 ms); the metal tests pass
// realistic durations (200 ms ≤ interval ≤ 1000 ms) way inside that
// range. The runner binds :8080 (the host's stable probe port) so
// waitReady reaches the fixture without a `PORT` env stamp.
func NodeFixtureStreaming(t *testing.T) []byte {
	t.Helper()
	const pkgJSON = `{
  "name": "faas-fixture-node-streaming",
  "version": "1.0.0",
  "private": true,
  "engines": {"node": "22"},
  "scripts": {"start": "node index.js"},
  "dependencies": {}
}
`
	const indexJS = `const http = require('http');

function drain(req, res, body) {
  // The customer contract is "stream N bytes regardless of body
  // size"; the runner doesn't read req, but if the request
  // carries a body we discard it so the connection doesn't stall
  // when the client closes early (F3 tripwire from PR-B+PR-C).
  req.on('data', () => {});
  req.on('end', () => {
    res.end(body);
  });
  req.on('close', () => {
    try { res.end(); } catch (_) {}
  });
}

http.createServer((req, res) => {
  const url = new URL(req.url, 'http://localhost');
  if (url.pathname === '/healthz') {
    res.writeHead(200, {'content-type': 'text/plain'});
    res.end('stream-ready');
    return;
  }
  if (url.pathname === '/sse') {
    const chunks = parseInt(url.searchParams.get('chunks') || '10', 10);
    const size = parseInt(url.searchParams.get('size') || '1024', 10);
    const interval = parseInt(url.searchParams.get('interval') || '200', 10);
    res.writeHead(200, {
      'content-type': 'text/event-stream',
      'cache-control': 'no-cache',
    });
    let n = 0;
    const handle = setInterval(() => {
      try {
        const payload = 'data: ' + 'x'.repeat(size) + '\n\n';
        res.write(payload);
        n++;
        if (n >= chunks) {
          clearInterval(handle);
          res.end();
        }
      } catch (_) {
        clearInterval(handle);
      }
    }, interval);
    req.on('close', () => clearInterval(handle));
    return;
  }
  if (url.pathname === '/payload') {
    const bytes = parseInt(url.searchParams.get('bytes') || '1024', 10);
    res.writeHead(200, {'content-type': 'application/octet-stream'});
    drain(req, res, 'x'.repeat(bytes));
    return;
  }
  res.writeHead(404, {'content-type': 'text/plain'});
  res.end('not found');
}).listen(8080, () => console.log('streaming fixture listening on :8080'));
`
	files := map[string]string{
		"package.json":     pkgJSON,
		"index.js":         indexJS,
		".faas-fixture":    "node22\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// PythonFixture returns the bytes of a minimal Python 3.12 source tarball
// with Flask as the single dep. Railpack auto-detects from requirements.txt
// and uses uvicorn+gunicorn under the hood; we don't pin that here because
// it's the runner's choice — the fixture only has to give Railpack a
// recognizable project shape.
func PythonFixture(t *testing.T) []byte {
	t.Helper()
	const reqs = "flask==3.0.3\n"
	const appPy = `from flask import Flask
app = Flask(__name__)

@app.route("/")
def hello():
    return "hello from faas (python fixture)\n"

@app.route("/healthz")
def healthz():
    return "ready\n"

if __name__ == "__main__":
    app.run(host="0.0.0.0", port=3000)
`
	files := map[string]string{
		"requirements.txt": reqs,
		"app.py":           appPy,
		".faas-fixture":    "python312\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// DockerfileFixture returns the bytes of a tarball whose root contains a
// Dockerfile. We use `FROM busybox` because the Lima builder VM has
// busybox in its base rootfs (faas-builder-bas), and busybox-static is
// also installed via the apt-get line in deploy/lima/faas-metal.yaml —
// no egress required, no Docker Hub dependency, exercises the buildctl
// --frontend dockerfile path end-to-end without a real registry.
//
// The CMD uses busybox httpd so waitReady can probe :3000 the same way it
// does for the node/python fixtures.
func DockerfileFixture(t *testing.T) []byte {
	t.Helper()
	// Plain string concat — using a raw string with interpolated time.Now
	// trips go vet's const-evaluation heuristic on the (test fixture) parens.
	dockerfile := "FROM busybox:1.37\n" +
		"LABEL org.opencontainers.image.title=\"faas-fixture-dockerfile\"\n" +
		"LABEL org.opencontainers.image.source=\"https://github.com/onebox-faas/faas test fixture\"\n" +
		"LABEL faas.build.token=\"" + time.Now().UTC().Format(time.RFC3339Nano) + "\"\n" +
		"RUN mkdir -p /public && printf 'hello from faas (dockerfile fixture)\\n' > /public/index.html && printf 'ready\\n' > /public/healthz\n" +
		"RUN adduser -D -u 1000 app\n" +
		"USER app\n" +
		"EXPOSE 3000\n" +
		"CMD [\"/bin/busybox\", \"httpd\", \"-f\", \"-p\", \"3000\", \"-h\", \"/public\"]\n"
	files := map[string]string{
		"Dockerfile":       dockerfile,
		".faas-fixture":    "dockerfile\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// GoFixture returns the bytes of a minimal Go 1.24 source tarball for the
// Railpack `--plan go` build path. The Railpack go plan emits a static
// binary at /app/server (per upstream catalog); imaged.handleDeployment
// stamps the layer manifest from the OCI image's `Cmd` via
// manifestFromImageConfig, so the static binary becomes the app's
// entrypoint. The fixture's main.go listens on :3000 to match the
// existing waitReady probe shape (and the node/python fixtures above).
//
// The fixture is the first "static binary" deploy on the app path —
// previously every runtime needed a runtime-scaffold runner shim. The
// no-shim path is the load-bearing novelty here.
func GoFixture(t *testing.T) []byte {
	t.Helper()
	const goMod = "module example.com/faas-go-fixture\n\n" +
		"go 1.24\n"
	const mainGo = `package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from faas (go fixture)\n")
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ready\n")
	})
	if err := http.ListenAndServe(":3000", nil); err != nil {
		panic(err)
	}
}
`
	files := map[string]string{
		"go.mod":           goMod,
		"main.go":          mainGo,
		".faas-fixture":    "go124\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// GoDockerfileFixture returns the bytes of a tarball whose root contains
// a multi-stage Dockerfile (FROM golang AS build → FROM scratch) and a
// go.mod + main.go. Exercises the buildctl --frontend dockerfile path
// for go124 (railpack is bypassed entirely; the customer writes the
// Dockerfile). Used by the build_metal_test `go124-dockerfile-tarball`
// subtest to pin the buildctl path for the new runtime.
func GoDockerfileFixture(t *testing.T) []byte {
	t.Helper()
	return goDockerfileFixture(t, "1.24-bookworm", "go124-dockerfile")
}

// GoAlpineDockerfileFixture mirrors GoDockerfileFixture but uses
// `FROM golang:1.24-alpine AS build` so the customer's compiled binary
// is a fully-static musl-linked executable. Exercises the buildctl
// path for runtime=go124-alpine (Tier 2 PR) — the produced /app/server
// must load on a musl base (`/srv/fc/base/runner-go124-alpine.ext4`)
// and would fail with `exec format error` against the bookworm base.
// Used by the build_metal_test `go124-alpine-tarball` subtest.
func GoAlpineDockerfileFixture(t *testing.T) []byte {
	t.Helper()
	return goDockerfileFixture(t, "1.24-alpine", "go124-alpine-dockerfile")
}

// goDockerfileFixture is the shared fixture body for the bookworm +
// alpine go124 paths. The bookworm + alpine subtests differ only in the
// build-stage FROM line and the .faas-fixture label (used by buildctl's
// per-fixture cache key + the metal subtest's assertion).
func goDockerfileFixture(t *testing.T, golangTag, label string) []byte {
	t.Helper()
	const goMod = "module example.com/faas-go-dockerfile-fixture\n\n" +
		"go 1.24\n"
	const mainGo = `package main

import (
	"fmt"
	"net/http"
)

func main() {
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "hello from faas (go dockerfile fixture)\n")
	})
	http.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "ready\n")
	})
	if err := http.ListenAndServe(":3000", nil); err != nil {
		panic(err)
	}
}
`
	dockerfile := "FROM golang:" + golangTag + " AS build\n" +
		"WORKDIR /src\n" +
		"COPY go.mod main.go ./\n" +
		"RUN CGO_ENABLED=0 go build -o /out/server ./\n" +
		"FROM scratch\n" +
		"COPY --from=build /out/server /app/server\n" +
		"LABEL org.opencontainers.image.title=\"faas-fixture-go-dockerfile\"\n" +
		"LABEL faas.build.token=\"" + time.Now().UTC().Format(time.RFC3339Nano) + "\"\n" +
		"EXPOSE 3000\n" +
		"CMD [\"/app/server\"]\n"
	files := map[string]string{
		"Dockerfile":       dockerfile,
		"go.mod":           goMod,
		"main.go":          mainGo,
		".faas-fixture":    label + "\n",
		"faas-build-token": time.Now().UTC().Format(time.RFC3339Nano) + "\n",
	}
	return buildTarGz(t, files)
}

// buildTarGz packs a flat name→content map into a gzipped tar. Files are
// stored with mode 0644 and a fixed mtime so the tar headers are stable
// across runs. The body bytes are *not* byte-stable: every fixture embeds
// a `faas-build-token` whose value is `time.Now()` at construction time,
// so two fixtures called within the same tick still get distinct content
// hashes. apid's validateTarballShape doesn't care; the timestamps exist
// to make duplicate-build dedup impossible (a CI cache that mistook two
// calls' tarballs for one would mask a fixture regression).
func buildTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		hdr := &tar.Header{
			Name:     name,
			Mode:     0o644,
			Size:     int64(len(body)),
			Typeflag: tar.TypeReg,
			ModTime:  time.Unix(0, 0),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("buildTarGz: WriteHeader(%s): %v", name, err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatalf("buildTarGz: Write(%s): %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("buildTarGz: tar.Close: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("buildTarGz: gzip.Close: %v", err)
	}
	return buf.Bytes()
}
