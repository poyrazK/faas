# gregale-sandbox-e2b

An E2B-compatible Python client for Gregale sandbox sessions. Code written against the E2B Python SDK moves by changing the import and the API key; supported calls keep their names and arguments. It uses only the standard library.

> Not published yet. Sandbox sessions are behind the internal agents capability until launch (ADR-20261027090013 §12). Written from E2B's public documentation; it copies no E2B code, and "E2B" is used only to describe compatibility. The TypeScript twin is `sdk/sandbox-e2b`.

```python
from gregale_sandbox_e2b import Sandbox

with Sandbox.create(timeout=300, ports=[3000]) as sbx:  # GREGALE_API_KEY
    sbx.files.write("app/main.py", "print('hi')")
    print(sbx.commands.run("python3 app/main.py").stdout)
    server = sbx.commands.run("python3 -m http.server 3000", background=True)
    print(f"https://{sbx.get_host(3000)}", sbx.preview_token(3000))
    server.kill()
```

`docs/sandbox-migration.md` lists every call and difference. In short:

- Synchronous only: `Sandbox`, not `AsyncSandbox`. Times are in seconds, as in E2B's Python SDK.
- `get_host(port)` answers only for exposed ports: pass `ports=` to `create`, or call `sandbox.expose_port(port)`.
- Files inside `/workspace` use Gregale's files API; other paths go through commands and are capped at 256 KB.
- The default network mode is `registries`; `allow_internet_access=False` is `none`.
- A background command's `on_stdout`/`on_stderr` run while you `wait()` on it or iterate it.
- Unsupported calls (`pty`, stdin, `files.watch_dir`, `pause`, MCP) raise `UnsupportedException` naming the call and the alternative.

Gregale options: `network`, `ram_mb`, `lifetime`, `ports`, `port_visibility`, `snapshot_id`, `ready_timeout`, `api_base` (default `GREGALE_API_BASE`, then `https://api.gregale.dev`).

Tests: `make test-sandbox-e2b-python` (Python 3.12+).
