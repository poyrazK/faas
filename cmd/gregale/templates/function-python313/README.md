# function-python313

A minimal Python 3.13 function handler.

## Deploy

```
gregale deploy --template function-python313 --name <slug>
```

The CLI forces `--runtime python313 --handler handler.handler` so
the function runner wires the invocation to your exported `handler`.
The underlying handler filename is `/app/handler.py` (version-neutral
on the wire — same as `python312`).

## Invoke

```
gregale open <slug>   # browser test page
```

## Differences from `function-python`

- Runtime is `python313` (Python 3.13, RHEL/Fedora default) instead of
  `python312`.
- The OCI base image in `images/runner-python313.Dockerfile` is bound
  to a Python 3.13 digest at build time; the underlying Python version
  is operator-controlled via `FAAS_DEPLOY_BASE_REF_PYTHON313`.
- All other behavior is identical — same envelope contract (§4.9),
  same metric set, same handler shape, same handler filename.

See `docs/runtimes/python313.md` for the per-runtime contract.
