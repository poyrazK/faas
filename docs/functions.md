# Functions and runtimes

Functions are request handlers packaged with a supported runtime. Explicit
runtime and handler flags make the contract unambiguous:

```bash
gregale deploy --function --runtime node24 --handler handler.handler
gregale deploy --function --runtime python313 --handler handler.handler
```

The current profiles include Node 22/24, Python 312/313, and Go 124. Handlers
must return a bounded response. For ordinary framework APIs, omit `--function`
and let source detection select the app profile.

Use `gregale invoke APP` for a smoke request and `gregale invocations get ID`
for an async result. Runtime-specific limits are in [plans](plans.md).
