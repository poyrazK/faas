# Functions and runtimes

Functions are request handlers packaged with a supported runtime. Explicit
runtime and handler flags make the contract unambiguous:

```bash
gregale deploy --function --runtime node24 --handler handler.handler
gregale deploy --function --runtime python313 --handler handler.handler
gregale deploy --function --runtime binary
```

The current profiles include Node 22/24, Python 312/313, Go 124, and
[`binary`](runtimes/binary.md): one statically linked executable in any
compiled language (Rust, Zig, C, C++, Go), built from a Dockerfile at the
source root and served by the Go runner. Start from the `function-rust`
template. Handlers
must return a bounded response. Node and Python handlers, and Go and `binary` handlers that
speak the [persistent protocol](runtimes/go124.md#persistent-handlers), stay
loaded between requests. Node and Python handlers are also captured warm in
the init snapshot; a Go or `binary` handler is restarted after a snapshot restore. For ordinary framework APIs, omit `--function`
and let source detection select the app profile.

Use `gregale invoke APP` for a smoke request and `gregale invocations get ID`
for an async result. Runtime-specific limits are in [plans](plans.md).
